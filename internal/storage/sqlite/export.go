package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// Export exports all data for a namespace within a single read-only transaction
// for snapshot consistency.
func (b *Backend) Export(ctx context.Context, namespace string, opts storage.ExportOptions) (*storage.ExportData, error) {
	tx, err := b.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("failed to begin export transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	data := &storage.ExportData{
		Version:    1,
		Namespace:  namespace,
		ExportedAt: time.Now().UTC().Unix(),
	}

	if opts.IncludeConversations {
		data.Threads, data.Messages, err = exportConversationsTx(ctx, tx, namespace, opts.IncludeEmbeddings)
		if err != nil {
			return nil, err
		}
	}

	if opts.IncludeKnowledge {
		data.Collections, data.Documents, data.Chunks, err = exportKnowledgeTx(ctx, tx, namespace, opts.IncludeEmbeddings)
		if err != nil {
			return nil, err
		}
	}

	if opts.IncludeContext {
		data.ContextEntries, data.ContextHistory, err = exportContextTx(ctx, tx, namespace)
		if err != nil {
			return nil, err
		}
	}

	if opts.IncludeEntities {
		data.Entities, data.EntityAliases, data.Mentions, data.Relationships, err = exportEntitiesTx(ctx, tx, namespace, opts.IncludeEmbeddings)
		if err != nil {
			return nil, err
		}
	}

	return data, nil
}

func exportConversationsTx(ctx context.Context, tx *sql.Tx, namespace string, includeEmbeddings bool) ([]*storage.ThreadExport, []*storage.MessageExport, error) {
	// Export threads
	rows, err := tx.QueryContext(ctx, `
		SELECT id, namespace, title, summary, metadata, created_at, updated_at
		FROM threads WHERE namespace = ?
		ORDER BY created_at
	`, namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query threads: %w", err)
	}

	var threads []*storage.ThreadExport
	for rows.Next() {
		var t types.Thread
		var title, summary sql.NullString
		var metadata []byte
		var createdAt, updatedAt int64

		if err := rows.Scan(&t.ID, &t.Namespace, &title, &summary, &metadata, &createdAt, &updatedAt); err != nil {
			_ = rows.Close()
			return nil, nil, fmt.Errorf("failed to scan thread: %w", err)
		}

		t.Title = title.String
		t.Summary = summary.String
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &t.Metadata); err != nil {
				_ = rows.Close()
				return nil, nil, fmt.Errorf("failed to unmarshal thread metadata for %s: %w", t.ID, err)
			}
		}
		t.CreatedAt = time.Unix(createdAt, 0).UTC()
		t.UpdatedAt = time.Unix(updatedAt, 0).UTC()

		threads = append(threads, &storage.ThreadExport{Thread: &t})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, fmt.Errorf("failed to iterate threads: %w", err)
	}
	_ = rows.Close()

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

	rows, err = tx.QueryContext(ctx, query, namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var messages []*storage.MessageExport
	for rows.Next() {
		var m types.Message
		var metadata []byte
		var summarized sql.NullInt64
		var createdAt int64
		var embedding []byte

		scanArgs := []any{&m.ID, &m.Namespace, &m.ThreadID, &m.Role, &m.Content, &metadata, &summarized, &createdAt}
		if includeEmbeddings {
			scanArgs = append(scanArgs, &embedding)
		}

		if err := rows.Scan(scanArgs...); err != nil {
			return nil, nil, fmt.Errorf("failed to scan message: %w", err)
		}

		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &m.Metadata); err != nil {
				return nil, nil, fmt.Errorf("failed to unmarshal message metadata for %s: %w", m.ID, err)
			}
		}
		m.Summarized = summarized.Valid && summarized.Int64 != 0
		m.CreatedAt = time.Unix(createdAt, 0).UTC()

		export := &storage.MessageExport{Message: &m}
		if includeEmbeddings && len(embedding) > 0 {
			export.Embedding = decodeVectorBinary(embedding)
		}
		messages = append(messages, export)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed to iterate messages: %w", err)
	}

	return threads, messages, nil
}

