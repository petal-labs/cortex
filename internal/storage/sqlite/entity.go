package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// UpsertEntity creates or updates an entity.
func (b *Backend) UpsertEntity(ctx context.Context, entity *types.Entity) error {
	if entity.ID == "" {
		entity.ID = uuid.New().String()
	}

	now := time.Now().UTC()
	if entity.FirstSeenAt.IsZero() {
		entity.FirstSeenAt = now
	}
	if entity.LastSeenAt.IsZero() {
		entity.LastSeenAt = now
	}

	// Marshal JSON fields
	aliasesJSON, err := json.Marshal(entity.Aliases)
	if err != nil {
		return fmt.Errorf("failed to marshal aliases: %w", err)
	}

	var attributesJSON, metadataJSON []byte
	if entity.Attributes != nil {
		attributesJSON, err = json.Marshal(entity.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}
	}
	if entity.Metadata != nil {
		metadataJSON, err = json.Marshal(entity.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	return b.withTx(ctx, func(tx *sql.Tx) error {
		// Check if entity exists
		var existingID string
		err := tx.QueryRowContext(ctx,
			"SELECT id FROM entities WHERE id = ?", entity.ID,
		).Scan(&existingID)

		if err == sql.ErrNoRows {
			// Insert new entity
			_, err = tx.ExecContext(ctx, `
				INSERT INTO entities (id, namespace, name, type, aliases, summary, attributes, metadata, mention_count, first_seen_at, last_seen_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, entity.ID, entity.Namespace, entity.Name, string(entity.Type), aliasesJSON,
				entity.Summary, attributesJSON, metadataJSON, entity.MentionCount,
				entity.FirstSeenAt.Unix(), entity.LastSeenAt.Unix())
			if err != nil {
				return fmt.Errorf("failed to insert entity: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to check entity existence: %w", err)
		} else {
			// Update existing entity
			_, err = tx.ExecContext(ctx, `
				UPDATE entities
				SET name = ?, type = ?, aliases = ?, summary = ?, attributes = ?, metadata = ?,
				    mention_count = ?, last_seen_at = ?
				WHERE id = ?
			`, entity.Name, string(entity.Type), aliasesJSON, entity.Summary,
				attributesJSON, metadataJSON, entity.MentionCount,
				entity.LastSeenAt.Unix(), entity.ID)
			if err != nil {
				return fmt.Errorf("failed to update entity: %w", err)
			}
		}

		// Update aliases in lookup table
		// First, delete existing aliases for this entity
		_, err = tx.ExecContext(ctx,
			"DELETE FROM entity_aliases WHERE entity_id = ?", entity.ID,
		)
		if err != nil {
			return fmt.Errorf("failed to clear aliases: %w", err)
		}

		// Insert new aliases (including lowercase canonical name)
		aliases := append([]string{strings.ToLower(entity.Name)}, entity.Aliases...)
		for _, alias := range aliases {
			lowerAlias := strings.ToLower(alias)
			_, err = tx.ExecContext(ctx, `
				INSERT OR IGNORE INTO entity_aliases (namespace, alias, entity_id)
				VALUES (?, ?, ?)
			`, entity.Namespace, lowerAlias, entity.ID)
			if err != nil {
				return fmt.Errorf("failed to insert alias: %w", err)
			}
		}

		return nil
	})
}

// GetEntityByID retrieves an entity by its ID.
func (b *Backend) GetEntityByID(ctx context.Context, namespace, entityID string) (*types.Entity, error) {
	return b.getEntity(ctx, "id = ? AND namespace = ?", entityID, namespace)
}

// GetEntityByName retrieves an entity by its canonical name (case-insensitive).
func (b *Backend) GetEntityByName(ctx context.Context, namespace, name string) (*types.Entity, error) {
	return b.getEntity(ctx, "namespace = ? AND LOWER(name) = LOWER(?)", namespace, name)
}

// getEntity is a helper for retrieving entities by different criteria.
func (b *Backend) getEntity(ctx context.Context, where string, args ...any) (*types.Entity, error) {
	entity := &types.Entity{}
	var aliasesJSON, attributesJSON, metadataJSON, summary sql.NullString
	var firstSeenAtUnix, lastSeenAtUnix int64
	var entityType string

	err := b.db.QueryRowContext(ctx, `
		SELECT id, namespace, name, type, aliases, summary, attributes, metadata,
		       mention_count, first_seen_at, last_seen_at
		FROM entities
		WHERE `+where, args...).Scan(
		&entity.ID, &entity.Namespace, &entity.Name, &entityType,
		&aliasesJSON, &summary, &attributesJSON, &metadataJSON,
		&entity.MentionCount, &firstSeenAtUnix, &lastSeenAtUnix,
	)

	if err == sql.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query entity: %w", err)
	}

	entity.Type = types.EntityType(entityType)
	entity.Summary = summary.String
	entity.FirstSeenAt = time.Unix(firstSeenAtUnix, 0).UTC()
	entity.LastSeenAt = time.Unix(lastSeenAtUnix, 0).UTC()

	// Parse JSON fields
	if aliasesJSON.Valid && aliasesJSON.String != "" {
		if err := json.Unmarshal([]byte(aliasesJSON.String), &entity.Aliases); err != nil {
			return nil, fmt.Errorf("failed to unmarshal aliases: %w", err)
		}
	}
	if attributesJSON.Valid && attributesJSON.String != "" {
		if err := json.Unmarshal([]byte(attributesJSON.String), &entity.Attributes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
		}
	}
	if metadataJSON.Valid && metadataJSON.String != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &entity.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	return entity, nil
}

// ResolveAlias looks up an entity by an alias.
func (b *Backend) ResolveAlias(ctx context.Context, namespace, alias string) (*types.Entity, error) {
	var entityID string
	err := b.db.QueryRowContext(ctx, `
		SELECT entity_id FROM entity_aliases
		WHERE namespace = ? AND alias = ?
	`, namespace, strings.ToLower(alias)).Scan(&entityID)

	if err == sql.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to resolve alias: %w", err)
	}

	return b.GetEntityByID(ctx, namespace, entityID)
}

// SearchEntities performs semantic search across entity summaries.
// Uses sqlite-vec's vec_distance_cosine for brute-force KNN search.
func (b *Backend) SearchEntities(ctx context.Context, namespace string, embedding []float32, opts storage.EntitySearchOpts) ([]*types.EntityResult, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	// Encode query embedding as binary for sqlite-vec
	queryEmbedding := encodeVectorBinary(embedding)

	// Build the query
	var args []any
	query := `
		SELECT e.id, e.namespace, e.name, e.type, e.aliases, e.summary,
		       e.attributes, e.metadata, e.mention_count, e.first_seen_at, e.last_seen_at,
		       vec_distance_cosine(emb.embedding, ?) AS distance
		FROM entity_embeddings emb
		JOIN entities e ON e.id = emb.entity_id
		WHERE e.namespace = ?
	`
	args = append(args, queryEmbedding, namespace)

	// Optional: filter by entity type
	if opts.EntityType != nil {
		query += " AND e.type = ?"
		args = append(args, string(*opts.EntityType))
	}

	query += " ORDER BY distance ASC LIMIT ?"
	args = append(args, opts.TopK)

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search entities: %w", err)
	}
	defer rows.Close()

	var results []*types.EntityResult
	for rows.Next() {
		entity := &types.Entity{}
		var aliasesJSON, attributesJSON, metadataJSON, summary sql.NullString
		var firstSeenAtUnix, lastSeenAtUnix int64
		var entityType string
		var distance float64

		if err := rows.Scan(
			&entity.ID, &entity.Namespace, &entity.Name, &entityType,
			&aliasesJSON, &summary, &attributesJSON, &metadataJSON,
			&entity.MentionCount, &firstSeenAtUnix, &lastSeenAtUnix, &distance,
		); err != nil {
			return nil, fmt.Errorf("failed to scan entity: %w", err)
		}

		entity.Type = types.EntityType(entityType)
		entity.Summary = summary.String
		entity.FirstSeenAt = time.Unix(firstSeenAtUnix, 0).UTC()
		entity.LastSeenAt = time.Unix(lastSeenAtUnix, 0).UTC()

		if aliasesJSON.Valid && aliasesJSON.String != "" {
			if err := json.Unmarshal([]byte(aliasesJSON.String), &entity.Aliases); err != nil {
				return nil, fmt.Errorf("failed to unmarshal aliases: %w", err)
			}
		}
		if attributesJSON.Valid && attributesJSON.String != "" {
			if err := json.Unmarshal([]byte(attributesJSON.String), &entity.Attributes); err != nil {
				return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
			}
		}
		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &entity.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		// Convert distance to similarity score (cosine distance -> similarity)
		// Cosine distance ranges from 0 (identical) to 2 (opposite)
		// We convert to similarity: 1 - (distance / 2) gives range [0, 1]
		score := 1.0 - (distance / 2.0)

		// Apply minimum score filter
		if opts.MinScore > 0 && score < opts.MinScore {
			continue
		}

		results = append(results, &types.EntityResult{
			Entity: entity,
			Score:  score,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate entities: %w", err)
	}

	return results, nil
}

// ListEntities returns entities in a namespace with optional filtering.
func (b *Backend) ListEntities(ctx context.Context, namespace string, opts storage.EntityListOpts) ([]*types.Entity, string, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	var args []any
	query := `
		SELECT id, namespace, name, type, aliases, summary, attributes, metadata,
		       mention_count, first_seen_at, last_seen_at
		FROM entities
		WHERE namespace = ?
	`
	args = append(args, namespace)

	// Filter by entity type
	if opts.EntityType != nil {
		query += " AND type = ?"
		args = append(args, string(*opts.EntityType))
	}

	// Handle cursor-based pagination
	sortBy := opts.SortBy
	if sortBy == "" {
		sortBy = types.EntitySortByName
	}

	switch sortBy {
	case types.EntitySortByMentionCount:
		if opts.Cursor != "" {
			query += " AND mention_count < ?"
			args = append(args, opts.Cursor)
		}
		query += " ORDER BY mention_count DESC, id"
	case types.EntitySortByLastSeen:
		if opts.Cursor != "" {
			query += " AND last_seen_at < ?"
			args = append(args, opts.Cursor)
		}
		query += " ORDER BY last_seen_at DESC, id"
	default: // EntitySortByName
		if opts.Cursor != "" {
			query += " AND name > ?"
			args = append(args, opts.Cursor)
		}
		query += " ORDER BY name, id"
	}

	query += " LIMIT ?"
	args = append(args, opts.Limit+1)

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query entities: %w", err)
	}
	defer rows.Close()

	var entities []*types.Entity
	for rows.Next() {
		entity := &types.Entity{}
		var aliasesJSON, attributesJSON, metadataJSON, summary sql.NullString
		var firstSeenAtUnix, lastSeenAtUnix int64
		var entityType string

		if err := rows.Scan(
			&entity.ID, &entity.Namespace, &entity.Name, &entityType,
			&aliasesJSON, &summary, &attributesJSON, &metadataJSON,
			&entity.MentionCount, &firstSeenAtUnix, &lastSeenAtUnix,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan entity: %w", err)
		}

		entity.Type = types.EntityType(entityType)
		entity.Summary = summary.String
		entity.FirstSeenAt = time.Unix(firstSeenAtUnix, 0).UTC()
		entity.LastSeenAt = time.Unix(lastSeenAtUnix, 0).UTC()

		if aliasesJSON.Valid && aliasesJSON.String != "" {
			if err := json.Unmarshal([]byte(aliasesJSON.String), &entity.Aliases); err != nil {
				return nil, "", fmt.Errorf("failed to unmarshal aliases: %w", err)
			}
		}
		if attributesJSON.Valid && attributesJSON.String != "" {
			if err := json.Unmarshal([]byte(attributesJSON.String), &entity.Attributes); err != nil {
				return nil, "", fmt.Errorf("failed to unmarshal attributes: %w", err)
			}
		}
		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &entity.Metadata); err != nil {
				return nil, "", fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		entities = append(entities, entity)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate entities: %w", err)
	}

	// Determine next cursor
	var nextCursor string
	if len(entities) > opts.Limit {
		entities = entities[:opts.Limit]
		if len(entities) > 0 {
			last := entities[len(entities)-1]
			switch sortBy {
			case types.EntitySortByMentionCount:
				nextCursor = fmt.Sprintf("%d", last.MentionCount)
			case types.EntitySortByLastSeen:
				nextCursor = fmt.Sprintf("%d", last.LastSeenAt.Unix())
			default:
				nextCursor = last.Name
			}
		}
	}

	return entities, nextCursor, nil
}

// DeleteEntity removes an entity and all its mentions/relationships.
func (b *Backend) DeleteEntity(ctx context.Context, namespace, entityID string) error {
	result, err := b.db.ExecContext(ctx,
		"DELETE FROM entities WHERE id = ? AND namespace = ?",
		entityID, namespace,
	)
	if err != nil {
		return fmt.Errorf("failed to delete entity: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return storage.ErrNotFound
	}

	return nil
}

// MergeEntities combines two entities, moving all data from source to target.
func (b *Backend) MergeEntities(ctx context.Context, namespace, sourceID, targetID string) error {
	return b.withTx(ctx, func(tx *sql.Tx) error {
		// Verify both entities exist
		var sourceExists, targetExists bool
		err := tx.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM entities WHERE id = ? AND namespace = ?)",
			sourceID, namespace,
		).Scan(&sourceExists)
		if err != nil {
			return fmt.Errorf("failed to check source entity: %w", err)
		}
		if !sourceExists {
			return storage.ErrNotFound
		}

		err = tx.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM entities WHERE id = ? AND namespace = ?)",
			targetID, namespace,
		).Scan(&targetExists)
		if err != nil {
			return fmt.Errorf("failed to check target entity: %w", err)
		}
		if !targetExists {
			return storage.ErrNotFound
		}

		// Move mentions from source to target
		_, err = tx.ExecContext(ctx,
			"UPDATE entity_mentions SET entity_id = ? WHERE entity_id = ?",
			targetID, sourceID,
		)
		if err != nil {
			return fmt.Errorf("failed to move mentions: %w", err)
		}

		// Move outgoing relationships (update source_entity_id)
		_, err = tx.ExecContext(ctx, `
			UPDATE entity_relationships
			SET source_entity_id = ?
			WHERE source_entity_id = ? AND target_entity_id != ?
		`, targetID, sourceID, targetID)
		if err != nil {
			return fmt.Errorf("failed to move outgoing relationships: %w", err)
		}

		// Move incoming relationships (update target_entity_id)
		_, err = tx.ExecContext(ctx, `
			UPDATE entity_relationships
			SET target_entity_id = ?
			WHERE target_entity_id = ? AND source_entity_id != ?
		`, targetID, sourceID, targetID)
		if err != nil {
			return fmt.Errorf("failed to move incoming relationships: %w", err)
		}

		// Delete self-referencing relationships that might have been created
		_, err = tx.ExecContext(ctx,
			"DELETE FROM entity_relationships WHERE source_entity_id = target_entity_id",
		)
		if err != nil {
			return fmt.Errorf("failed to clean self-references: %w", err)
		}

		// Update target's mention count
		_, err = tx.ExecContext(ctx, `
			UPDATE entities
			SET mention_count = (SELECT COUNT(*) FROM entity_mentions WHERE entity_id = ?)
			WHERE id = ?
		`, targetID, targetID)
		if err != nil {
			return fmt.Errorf("failed to update mention count: %w", err)
		}

		// Move aliases from source to target
		_, err = tx.ExecContext(ctx,
			"UPDATE OR IGNORE entity_aliases SET entity_id = ? WHERE entity_id = ?",
			targetID, sourceID,
		)
		if err != nil {
			return fmt.Errorf("failed to move aliases: %w", err)
		}

		// Delete source entity (cascades to remaining aliases)
		_, err = tx.ExecContext(ctx,
			"DELETE FROM entities WHERE id = ?", sourceID,
		)
		if err != nil {
			return fmt.Errorf("failed to delete source entity: %w", err)
		}

		return nil
	})
}

// InsertMention records a mention of an entity in source content.
func (b *Backend) InsertMention(ctx context.Context, mention *types.EntityMention) error {
	if mention.ID == "" {
		mention.ID = uuid.New().String()
	}
	if mention.CreatedAt.IsZero() {
		mention.CreatedAt = time.Now().UTC()
	}

	return b.withTx(ctx, func(tx *sql.Tx) error {
		// Insert mention
		_, err := tx.ExecContext(ctx, `
			INSERT INTO entity_mentions (id, entity_id, namespace, source_type, source_id, context, snippet, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, mention.ID, mention.EntityID, mention.Namespace, mention.SourceType,
			mention.SourceID, mention.Context, mention.Snippet, mention.CreatedAt.Unix())
		if err != nil {
			return fmt.Errorf("failed to insert mention: %w", err)
		}

		// Update entity mention count and last_seen_at
		_, err = tx.ExecContext(ctx, `
			UPDATE entities
			SET mention_count = mention_count + 1, last_seen_at = ?
			WHERE id = ?
		`, mention.CreatedAt.Unix(), mention.EntityID)
		if err != nil {
			return fmt.Errorf("failed to update entity mention count: %w", err)
		}

		return nil
	})
}

// GetMentions retrieves recent mentions of an entity.
func (b *Backend) GetMentions(ctx context.Context, entityID string, limit int) ([]*types.EntityMention, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := b.db.QueryContext(ctx, `
		SELECT id, entity_id, namespace, source_type, source_id, context, snippet, created_at
		FROM entity_mentions
		WHERE entity_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, entityID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query mentions: %w", err)
	}
	defer rows.Close()

	var mentions []*types.EntityMention
	for rows.Next() {
		mention := &types.EntityMention{}
		var context, snippet sql.NullString
		var createdAtUnix int64

		if err := rows.Scan(
			&mention.ID, &mention.EntityID, &mention.Namespace,
			&mention.SourceType, &mention.SourceID,
			&context, &snippet, &createdAtUnix,
		); err != nil {
			return nil, fmt.Errorf("failed to scan mention: %w", err)
		}

		mention.Context = context.String
		mention.Snippet = snippet.String
		mention.CreatedAt = time.Unix(createdAtUnix, 0).UTC()

		mentions = append(mentions, mention)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate mentions: %w", err)
	}

	return mentions, nil
}

// UpsertRelationship creates or updates a relationship between entities.
func (b *Backend) UpsertRelationship(ctx context.Context, rel *types.EntityRelationship) error {
	if rel.ID == "" {
		rel.ID = uuid.New().String()
	}

	now := time.Now().UTC()
	if rel.FirstSeenAt.IsZero() {
		rel.FirstSeenAt = now
	}
	if rel.LastSeenAt.IsZero() {
		rel.LastSeenAt = now
	}
	if rel.Confidence == 0 {
		rel.Confidence = 1.0
	}

	// Try to update existing relationship
	result, err := b.db.ExecContext(ctx, `
		UPDATE entity_relationships
		SET description = ?, confidence = ?, mention_count = mention_count + 1, last_seen_at = ?
		WHERE namespace = ? AND source_entity_id = ? AND target_entity_id = ? AND relation_type = ?
	`, rel.Description, rel.Confidence, rel.LastSeenAt.Unix(),
		rel.Namespace, rel.SourceEntityID, rel.TargetEntityID, rel.RelationType)

	if err != nil {
		return fmt.Errorf("failed to update relationship: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		// Insert new relationship
		_, err = b.db.ExecContext(ctx, `
			INSERT INTO entity_relationships
				(id, namespace, source_entity_id, target_entity_id, relation_type, description,
				 confidence, mention_count, first_seen_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, rel.ID, rel.Namespace, rel.SourceEntityID, rel.TargetEntityID, rel.RelationType,
			rel.Description, rel.Confidence, rel.MentionCount,
			rel.FirstSeenAt.Unix(), rel.LastSeenAt.Unix())
		if err != nil {
			return fmt.Errorf("failed to insert relationship: %w", err)
		}
	}

	return nil
}

// GetRelationships retrieves relationships for an entity.
func (b *Backend) GetRelationships(ctx context.Context, namespace, entityID string, opts storage.RelationshipOpts) ([]*types.EntityRelationship, error) {
	var args []any
	var conditions []string

	conditions = append(conditions, "namespace = ?")
	args = append(args, namespace)

	// Build direction condition
	switch opts.Direction {
	case types.RelationshipDirectionOutgoing:
		conditions = append(conditions, "source_entity_id = ?")
		args = append(args, entityID)
	case types.RelationshipDirectionIncoming:
		conditions = append(conditions, "target_entity_id = ?")
		args = append(args, entityID)
	default: // Both
		conditions = append(conditions, "(source_entity_id = ? OR target_entity_id = ?)")
		args = append(args, entityID, entityID)
	}

	// Filter by relation type if specified
	if opts.RelationType != nil {
		conditions = append(conditions, "relation_type = ?")
		args = append(args, *opts.RelationType)
	}

	query := `
		SELECT id, namespace, source_entity_id, target_entity_id, relation_type,
		       description, confidence, mention_count, first_seen_at, last_seen_at
		FROM entity_relationships
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY mention_count DESC, last_seen_at DESC
	`

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query relationships: %w", err)
	}
	defer rows.Close()

	var relationships []*types.EntityRelationship
	for rows.Next() {
		rel := &types.EntityRelationship{}
		var description sql.NullString
		var firstSeenAtUnix, lastSeenAtUnix int64

		if err := rows.Scan(
			&rel.ID, &rel.Namespace, &rel.SourceEntityID, &rel.TargetEntityID,
			&rel.RelationType, &description, &rel.Confidence, &rel.MentionCount,
			&firstSeenAtUnix, &lastSeenAtUnix,
		); err != nil {
			return nil, fmt.Errorf("failed to scan relationship: %w", err)
		}

		rel.Description = description.String
		rel.FirstSeenAt = time.Unix(firstSeenAtUnix, 0).UTC()
		rel.LastSeenAt = time.Unix(lastSeenAtUnix, 0).UTC()

		relationships = append(relationships, rel)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate relationships: %w", err)
	}

	return relationships, nil
}

// RegisterAlias adds an alias for an entity.
func (b *Backend) RegisterAlias(ctx context.Context, namespace, alias, entityID string) error {
	// Verify entity exists
	var exists bool
	err := b.db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM entities WHERE id = ? AND namespace = ?)",
		entityID, namespace,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check entity existence: %w", err)
	}
	if !exists {
		return storage.ErrNotFound
	}

	// Insert alias
	_, err = b.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO entity_aliases (namespace, alias, entity_id)
		VALUES (?, ?, ?)
	`, namespace, strings.ToLower(alias), entityID)

	if err != nil {
		return fmt.Errorf("failed to register alias: %w", err)
	}

	return nil
}

// StoreEntityEmbedding stores the embedding for an entity's summary.
// Embeddings are stored in binary format for use with sqlite-vec.
func (b *Backend) StoreEntityEmbedding(ctx context.Context, entityID string, embedding []float32) error {
	// Encode embedding as binary for sqlite-vec compatibility
	embeddingBytes := encodeVectorBinary(embedding)

	_, err := b.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO entity_embeddings (entity_id, embedding)
		VALUES (?, ?)
	`, entityID, embeddingBytes)

	if err != nil {
		return fmt.Errorf("failed to store entity embedding: %w", err)
	}

	return nil
}

