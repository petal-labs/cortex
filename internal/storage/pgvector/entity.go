package pgvector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"

	"github.com/petal-labs/cortex/v2/internal/storage"
	"github.com/petal-labs/cortex/v2/pkg/types"
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
	var attributesJSON, metadataJSON []byte
	var err error
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

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Verify namespace ownership if entity ID already exists
	var existingNamespace string
	err = tx.QueryRow(ctx,
		"SELECT namespace FROM entities WHERE id = $1 FOR UPDATE", entity.ID,
	).Scan(&existingNamespace)
	if err != nil && err != pgx.ErrNoRows {
		return fmt.Errorf("failed to check entity namespace: %w", err)
	}
	if err == nil && existingNamespace != entity.Namespace {
		return fmt.Errorf("entity %q belongs to another namespace: %w", entity.ID, storage.ErrInvalidInput)
	}

	// Upsert entity using ON CONFLICT guarded by namespace
	tag, err := tx.Exec(ctx, `
		INSERT INTO entities (id, namespace, name, type, aliases, summary, attributes, metadata, mention_count, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			type = EXCLUDED.type,
			aliases = EXCLUDED.aliases,
			summary = EXCLUDED.summary,
			attributes = EXCLUDED.attributes,
			metadata = EXCLUDED.metadata,
			mention_count = EXCLUDED.mention_count,
			last_seen_at = EXCLUDED.last_seen_at
		WHERE entities.namespace = EXCLUDED.namespace
	`, entity.ID, entity.Namespace, entity.Name, string(entity.Type),
		entity.Aliases, entity.Summary, attributesJSON, metadataJSON,
		entity.MentionCount, entity.FirstSeenAt, entity.LastSeenAt)
	if err != nil {
		return fmt.Errorf("failed to upsert entity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("entity %q belongs to another namespace: %w", entity.ID, storage.ErrInvalidInput)
	}

	// Update aliases in lookup table
	// First, delete existing aliases for this entity in this namespace
	_, err = tx.Exec(ctx,
		"DELETE FROM entity_aliases WHERE entity_id = $1 AND namespace = $2", entity.ID, entity.Namespace,
	)
	if err != nil {
		return fmt.Errorf("failed to clear aliases: %w", err)
	}

	// Insert new aliases (including lowercase canonical name)
	aliases := append([]string{strings.ToLower(entity.Name)}, entity.Aliases...)
	for _, alias := range aliases {
		lowerAlias := strings.ToLower(alias)
		_, err = tx.Exec(ctx, `
			INSERT INTO entity_aliases (namespace, alias, entity_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (namespace, LOWER(alias)) DO NOTHING
		`, entity.Namespace, lowerAlias, entity.ID)
		if err != nil {
			return fmt.Errorf("failed to insert alias: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// GetEntityByID retrieves an entity by its ID.
func (b *Backend) GetEntityByID(ctx context.Context, namespace, entityID string) (*types.Entity, error) {
	return b.getEntity(ctx, "id = $1 AND namespace = $2", entityID, namespace)
}

// GetEntityByName retrieves an entity by its canonical name (case-insensitive).
func (b *Backend) GetEntityByName(ctx context.Context, namespace, name string) (*types.Entity, error) {
	return b.getEntity(ctx, "namespace = $1 AND LOWER(name) = LOWER($2)", namespace, name)
}

// getEntity is a helper for retrieving entities by different criteria.
func (b *Backend) getEntity(ctx context.Context, where string, args ...any) (*types.Entity, error) {
	entity := &types.Entity{}
	var attributesJSON, metadataJSON []byte
	var summary *string
	var entityType string

	err := b.pool.QueryRow(ctx, `
		SELECT id, namespace, name, type, aliases, summary, attributes, metadata,
		       mention_count, first_seen_at, last_seen_at
		FROM entities
		WHERE `+where, args...).Scan(
		&entity.ID, &entity.Namespace, &entity.Name, &entityType,
		&entity.Aliases, &summary, &attributesJSON, &metadataJSON,
		&entity.MentionCount, &entity.FirstSeenAt, &entity.LastSeenAt,
	)

	if err == pgx.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query entity: %w", err)
	}

	entity.Type = types.EntityType(entityType)
	if summary != nil {
		entity.Summary = *summary
	}

	// Parse JSON fields
	if len(attributesJSON) > 0 {
		if err := json.Unmarshal(attributesJSON, &entity.Attributes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
		}
	}
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &entity.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	return entity, nil
}

// ResolveAlias looks up an entity by an alias.
func (b *Backend) ResolveAlias(ctx context.Context, namespace, alias string) (*types.Entity, error) {
	var entityID string
	err := b.pool.QueryRow(ctx, `
		SELECT entity_id FROM entity_aliases
		WHERE namespace = $1 AND LOWER(alias) = LOWER($2)
	`, namespace, alias).Scan(&entityID)

	if err == pgx.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to resolve alias: %w", err)
	}

	return b.GetEntityByID(ctx, namespace, entityID)
}

// SearchEntities performs semantic search across entity summaries.
// Uses pgvector's HNSW index for approximate nearest neighbor search.
func (b *Backend) SearchEntities(ctx context.Context, namespace string, embedding []float32, opts storage.EntitySearchOpts) ([]*types.EntityResult, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	queryVector := pgvector.NewVector(embedding)

	// Build the query
	var args []any
	query := `
		SELECT e.id, e.namespace, e.name, e.type, e.aliases, e.summary,
		       e.attributes, e.metadata, e.mention_count, e.first_seen_at, e.last_seen_at,
		       1 - (emb.embedding <=> $1) AS score
		FROM entity_embeddings emb
		JOIN entities e ON e.id = emb.entity_id
		WHERE e.namespace = $2
	`
	args = append(args, queryVector, namespace)
	argNum := 3

	// Optional: filter by entity type
	if opts.EntityType != nil {
		query += fmt.Sprintf(" AND e.type = $%d", argNum)
		args = append(args, string(*opts.EntityType))
		argNum++
	}

	// Optional: minimum score filter
	if opts.MinScore > 0 {
		query += fmt.Sprintf(" AND 1 - (emb.embedding <=> $1) >= $%d", argNum)
		args = append(args, opts.MinScore)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY emb.embedding <=> $1 ASC LIMIT $%d", argNum)
	args = append(args, opts.TopK)

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search entities: %w", err)
	}
	defer rows.Close()

	var results []*types.EntityResult
	for rows.Next() {
		entity := &types.Entity{}
		var attributesJSON, metadataJSON []byte
		var summary *string
		var entityType string
		var score float64

		if err := rows.Scan(
			&entity.ID, &entity.Namespace, &entity.Name, &entityType,
			&entity.Aliases, &summary, &attributesJSON, &metadataJSON,
			&entity.MentionCount, &entity.FirstSeenAt, &entity.LastSeenAt, &score,
		); err != nil {
			return nil, fmt.Errorf("failed to scan entity: %w", err)
		}

		entity.Type = types.EntityType(entityType)
		if summary != nil {
			entity.Summary = *summary
		}

		if len(attributesJSON) > 0 {
			if err := json.Unmarshal(attributesJSON, &entity.Attributes); err != nil {
				return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
			}
		}
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &entity.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
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
		WHERE namespace = $1
	`
	args = append(args, namespace)
	argNum := 2

	// Filter by entity type
	if opts.EntityType != nil {
		query += fmt.Sprintf(" AND type = $%d", argNum)
		args = append(args, string(*opts.EntityType))
		argNum++
	}

	// Handle cursor-based pagination
	sortBy := opts.SortBy
	if sortBy == "" {
		sortBy = types.EntitySortByName
	}

	switch sortBy {
	case types.EntitySortByMentionCount:
		if opts.Cursor != "" {
			query += fmt.Sprintf(" AND mention_count < $%d", argNum)
			args = append(args, opts.Cursor)
			argNum++
		}
		query += " ORDER BY mention_count DESC, id"
	case types.EntitySortByLastSeen:
		if opts.Cursor != "" {
			query += fmt.Sprintf(" AND last_seen_at < $%d", argNum)
			args = append(args, opts.Cursor)
			argNum++
		}
		query += " ORDER BY last_seen_at DESC, id"
	default: // EntitySortByName
		if opts.Cursor != "" {
			query += fmt.Sprintf(" AND name > $%d", argNum)
			args = append(args, opts.Cursor)
			argNum++
		}
		query += " ORDER BY name, id"
	}

	query += fmt.Sprintf(" LIMIT $%d", argNum)
	args = append(args, opts.Limit+1)

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query entities: %w", err)
	}
	defer rows.Close()

	var entities []*types.Entity
	for rows.Next() {
		entity := &types.Entity{}
		var attributesJSON, metadataJSON []byte
		var summary *string
		var entityType string

		if err := rows.Scan(
			&entity.ID, &entity.Namespace, &entity.Name, &entityType,
			&entity.Aliases, &summary, &attributesJSON, &metadataJSON,
			&entity.MentionCount, &entity.FirstSeenAt, &entity.LastSeenAt,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan entity: %w", err)
		}

		entity.Type = types.EntityType(entityType)
		if summary != nil {
			entity.Summary = *summary
		}

		if len(attributesJSON) > 0 {
			if err := json.Unmarshal(attributesJSON, &entity.Attributes); err != nil {
				return nil, "", fmt.Errorf("failed to unmarshal attributes: %w", err)
			}
		}
		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &entity.Metadata); err != nil {
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
				nextCursor = last.LastSeenAt.Format(time.RFC3339Nano)
			default:
				nextCursor = last.Name
			}
		}
	}

	return entities, nextCursor, nil
}

// DeleteEntity removes an entity and all its mentions/relationships.
func (b *Backend) DeleteEntity(ctx context.Context, namespace, entityID string) error {
	result, err := b.pool.Exec(ctx,
		"DELETE FROM entities WHERE id = $1 AND namespace = $2",
		entityID, namespace,
	)
	if err != nil {
		return fmt.Errorf("failed to delete entity: %w", err)
	}

	if result.RowsAffected() == 0 {
		return storage.ErrNotFound
	}

	return nil
}

// MergeEntities combines two entities, moving all data from source to target.
func (b *Backend) MergeEntities(ctx context.Context, namespace, sourceID, targetID string) error {
	if sourceID == "" || targetID == "" {
		return fmt.Errorf("source and target entity IDs are required: %w", storage.ErrInvalidInput)
	}
	if sourceID == targetID {
		return fmt.Errorf("cannot merge entity %q into itself: %w", sourceID, storage.ErrInvalidInput)
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Verify both entities exist in this namespace and load their names/aliases
	var sourceName, targetName string
	var sourceAliases, targetAliases []string

	err = tx.QueryRow(ctx,
		"SELECT name, aliases FROM entities WHERE id = $1 AND namespace = $2",
		sourceID, namespace,
	).Scan(&sourceName, &sourceAliases)
	if err == pgx.ErrNoRows {
		return storage.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to load source entity: %w", err)
	}

	err = tx.QueryRow(ctx,
		"SELECT name, aliases FROM entities WHERE id = $1 AND namespace = $2",
		targetID, namespace,
	).Scan(&targetName, &targetAliases)
	if err == pgx.ErrNoRows {
		return storage.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to load target entity: %w", err)
	}

	// Move mentions from source to target
	_, err = tx.Exec(ctx,
		"UPDATE entity_mentions SET entity_id = $1 WHERE namespace = $2 AND entity_id = $3",
		targetID, namespace, sourceID,
	)
	if err != nil {
		return fmt.Errorf("failed to move mentions: %w", err)
	}

	// Remove direct relationships between source and target (would become self-references)
	_, err = tx.Exec(ctx, `
		DELETE FROM entity_relationships
		WHERE namespace = $1
		  AND ((source_entity_id = $2 AND target_entity_id = $3)
		    OR (source_entity_id = $3 AND target_entity_id = $2))
	`, namespace, sourceID, targetID)
	if err != nil {
		return fmt.Errorf("failed to delete direct relationships between merged entities: %w", err)
	}

	type relRow struct {
		id           string
		otherID      string
		relationType string
		confidence   float64
		mentionCount int64
		createdAt    time.Time
		updatedAt    time.Time
	}

	// Reconcile outgoing relationships (source_entity_id = sourceID)
	outRows, err := tx.Query(ctx, `
		SELECT id, target_entity_id, relation_type, confidence, mention_count, created_at, updated_at
		FROM entity_relationships
		WHERE namespace = $1 AND source_entity_id = $2
	`, namespace, sourceID)
	if err != nil {
		return fmt.Errorf("failed to query outgoing relationships: %w", err)
	}
	var outgoing []relRow
	for outRows.Next() {
		var r relRow
		if err := outRows.Scan(&r.id, &r.otherID, &r.relationType, &r.confidence, &r.mentionCount, &r.createdAt, &r.updatedAt); err != nil {
			outRows.Close()
			return fmt.Errorf("failed to scan outgoing relationship: %w", err)
		}
		outgoing = append(outgoing, r)
	}
	if err := outRows.Err(); err != nil {
		outRows.Close()
		return fmt.Errorf("failed to iterate outgoing relationships: %w", err)
	}
	outRows.Close()

	for _, r := range outgoing {
		var existingID string
		err := tx.QueryRow(ctx, `
			SELECT id FROM entity_relationships
			WHERE namespace = $1 AND source_entity_id = $2 AND target_entity_id = $3 AND relation_type = $4
		`, namespace, targetID, r.otherID, r.relationType).Scan(&existingID)
		if err == pgx.ErrNoRows {
			if _, err := tx.Exec(ctx, `
				UPDATE entity_relationships
				SET source_entity_id = $1
				WHERE id = $2 AND namespace = $3
			`, targetID, r.id, namespace); err != nil {
				return fmt.Errorf("failed to repoint outgoing relationship: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to check overlapping outgoing relationship: %w", err)
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE entity_relationships
				SET mention_count = mention_count + $1,
				    confidence = GREATEST(confidence, $2),
				    created_at = LEAST(created_at, $3),
				    updated_at = GREATEST(updated_at, $4)
				WHERE id = $5 AND namespace = $6
			`, r.mentionCount, r.confidence, r.createdAt, r.updatedAt, existingID, namespace); err != nil {
				return fmt.Errorf("failed to merge overlapping outgoing relationship: %w", err)
			}
			if _, err := tx.Exec(ctx,
				"DELETE FROM entity_relationships WHERE id = $1 AND namespace = $2",
				r.id, namespace,
			); err != nil {
				return fmt.Errorf("failed to delete merged outgoing relationship: %w", err)
			}
		}
	}

	// Reconcile incoming relationships (target_entity_id = sourceID)
	inRows, err := tx.Query(ctx, `
		SELECT id, source_entity_id, relation_type, confidence, mention_count, created_at, updated_at
		FROM entity_relationships
		WHERE namespace = $1 AND target_entity_id = $2
	`, namespace, sourceID)
	if err != nil {
		return fmt.Errorf("failed to query incoming relationships: %w", err)
	}
	var incoming []relRow
	for inRows.Next() {
		var r relRow
		if err := inRows.Scan(&r.id, &r.otherID, &r.relationType, &r.confidence, &r.mentionCount, &r.createdAt, &r.updatedAt); err != nil {
			inRows.Close()
			return fmt.Errorf("failed to scan incoming relationship: %w", err)
		}
		incoming = append(incoming, r)
	}
	if err := inRows.Err(); err != nil {
		inRows.Close()
		return fmt.Errorf("failed to iterate incoming relationships: %w", err)
	}
	inRows.Close()

	for _, r := range incoming {
		var existingID string
		err := tx.QueryRow(ctx, `
			SELECT id FROM entity_relationships
			WHERE namespace = $1 AND source_entity_id = $2 AND target_entity_id = $3 AND relation_type = $4
		`, namespace, r.otherID, targetID, r.relationType).Scan(&existingID)
		if err == pgx.ErrNoRows {
			if _, err := tx.Exec(ctx, `
				UPDATE entity_relationships
				SET target_entity_id = $1
				WHERE id = $2 AND namespace = $3
			`, targetID, r.id, namespace); err != nil {
				return fmt.Errorf("failed to repoint incoming relationship: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to check overlapping incoming relationship: %w", err)
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE entity_relationships
				SET mention_count = mention_count + $1,
				    confidence = GREATEST(confidence, $2),
				    created_at = LEAST(created_at, $3),
				    updated_at = GREATEST(updated_at, $4)
				WHERE id = $5 AND namespace = $6
			`, r.mentionCount, r.confidence, r.createdAt, r.updatedAt, existingID, namespace); err != nil {
				return fmt.Errorf("failed to merge overlapping incoming relationship: %w", err)
			}
			if _, err := tx.Exec(ctx,
				"DELETE FROM entity_relationships WHERE id = $1 AND namespace = $2",
				r.id, namespace,
			); err != nil {
				return fmt.Errorf("failed to delete merged incoming relationship: %w", err)
			}
		}
	}

	// Delete self-referencing relationships scoped to this namespace
	_, err = tx.Exec(ctx,
		"DELETE FROM entity_relationships WHERE namespace = $1 AND source_entity_id = target_entity_id",
		namespace,
	)
	if err != nil {
		return fmt.Errorf("failed to clean self-references: %w", err)
	}

	// Move aliases from source to target (case-insensitive dedup)
	_, err = tx.Exec(ctx, `
		UPDATE entity_aliases SET entity_id = $1
		WHERE namespace = $2 AND entity_id = $3
		AND NOT EXISTS (
			SELECT 1 FROM entity_aliases ea2
			WHERE ea2.namespace = $2 AND ea2.entity_id = $1 AND LOWER(ea2.alias) = LOWER(entity_aliases.alias)
		)
	`, targetID, namespace, sourceID)
	if err != nil {
		return fmt.Errorf("failed to move aliases: %w", err)
	}

	// Ensure source canonical name is registered as an alias for target
	if sourceName != "" && !strings.EqualFold(sourceName, targetName) {
		_, err = tx.Exec(ctx, `
			INSERT INTO entity_aliases (namespace, alias, entity_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (namespace, LOWER(alias)) DO NOTHING
		`, namespace, strings.ToLower(sourceName), targetID)
		if err != nil {
			return fmt.Errorf("failed to register source name as alias: %w", err)
		}
	}

	// Collect any aliases present in entity_aliases for target
	aliasRows, err := tx.Query(ctx,
		"SELECT alias FROM entity_aliases WHERE namespace = $1 AND entity_id = $2 ORDER BY alias",
		namespace, targetID,
	)
	if err != nil {
		return fmt.Errorf("failed to query target aliases: %w", err)
	}
	var lookupAliases []string
	for aliasRows.Next() {
		var a string
		if err := aliasRows.Scan(&a); err != nil {
			aliasRows.Close()
			return fmt.Errorf("failed to scan alias: %w", err)
		}
		lookupAliases = append(lookupAliases, a)
	}
	if err := aliasRows.Err(); err != nil {
		aliasRows.Close()
		return fmt.Errorf("failed to iterate aliases: %w", err)
	}
	aliasRows.Close()

	mergedAliases := mergeEntityAliases(targetName, targetAliases, sourceName, sourceAliases, lookupAliases)

	// Update target's mention count and synchronized aliases
	_, err = tx.Exec(ctx, `
		UPDATE entities
		SET mention_count = (SELECT COUNT(*) FROM entity_mentions WHERE namespace = $1 AND entity_id = $2),
		    aliases = $3
		WHERE id = $2 AND namespace = $1
	`, namespace, targetID, mergedAliases)
	if err != nil {
		return fmt.Errorf("failed to update target entity: %w", err)
	}

	// Delete source entity (cascades to remaining aliases)
	_, err = tx.Exec(ctx,
		"DELETE FROM entities WHERE id = $1 AND namespace = $2", sourceID, namespace,
	)
	if err != nil {
		return fmt.Errorf("failed to delete source entity: %w", err)
	}

	return tx.Commit(ctx)
}

// mergeEntityAliases merges aliases from target, source, source's canonical name,
// and lookup table entries with case-insensitive deduplication, excluding target's own name.
func mergeEntityAliases(targetName string, targetAliases []string, sourceName string, sourceAliases, lookupAliases []string) []string {
	seen := make(map[string]bool)
	if targetName != "" {
		seen[strings.ToLower(strings.TrimSpace(targetName))] = true
	}

	merged := make([]string, 0, len(targetAliases)+len(sourceAliases)+1)
	add := func(alias string) {
		a := strings.TrimSpace(alias)
		if a == "" {
			return
		}
		lower := strings.ToLower(a)
		if seen[lower] {
			return
		}
		seen[lower] = true
		merged = append(merged, a)
	}

	for _, a := range targetAliases {
		add(a)
	}
	for _, a := range sourceAliases {
		add(a)
	}
	if sourceName != "" {
		add(sourceName)
	}
	for _, a := range lookupAliases {
		add(a)
	}
	return merged
}

// InsertMention records a mention of an entity in source content.
func (b *Backend) InsertMention(ctx context.Context, mention *types.EntityMention) error {
	if mention.CreatedAt.IsZero() {
		mention.CreatedAt = time.Now().UTC()
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Verify entity exists in the mention's namespace and update mention count + last_seen_at
	tag, err := tx.Exec(ctx, `
		UPDATE entities
		SET mention_count = mention_count + 1, last_seen_at = $1
		WHERE id = $2 AND namespace = $3
	`, mention.CreatedAt, mention.EntityID, mention.Namespace)
	if err != nil {
		return fmt.Errorf("failed to update entity mention count: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrNotFound
	}

	// Insert mention — id is SERIAL (auto-generated), so omit it and use
	// RETURNING to populate the generated ID back onto the struct.
	err = tx.QueryRow(ctx, `
		INSERT INTO entity_mentions (entity_id, namespace, source_type, source_id, context, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, mention.EntityID, mention.Namespace, mention.SourceType,
		mention.SourceID, mention.Context, mention.CreatedAt).Scan(&mention.ID)
	if err != nil {
		return fmt.Errorf("failed to insert mention: %w", err)
	}

	return tx.Commit(ctx)
}

// GetMentions retrieves recent mentions of an entity.
func (b *Backend) GetMentions(ctx context.Context, entityID string, limit int) ([]*types.EntityMention, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := b.pool.Query(ctx, `
		SELECT id, entity_id, namespace, source_type, source_id, context, created_at
		FROM entity_mentions
		WHERE entity_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, entityID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query mentions: %w", err)
	}
	defer rows.Close()

	var mentions []*types.EntityMention
	for rows.Next() {
		mention := &types.EntityMention{}
		var context *string

		if err := rows.Scan(
			&mention.ID, &mention.EntityID, &mention.Namespace,
			&mention.SourceType, &mention.SourceID,
			&context, &mention.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan mention: %w", err)
		}

		if context != nil {
			mention.Context = *context
		}

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

	// Use ON CONFLICT for upsert — increment mention_count on conflict.
	_, err := b.pool.Exec(ctx, `
		INSERT INTO entity_relationships
			(id, namespace, source_entity_id, target_entity_id, relation_type, description, confidence, mention_count, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (namespace, source_entity_id, target_entity_id, relation_type) DO UPDATE SET
			description = EXCLUDED.description,
			confidence = EXCLUDED.confidence,
			mention_count = entity_relationships.mention_count + 1,
			updated_at = EXCLUDED.updated_at
	`, rel.ID, rel.Namespace, rel.SourceEntityID, rel.TargetEntityID, rel.RelationType,
		rel.Description, rel.Confidence, rel.MentionCount, rel.FirstSeenAt, rel.LastSeenAt)

	if err != nil {
		return fmt.Errorf("failed to upsert relationship: %w", err)
	}

	return nil
}

// GetRelationships retrieves relationships for an entity.
func (b *Backend) GetRelationships(ctx context.Context, namespace, entityID string, opts storage.RelationshipOpts) ([]*types.EntityRelationship, error) {
	var args []any
	var conditions []string

	conditions = append(conditions, "namespace = $1")
	args = append(args, namespace)
	argNum := 2

	// Build direction condition
	switch opts.Direction {
	case types.RelationshipDirectionOutgoing:
		conditions = append(conditions, fmt.Sprintf("source_entity_id = $%d", argNum))
		args = append(args, entityID)
		argNum++
	case types.RelationshipDirectionIncoming:
		conditions = append(conditions, fmt.Sprintf("target_entity_id = $%d", argNum))
		args = append(args, entityID)
		argNum++
	default: // Both
		conditions = append(conditions, fmt.Sprintf("(source_entity_id = $%d OR target_entity_id = $%d)", argNum, argNum+1))
		args = append(args, entityID, entityID)
		argNum += 2
	}

	// Filter by relation type if specified
	if opts.RelationType != nil {
		conditions = append(conditions, fmt.Sprintf("relation_type = $%d", argNum))
		args = append(args, *opts.RelationType)
	}

	query := `
		SELECT id, namespace, source_entity_id, target_entity_id, relation_type,
		       description, confidence, mention_count, created_at, updated_at
		FROM entity_relationships
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY updated_at DESC
	`

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query relationships: %w", err)
	}
	defer rows.Close()

	var relationships []*types.EntityRelationship
	for rows.Next() {
		rel := &types.EntityRelationship{}
		var description *string

		if err := rows.Scan(
			&rel.ID, &rel.Namespace, &rel.SourceEntityID, &rel.TargetEntityID,
			&rel.RelationType, &description, &rel.Confidence, &rel.MentionCount,
			&rel.FirstSeenAt, &rel.LastSeenAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan relationship: %w", err)
		}

		if description != nil {
			rel.Description = *description
		}

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
	err := b.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM entities WHERE id = $1 AND namespace = $2)",
		entityID, namespace,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check entity existence: %w", err)
	}
	if !exists {
		return storage.ErrNotFound
	}

	// Insert alias using ON CONFLICT
	_, err = b.pool.Exec(ctx, `
		INSERT INTO entity_aliases (namespace, alias, entity_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (namespace, LOWER(alias)) DO UPDATE SET entity_id = EXCLUDED.entity_id
	`, namespace, strings.ToLower(alias), entityID)

	if err != nil {
		return fmt.Errorf("failed to register alias: %w", err)
	}

	return nil
}

// StoreEntityEmbedding stores the embedding for an entity's summary.
func (b *Backend) StoreEntityEmbedding(ctx context.Context, entityID string, embedding []float32) error {
	_, err := b.pool.Exec(ctx, `
		INSERT INTO entity_embeddings (entity_id, embedding)
		VALUES ($1, $2)
		ON CONFLICT (entity_id) DO UPDATE SET embedding = EXCLUDED.embedding
	`, entityID, pgvector.NewVector(embedding))

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

	err := b.pool.QueryRow(ctx, `
		INSERT INTO extraction_queue (namespace, source_type, source_id, content, status, retry_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`, item.Namespace, item.SourceType, item.SourceID, item.Content,
		item.Status, item.Attempts, item.CreatedAt).Scan(&item.ID)

	if err != nil {
		return fmt.Errorf("failed to enqueue extraction: %w", err)
	}

	return nil
}

// DequeueExtraction retrieves pending items from the extraction queue.
func (b *Backend) DequeueExtraction(ctx context.Context, batchSize int) ([]*types.ExtractionQueueItem, error) {
	if batchSize <= 0 {
		batchSize = 10
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Get and lock pending items whose backoff (if any) has elapsed,
	// using FOR UPDATE SKIP LOCKED
	rows, err := tx.Query(ctx, `
		SELECT id, namespace, source_type, source_id, content, status, retry_count, created_at, processed_at, next_retry_at
		FROM extraction_queue
		WHERE status = 'pending'
			AND (next_retry_at IS NULL OR next_retry_at <= NOW())
		ORDER BY created_at
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`, batchSize)
	if err != nil {
		return nil, fmt.Errorf("failed to query queue: %w", err)
	}

	var items []*types.ExtractionQueueItem
	var ids []int64
	for rows.Next() {
		item := &types.ExtractionQueueItem{}
		var processedAt *time.Time

		if err := rows.Scan(
			&item.ID, &item.Namespace, &item.SourceType, &item.SourceID,
			&item.Content, &item.Status, &item.Attempts,
			&item.CreatedAt, &processedAt, &item.NextRetryAt,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to scan queue item: %w", err)
		}

		item.ProcessedAt = processedAt
		items = append(items, item)
		ids = append(ids, item.ID)
	}
	rows.Close()

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate queue: %w", err)
	}

	// Mark items as processing
	if len(ids) > 0 {
		// Build placeholders for IN clause
		placeholders := make([]string, len(ids))
		args := make([]any, len(ids))
		for i, id := range ids {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
			args[i] = id
		}
		query := fmt.Sprintf(
			"UPDATE extraction_queue SET status = 'processing', retry_count = retry_count + 1 WHERE id IN (%s)",
			strings.Join(placeholders, ","),
		)
		_, err = tx.Exec(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("failed to mark items as processing: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return items, nil
}

// RequeueExtraction returns a dequeued ("processing") item to the pending
// state, scheduling its next attempt no earlier than nextRetryAt (a zero
// time means immediately eligible). When countFailure is false the attempt
// increment applied at dequeue is undone.
func (b *Backend) RequeueExtraction(ctx context.Context, itemID int64, nextRetryAt time.Time, countFailure bool) error {
	attemptExpr := "retry_count = retry_count - 1"
	if countFailure {
		attemptExpr = "retry_count = retry_count"
	}

	var retryVal any
	if !nextRetryAt.IsZero() {
		retryVal = nextRetryAt.UTC()
	}

	result, err := b.pool.Exec(ctx, `
		UPDATE extraction_queue
		SET status = 'pending', next_retry_at = $1, `+attemptExpr+`
		WHERE id = $2
	`, retryVal, itemID)
	if err != nil {
		return fmt.Errorf("failed to requeue extraction: %w", err)
	}

	if result.RowsAffected() == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// CompleteExtraction marks an extraction queue item as completed or failed.
func (b *Backend) CompleteExtraction(ctx context.Context, itemID int64, status string) error {
	now := time.Now().UTC()

	// Check for dead letter (too many attempts)
	var attempts int
	err := b.pool.QueryRow(ctx,
		"SELECT retry_count FROM extraction_queue WHERE id = $1", itemID,
	).Scan(&attempts)
	if err == pgx.ErrNoRows {
		return storage.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to check attempts: %w", err)
	}

	// Move to dead letter if failed too many times
	if status == "failed" && attempts >= 3 {
		status = "dead_letter"
	}

	_, err = b.pool.Exec(ctx, `
		UPDATE extraction_queue
		SET status = $1, processed_at = $2
		WHERE id = $3
	`, status, now, itemID)

	if err != nil {
		return fmt.Errorf("failed to complete extraction: %w", err)
	}

	return nil
}

// GetExtractionQueueStats returns statistics about the extraction queue.
func (b *Backend) GetExtractionQueueStats(ctx context.Context) (*storage.ExtractionQueueStats, error) {
	stats := &storage.ExtractionQueueStats{}

	rows, err := b.pool.Query(ctx, `
		SELECT status, COUNT(*) as count
		FROM extraction_queue
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