func exportKnowledgeTx(ctx context.Context, tx *sql.Tx, namespace string, includeEmbeddings bool) ([]*types.Collection, []*storage.DocumentExport, []*storage.ChunkExport, error) {
	// Export collections
	rows, err := tx.QueryContext(ctx, `
		SELECT id, namespace, name, description, chunk_config, created_at
		FROM collections WHERE namespace = ?
		ORDER BY created_at
	`, namespace)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to query collections: %w", err)
	}

	var collections []*types.Collection
	for rows.Next() {
		var c types.Collection
		var description sql.NullString
		var chunkConfig []byte
		var createdAt int64

		if err := rows.Scan(&c.ID, &c.Namespace, &c.Name, &description, &chunkConfig, &createdAt); err != nil {
			_ = rows.Close()
			return nil, nil, nil, fmt.Errorf("failed to scan collection: %w", err)
		}

		c.Description = description.String
		if len(chunkConfig) > 0 {
			if err := json.Unmarshal(chunkConfig, &c.ChunkConfig); err != nil {
				_ = rows.Close()
				return nil, nil, nil, fmt.Errorf("failed to unmarshal chunk_config for collection %s: %w", c.ID, err)
			}
		}
		c.CreatedAt = time.Unix(createdAt, 0).UTC()
		collections = append(collections, &c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, nil, fmt.Errorf("failed to iterate collections: %w", err)
	}
	_ = rows.Close()

	// Export documents
	rows, err = tx.QueryContext(ctx, `
		SELECT id, namespace, collection_id, title, content, content_type, source, metadata, created_at, updated_at
		FROM documents WHERE namespace = ?
		ORDER BY created_at
	`, namespace)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to query documents: %w", err)
	}

	var documents []*storage.DocumentExport
	for rows.Next() {
		var d types.Document
		var title, contentType, source sql.NullString
		var metadata []byte
		var createdAt, updatedAt int64

		if err := rows.Scan(&d.ID, &d.Namespace, &d.CollectionID, &title, &d.Content, &contentType, &source, &metadata, &createdAt, &updatedAt); err != nil {
			_ = rows.Close()
			return nil, nil, nil, fmt.Errorf("failed to scan document: %w", err)
		}

		d.Title = title.String
		d.ContentType = contentType.String
		d.Source = source.String
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &d.Metadata); err != nil {
				_ = rows.Close()
				return nil, nil, nil, fmt.Errorf("failed to unmarshal document metadata for %s: %w", d.ID, err)
			}
		}
		d.CreatedAt = time.Unix(createdAt, 0).UTC()
		d.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		documents = append(documents, &storage.DocumentExport{Document: &d})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, nil, fmt.Errorf("failed to iterate documents: %w", err)
	}
	_ = rows.Close()

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

	rows, err = tx.QueryContext(ctx, query, namespace)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to query chunks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var chunks []*storage.ChunkExport
	for rows.Next() {
		var c types.Chunk
		var tokenCount sql.NullInt64
		var metadata []byte
		var embedding []byte

		scanArgs := []any{&c.ID, &c.DocumentID, &c.Namespace, &c.CollectionID, &c.Content, &c.Index, &tokenCount, &metadata}
		if includeEmbeddings {
			scanArgs = append(scanArgs, &embedding)
		}

		if err := rows.Scan(scanArgs...); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to scan chunk: %w", err)
		}

		if tokenCount.Valid {
			c.TokenCount = int(tokenCount.Int64)
		}
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &c.Metadata); err != nil {
				return nil, nil, nil, fmt.Errorf("failed to unmarshal chunk metadata for %s: %w", c.ID, err)
			}
		}

		export := &storage.ChunkExport{Chunk: &c}
		if includeEmbeddings && len(embedding) > 0 {
			export.Embedding = decodeVectorBinary(embedding)
		}
		chunks = append(chunks, export)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to iterate chunks: %w", err)
	}

	return collections, documents, chunks, nil
}