// EnqueueExtraction adds an item to the entity extraction queue.
func (b *Backend) EnqueueExtraction(ctx context.Context, item *types.ExtractionQueueItem) error {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if item.Status == "" {
		item.Status = "pending"
	}

	result, err := b.db.ExecContext(ctx, `
		INSERT INTO entity_extraction_queue (namespace, source_type, source_id, content, status, attempts, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, item.Namespace, item.SourceType, item.SourceID, item.Content,
		item.Status, item.Attempts, item.CreatedAt.Unix())

	if err != nil {
		return fmt.Errorf("failed to enqueue extraction: %w", err)
	}

	// Get the auto-generated ID
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get queue item ID: %w", err)
	}
	item.ID = id

	return nil
}

// DequeueExtraction retrieves pending items from the extraction queue.
func (b *Backend) DequeueExtraction(ctx context.Context, batchSize int) ([]*types.ExtractionQueueItem, error) {
	if batchSize <= 0 {
		batchSize = 10
	}

	return b.withTxResult(ctx, func(tx *sql.Tx) ([]*types.ExtractionQueueItem, error) {
		// Get pending items
		rows, err := tx.QueryContext(ctx, `
			SELECT id, namespace, source_type, source_id, content, status, attempts, created_at, processed_at
			FROM entity_extraction_queue
			WHERE status = 'pending'
			ORDER BY created_at
			LIMIT ?
		`, batchSize)
		if err != nil {
			return nil, fmt.Errorf("failed to query queue: %w", err)
		}
		defer rows.Close()

		var items []*types.ExtractionQueueItem
		var ids []any
		for rows.Next() {
			item := &types.ExtractionQueueItem{}
			var createdAtUnix int64
			var processedAtUnix sql.NullInt64

			if err := rows.Scan(
				&item.ID, &item.Namespace, &item.SourceType, &item.SourceID,
				&item.Content, &item.Status, &item.Attempts,
				&createdAtUnix, &processedAtUnix,
			); err != nil {
				return nil, fmt.Errorf("failed to scan queue item: %w", err)
			}

			item.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
			if processedAtUnix.Valid {
				t := time.Unix(processedAtUnix.Int64, 0).UTC()
				item.ProcessedAt = &t
			}

			items = append(items, item)
			ids = append(ids, item.ID)
		}

		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("failed to iterate queue: %w", err)
		}

		// Mark items as processing
		if len(ids) > 0 {
			placeholders := make([]string, len(ids))
			for i := range placeholders {
				placeholders[i] = "?"
			}
			query := fmt.Sprintf(
				"UPDATE entity_extraction_queue SET status = 'processing', attempts = attempts + 1 WHERE id IN (%s)",
				strings.Join(placeholders, ","),
			)
			_, err = tx.ExecContext(ctx, query, ids...)
			if err != nil {
				return nil, fmt.Errorf("failed to mark items as processing: %w", err)
			}
		}

		return items, nil
	})
}

// withTxResult is a helper for transactions that return a result.
func (b *Backend) withTxResult(ctx context.Context, fn func(*sql.Tx) ([]*types.ExtractionQueueItem, error)) ([]*types.ExtractionQueueItem, error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	result, err := fn(tx)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return nil, fmt.Errorf("failed to rollback transaction: %v (original error: %w)", rbErr, err)
		}
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return result, nil
}

// CompleteExtraction marks an extraction queue item as completed or failed.
func (b *Backend) CompleteExtraction(ctx context.Context, itemID int64, status string) error {
	now := time.Now().UTC()

	// Check for dead letter (too many attempts)
	var attempts int
	err := b.db.QueryRowContext(ctx,
		"SELECT attempts FROM entity_extraction_queue WHERE id = ?", itemID,
	).Scan(&attempts)
	if err == sql.ErrNoRows {
		return storage.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to check attempts: %w", err)
	}

	// Move to dead letter if failed too many times
	if status == "failed" && attempts >= 3 {
		status = "dead_letter"
	}

	_, err = b.db.ExecContext(ctx, `
		UPDATE entity_extraction_queue
		SET status = ?, processed_at = ?
		WHERE id = ?
	`, status, now.Unix(), itemID)

	if err != nil {
		return fmt.Errorf("failed to complete extraction: %w", err)
	}

	return nil
}

// GetExtractionQueueStats returns statistics about the extraction queue.
func (b *Backend) GetExtractionQueueStats(ctx context.Context) (*storage.ExtractionQueueStats, error) {
	stats := &storage.ExtractionQueueStats{}

	rows, err := b.db.QueryContext(ctx, `
		SELECT status, COUNT(*) as count
		FROM entity_extraction_queue
		GROUP BY status
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query queue stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("failed to scan stats: %w", err)
		}

		switch status {
		case "pending":
			stats.PendingCount = count
		case "processing":
			stats.ProcessingCount = count
		case "failed":
			stats.FailedCount = count
		case "dead_letter":
			stats.DeadLetterCount = count
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate stats: %w", err)
	}

	return stats, nil
}
