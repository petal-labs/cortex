package sqlite

import (
	"context"
	"encoding/json"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// Export exports all data for a namespace.
func (b *Backend) Export(ctx context.Context, namespace string, opts storage.ExportOptions) (*storage.ExportData, error) {
	data := &storage.ExportData{
		Version:    1,
		Namespace:  namespace,
		ExportedAt: time.Now().Unix(),
	}

	var err error

	if opts.IncludeConversations {
		data.Threads, data.Messages, err = b.exportConversations(ctx, namespace, opts.IncludeEmbeddings)
		if err != nil {
			return nil, err
		}
	}

	if opts.IncludeKnowledge {
		data.Collections, data.Documents, data.Chunks, err = b.exportKnowledge(ctx, namespace, opts.IncludeEmbeddings)
		if err != nil {
			return nil, err
		}
	}

	if opts.IncludeContext {
		data.ContextEntries, data.ContextHistory, err = b.exportContext(ctx, namespace)
		if err != nil {
			return nil, err
		}
	}

	if opts.IncludeEntities {
		data.Entities, data.EntityAliases, data.Mentions, data.Relationships, err = b.exportEntities(ctx, namespace, opts.IncludeEmbeddings)
		if err != nil {
			return nil, err
		}
	}

	return data, nil
}

func (b *Backend) exportConversations(ctx context.Context, namespace string, includeEmbeddings bool) ([]*storage.ThreadExport, []*storage.MessageExport, error) {
	// Export threads
	rows, err := b.db.QueryContext(ctx, `
		SELECT id, namespace, title, summary, metadata, created_at, updated_at
		FROM threads WHERE namespace = ?
		ORDER BY created_at
	`, namespace)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var threads []*storage.ThreadExport
	for rows.Next() {
		var t types.Thread
		var metadata []byte
		var createdAt, updatedAt int64

		err := rows.Scan(&t.ID, &t.Namespace, &t.Title, &t.Summary, &metadata, &createdAt, &updatedAt)
		if err != nil {
			return nil, nil, err
		}

		if len(metadata) > 0 {
			json.Unmarshal(metadata, &t.Metadata)
		}
		t.CreatedAt = time.Unix(createdAt, 0)
		t.UpdatedAt = time.Unix(updatedAt, 0)

		threads = append(threads, &storage.ThreadExport{Thread: &t})
	}

	// Export messages
	query := `
		SELECT m.id, m.namespace, m.thread_id, m.role, m.content, m.metadata, m.summarized, m.created_at
	`
	if includeEmbeddings {
		query += `, e.embedding`
	}
	query += ` FROM messages m`
	if includeEmbeddings {
		query += ` LEFT JOIN message_embeddings e ON m.id = e.message_id`
	}
	query += ` WHERE m.namespace = ? ORDER BY m.created_at`

	rows, err = b.db.QueryContext(ctx, query, namespace)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var messages []*storage.MessageExport
	for rows.Next() {
		var m types.Message
		var metadata []byte
		var summarized int
		var createdAt int64
		var embedding []byte

		var scanArgs []interface{}
		scanArgs = append(scanArgs, &m.ID, &m.Namespace, &m.ThreadID, &m.Role, &m.Content, &metadata, &summarized, &createdAt)
		if includeEmbeddings {
			scanArgs = append(scanArgs, &embedding)
		}

		err := rows.Scan(scanArgs...)
		if err != nil {
			return nil, nil, err
		}

		if len(metadata) > 0 {
			json.Unmarshal(metadata, &m.Metadata)
		}
		m.Summarized = summarized != 0
		m.CreatedAt = time.Unix(createdAt, 0)

		export := &storage.MessageExport{Message: &m}
		if includeEmbeddings && len(embedding) > 0 {
			export.Embedding = decodeVectorBinary(embedding)
		}
		messages = append(messages, export)
	}

	return threads, messages, nil
}

func (b *Backend) exportKnowledge(ctx context.Context, namespace string, includeEmbeddings bool) ([]*types.Collection, []*storage.DocumentExport, []*storage.ChunkExport, error) {
	// Export collections
	rows, err := b.db.QueryContext(ctx, `
		SELECT id, namespace, name, description, chunk_config, created_at
		FROM collections WHERE namespace = ?
		ORDER BY created_at
	`, namespace)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	var collections []*types.Collection
	for rows.Next() {
		var c types.Collection
		var chunkConfig []byte
		var createdAt int64

		err := rows.Scan(&c.ID, &c.Namespace, &c.Name, &c.Description, &chunkConfig, &createdAt)
		if err != nil {
			return nil, nil, nil, err
		}

		if len(chunkConfig) > 0 {
			json.Unmarshal(chunkConfig, &c.ChunkConfig)
		}
		c.CreatedAt = time.Unix(createdAt, 0)
		collections = append(collections, &c)
	}

	// Export documents
	rows, err = b.db.QueryContext(ctx, `
		SELECT id, namespace, collection_id, title, content, content_type, source, metadata, created_at, updated_at
		FROM documents WHERE namespace = ?
		ORDER BY created_at
	`, namespace)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	var documents []*storage.DocumentExport
	for rows.Next() {
		var d types.Document
		var metadata []byte
		var createdAt, updatedAt int64

		err := rows.Scan(&d.ID, &d.Namespace, &d.CollectionID, &d.Title, &d.Content, &d.ContentType, &d.Source, &metadata, &createdAt, &updatedAt)
		if err != nil {
			return nil, nil, nil, err
		}

		if len(metadata) > 0 {
			json.Unmarshal(metadata, &d.Metadata)
		}
		d.CreatedAt = time.Unix(createdAt, 0)
		d.UpdatedAt = time.Unix(updatedAt, 0)
		documents = append(documents, &storage.DocumentExport{Document: &d})
	}

	// Export chunks
	query := `
		SELECT c.id, c.document_id, c.namespace, c.collection_id, c.content, c.chunk_index, c.token_count, c.metadata
	`
	if includeEmbeddings {
		query += `, e.embedding`
	}
	query += ` FROM chunks c`
	if includeEmbeddings {
		query += ` LEFT JOIN chunk_embeddings e ON c.id = e.chunk_id`
	}
	query += ` WHERE c.namespace = ? ORDER BY c.document_id, c.chunk_index`

	rows, err = b.db.QueryContext(ctx, query, namespace)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	var chunks []*storage.ChunkExport
	for rows.Next() {
		var c types.Chunk
		var metadata []byte
		var embedding []byte

		var scanArgs []interface{}
		scanArgs = append(scanArgs, &c.ID, &c.DocumentID, &c.Namespace, &c.CollectionID, &c.Content, &c.Index, &c.TokenCount, &metadata)
		if includeEmbeddings {
			scanArgs = append(scanArgs, &embedding)
		}

		err := rows.Scan(scanArgs...)
		if err != nil {
			return nil, nil, nil, err
		}

		if len(metadata) > 0 {
			json.Unmarshal(metadata, &c.Metadata)
		}

		export := &storage.ChunkExport{Chunk: &c}
		if includeEmbeddings && len(embedding) > 0 {
			export.Embedding = decodeVectorBinary(embedding)
		}
		chunks = append(chunks, export)
	}

	return collections, documents, chunks, nil
}

func (b *Backend) exportContext(ctx context.Context, namespace string) ([]*types.ContextEntry, []*storage.ContextHistoryExport, error) {
	// Export context entries
	rows, err := b.db.QueryContext(ctx, `
		SELECT namespace, run_id, key, value, version, updated_at, updated_by, ttl_expires_at
		FROM context_entries WHERE namespace = ?
	`, namespace)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var entries []*types.ContextEntry
	for rows.Next() {
		var e types.ContextEntry
		var runID string
		var valueBytes []byte
		var updatedAt int64
		var ttlExpiresAt *int64

		err := rows.Scan(&e.Namespace, &runID, &e.Key, &valueBytes, &e.Version, &updatedAt, &e.UpdatedBy, &ttlExpiresAt)
		if err != nil {
			return nil, nil, err
		}

		if runID != "" {
			e.RunID = &runID
		}
		json.Unmarshal(valueBytes, &e.Value)
		e.UpdatedAt = time.Unix(updatedAt, 0)
		if ttlExpiresAt != nil {
			expiresAt := time.Unix(*ttlExpiresAt, 0)
			e.TTLExpiresAt = &expiresAt
		}

		entries = append(entries, &e)
	}

	// Export context history
	rows, err = b.db.QueryContext(ctx, `
		SELECT namespace, run_id, key, value, version, operation, updated_at, updated_by
		FROM context_history WHERE namespace = ?
		ORDER BY updated_at
	`, namespace)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var history []*storage.ContextHistoryExport
	for rows.Next() {
		var h storage.ContextHistoryExport
		h.ContextHistoryEntry = &types.ContextHistoryEntry{}
		var runID *string
		var valueBytes []byte
		var updatedAt int64

		err := rows.Scan(&h.Namespace, &runID, &h.Key, &valueBytes, &h.Version, &h.Operation, &updatedAt, &h.UpdatedBy)
		if err != nil {
			return nil, nil, err
		}

		h.RunID = runID
		json.Unmarshal(valueBytes, &h.Value)
		h.UpdatedAt = time.Unix(updatedAt, 0)

		history = append(history, &h)
	}

	return entries, history, nil
}

func (b *Backend) exportEntities(ctx context.Context, namespace string, includeEmbeddings bool) ([]*types.Entity, []*storage.EntityAliasExport, []*types.EntityMention, []*types.EntityRelationship, error) {
	// Export entities
	query := `
		SELECT e.id, e.namespace, e.name, e.type, e.aliases, e.summary, e.attributes, e.metadata,
		       e.mention_count, e.first_seen_at, e.last_seen_at
	`
	if includeEmbeddings {
		query += `, emb.embedding`
	}
	query += ` FROM entities e`
	if includeEmbeddings {
		query += ` LEFT JOIN entity_embeddings emb ON e.id = emb.entity_id`
	}
	query += ` WHERE e.namespace = ?`

	rows, err := b.db.QueryContext(ctx, query, namespace)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer rows.Close()

	var entities []*types.Entity
	for rows.Next() {
		var e types.Entity
		var aliases, attributes, metadata []byte
		var firstSeen, lastSeen int64
		var embedding []byte

		var scanArgs []interface{}
		scanArgs = append(scanArgs, &e.ID, &e.Namespace, &e.Name, &e.Type, &aliases, &e.Summary, &attributes, &metadata, &e.MentionCount, &firstSeen, &lastSeen)
		if includeEmbeddings {
			scanArgs = append(scanArgs, &embedding)
		}

		err := rows.Scan(scanArgs...)
		if err != nil {
			return nil, nil, nil, nil, err
		}

		if len(aliases) > 0 {
			json.Unmarshal(aliases, &e.Aliases)
		}
		if len(attributes) > 0 {
			json.Unmarshal(attributes, &e.Attributes)
		}
		if len(metadata) > 0 {
			json.Unmarshal(metadata, &e.Metadata)
		}
		e.FirstSeenAt = time.Unix(firstSeen, 0)
		e.LastSeenAt = time.Unix(lastSeen, 0)
		// Note: Entity embeddings are stored separately and not exported with the entity
		// If embeddings are needed, they can be regenerated on import
		_ = embedding

		entities = append(entities, &e)
	}

	// Export entity aliases
	rows, err = b.db.QueryContext(ctx, `
		SELECT namespace, alias, entity_id FROM entity_aliases WHERE namespace = ?
	`, namespace)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer rows.Close()

	var entityAliases []*storage.EntityAliasExport
	for rows.Next() {
		var a storage.EntityAliasExport
		err := rows.Scan(&a.Namespace, &a.Alias, &a.EntityID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		entityAliases = append(entityAliases, &a)
	}

	// Export entity mentions
	rows, err = b.db.QueryContext(ctx, `
		SELECT id, entity_id, namespace, source_type, source_id, context, snippet, created_at
		FROM entity_mentions WHERE namespace = ?
		ORDER BY created_at
	`, namespace)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer rows.Close()

	var mentions []*types.EntityMention
	for rows.Next() {
		var m types.EntityMention
		var createdAt int64

		err := rows.Scan(&m.ID, &m.EntityID, &m.Namespace, &m.SourceType, &m.SourceID, &m.Context, &m.Snippet, &createdAt)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		mentions = append(mentions, &m)
	}

	// Export entity relationships
	rows, err = b.db.QueryContext(ctx, `
		SELECT id, namespace, source_entity_id, target_entity_id, relation_type, description,
		       confidence, mention_count, first_seen_at, last_seen_at
		FROM entity_relationships WHERE namespace = ?
	`, namespace)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer rows.Close()

	var relationships []*types.EntityRelationship
	for rows.Next() {
		var r types.EntityRelationship
		var firstSeen, lastSeen int64

		err := rows.Scan(&r.ID, &r.Namespace, &r.SourceEntityID, &r.TargetEntityID, &r.RelationType, &r.Description, &r.Confidence, &r.MentionCount, &firstSeen, &lastSeen)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		r.FirstSeenAt = time.Unix(firstSeen, 0)
		r.LastSeenAt = time.Unix(lastSeen, 0)
		relationships = append(relationships, &r)
	}

	return entities, entityAliases, mentions, relationships, nil
}