func exportContextTx(ctx context.Context, tx *sql.Tx, namespace string) ([]*types.ContextEntry, []*storage.ContextHistoryExport, error) {
	// Export context entries
	rows, err := tx.QueryContext(ctx, `
		SELECT namespace, run_id, key, value, version, updated_at, updated_by, ttl_expires_at
		FROM context_entries WHERE namespace = ?
	`, namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query context entries: %w", err)
	}

	var entries []*types.ContextEntry
	for rows.Next() {
		var e types.ContextEntry
		var runID, updatedBy sql.NullString
		var valueBytes []byte
		var updatedAt int64
		var ttlExpiresAt sql.NullInt64

		if err := rows.Scan(&e.Namespace, &runID, &e.Key, &valueBytes, &e.Version, &updatedAt, &updatedBy, &ttlExpiresAt); err != nil {
			_ = rows.Close()
			return nil, nil, fmt.Errorf("failed to scan context entry: %w", err)
		}

		if runID.Valid && runID.String != "" {
			r := runID.String
			e.RunID = &r
		}
		if err := json.Unmarshal(valueBytes, &e.Value); err != nil {
			_ = rows.Close()
			return nil, nil, fmt.Errorf("failed to unmarshal context value for key %q: %w", e.Key, err)
		}
		e.UpdatedBy = updatedBy.String
		e.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		if ttlExpiresAt.Valid {
			expiresAt := time.Unix(ttlExpiresAt.Int64, 0).UTC()
			e.TTLExpiresAt = &expiresAt
		}

		entries = append(entries, &e)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, fmt.Errorf("failed to iterate context entries: %w", err)
	}
	_ = rows.Close()

	// Export context history
	rows, err = tx.QueryContext(ctx, `
		SELECT namespace, run_id, key, value, version, operation, updated_at, updated_by
		FROM context_history WHERE namespace = ?
		ORDER BY updated_at
	`, namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query context history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var history []*storage.ContextHistoryExport
	for rows.Next() {
		var h storage.ContextHistoryExport
		h.ContextHistoryEntry = &types.ContextHistoryEntry{}
		var runID, updatedBy sql.NullString
		var valueBytes []byte
		var updatedAt int64

		if err := rows.Scan(&h.Namespace, &runID, &h.Key, &valueBytes, &h.Version, &h.Operation, &updatedAt, &updatedBy); err != nil {
			return nil, nil, fmt.Errorf("failed to scan context history: %w", err)
		}

		if runID.Valid && runID.String != "" {
			r := runID.String
			h.RunID = &r
		}
		if err := json.Unmarshal(valueBytes, &h.Value); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal context history value for key %q: %w", h.Key, err)
		}
		h.UpdatedBy = updatedBy.String
		h.UpdatedAt = time.Unix(updatedAt, 0).UTC()

		history = append(history, &h)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed to iterate context history: %w", err)
	}

	return entries, history, nil
}

func exportEntitiesTx(ctx context.Context, tx *sql.Tx, namespace string, includeEmbeddings bool) ([]*storage.EntityExport, []*storage.EntityAliasExport, []*types.EntityMention, []*types.EntityRelationship, error) {
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

	rows, err := tx.QueryContext(ctx, query, namespace)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to query entities: %w", err)
	}

	var entities []*storage.EntityExport
	for rows.Next() {
		var e types.Entity
		var aliases, attributes, metadata []byte
		var summary sql.NullString
		var mentionCount sql.NullInt64
		var firstSeen, lastSeen int64
		var embedding []byte

		scanArgs := []any{&e.ID, &e.Namespace, &e.Name, &e.Type, &aliases, &summary, &attributes, &metadata, &mentionCount, &firstSeen, &lastSeen}
		if includeEmbeddings {
			scanArgs = append(scanArgs, &embedding)
		}

		if err := rows.Scan(scanArgs...); err != nil {
			_ = rows.Close()
			return nil, nil, nil, nil, fmt.Errorf("failed to scan entity: %w", err)
		}

		e.Summary = summary.String
		if mentionCount.Valid {
			e.MentionCount = mentionCount.Int64
		}
		if len(aliases) > 0 {
			if err := json.Unmarshal(aliases, &e.Aliases); err != nil {
				_ = rows.Close()
				return nil, nil, nil, nil, fmt.Errorf("failed to unmarshal entity aliases for %s: %w", e.ID, err)
			}
		}
		if len(attributes) > 0 {
			if err := json.Unmarshal(attributes, &e.Attributes); err != nil {
				_ = rows.Close()
				return nil, nil, nil, nil, fmt.Errorf("failed to unmarshal entity attributes for %s: %w", e.ID, err)
			}
		}
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &e.Metadata); err != nil {
				_ = rows.Close()
				return nil, nil, nil, nil, fmt.Errorf("failed to unmarshal entity metadata for %s: %w", e.ID, err)
			}
		}
		e.FirstSeenAt = time.Unix(firstSeen, 0).UTC()
		e.LastSeenAt = time.Unix(lastSeen, 0).UTC()

		export := &storage.EntityExport{Entity: &e}
		if includeEmbeddings && len(embedding) > 0 {
			export.Embedding = decodeVectorBinary(embedding)
		}
		entities = append(entities, export)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, nil, nil, fmt.Errorf("failed to iterate entities: %w", err)
	}
	_ = rows.Close()

	// Export entity aliases
	rows, err = tx.QueryContext(ctx, `
		SELECT namespace, alias, entity_id FROM entity_aliases WHERE namespace = ?
	`, namespace)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to query entity aliases: %w", err)
	}

	var entityAliases []*storage.EntityAliasExport
	for rows.Next() {
		var a storage.EntityAliasExport
		if err := rows.Scan(&a.Namespace, &a.Alias, &a.EntityID); err != nil {
			_ = rows.Close()
			return nil, nil, nil, nil, fmt.Errorf("failed to scan entity alias: %w", err)
		}
		entityAliases = append(entityAliases, &a)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, nil, nil, fmt.Errorf("failed to iterate entity aliases: %w", err)
	}
	_ = rows.Close()

	// Export entity mentions
	rows, err = tx.QueryContext(ctx, `
		SELECT id, entity_id, namespace, source_type, source_id, context, snippet, created_at
		FROM entity_mentions WHERE namespace = ?
		ORDER BY created_at
	`, namespace)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to query entity mentions: %w", err)
	}

	var mentions []*types.EntityMention
	for rows.Next() {
		var m types.EntityMention
		var mentionContext, snippet sql.NullString
		var createdAt int64

		if err := rows.Scan(&m.ID, &m.EntityID, &m.Namespace, &m.SourceType, &m.SourceID, &mentionContext, &snippet, &createdAt); err != nil {
			_ = rows.Close()
			return nil, nil, nil, nil, fmt.Errorf("failed to scan entity mention: %w", err)
		}
		m.Context = mentionContext.String
		m.Snippet = snippet.String
		m.CreatedAt = time.Unix(createdAt, 0).UTC()
		mentions = append(mentions, &m)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, nil, nil, fmt.Errorf("failed to iterate entity mentions: %w", err)
	}
	_ = rows.Close()

	// Export entity relationships
	rows, err = tx.QueryContext(ctx, `
		SELECT id, namespace, source_entity_id, target_entity_id, relation_type, description,
		       confidence, mention_count, first_seen_at, last_seen_at
		FROM entity_relationships WHERE namespace = ?
	`, namespace)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to query entity relationships: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var relationships []*types.EntityRelationship
	for rows.Next() {
		var r types.EntityRelationship
		var description sql.NullString
		var confidence sql.NullFloat64
		var mentionCount sql.NullInt64
		var firstSeen, lastSeen int64

		if err := rows.Scan(&r.ID, &r.Namespace, &r.SourceEntityID, &r.TargetEntityID, &r.RelationType, &description, &confidence, &mentionCount, &firstSeen, &lastSeen); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("failed to scan entity relationship: %w", err)
		}
		r.Description = description.String
		if confidence.Valid {
			r.Confidence = confidence.Float64
		}
		if mentionCount.Valid {
			r.MentionCount = mentionCount.Int64
		}
		r.FirstSeenAt = time.Unix(firstSeen, 0).UTC()
		r.LastSeenAt = time.Unix(lastSeen, 0).UTC()
		relationships = append(relationships, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to iterate entity relationships: %w", err)
	}

	return entities, entityAliases, mentions, relationships, nil
}
