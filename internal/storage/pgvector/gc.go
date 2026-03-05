package pgvector

import (
	"context"
	"time"
)

// DeleteOldConversations removes threads and their messages older than the specified duration.
// Returns the number of threads deleted.
func (b *Backend) DeleteOldConversations(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)

	// First, get the count of threads to be deleted
	var count int64
	err := b.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM threads WHERE updated_at < $1",
		cutoff,
	).Scan(&count)
	if err != nil {
		return 0, err
	}

	if count == 0 {
		return 0, nil
	}

	// Delete message embeddings for old threads
	_, err = b.pool.Exec(ctx,
		`DELETE FROM message_embeddings WHERE message_id IN (
			SELECT m.id FROM messages m
			JOIN threads t ON m.thread_id = t.id AND m.namespace = t.namespace
			WHERE t.updated_at < $1
		)`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}

	// Delete messages for old threads
	_, err = b.pool.Exec(ctx,
		`DELETE FROM messages WHERE thread_id IN (
			SELECT id FROM threads WHERE updated_at < $1
		)`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}

	// Delete the threads
	result, err := b.pool.Exec(ctx,
		"DELETE FROM threads WHERE updated_at < $1",
		cutoff,
	)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected(), nil
}

// PruneStaleEntities removes entities that haven't been mentioned recently
// and have fewer mentions than the threshold.
// Returns the number of entities deleted.
func (b *Backend) PruneStaleEntities(ctx context.Context, staleDuration time.Duration, minMentions int) (int64, error) {
	cutoff := time.Now().Add(-staleDuration)

	// First, get the count of entities to be deleted
	var count int64
	err := b.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM entities
		 WHERE last_seen_at < $1 AND mention_count < $2`,
		cutoff, minMentions,
	).Scan(&count)
	if err != nil {
		return 0, err
	}

	if count == 0 {
		return 0, nil
	}

	// Get IDs of entities to delete
	rows, err := b.pool.Query(ctx,
		`SELECT id FROM entities
		 WHERE last_seen_at < $1 AND mention_count < $2`,
		cutoff, minMentions,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var entityIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		entityIDs = append(entityIDs, id)
	}

	if len(entityIDs) == 0 {
		return 0, nil
	}

	// Delete in order to respect foreign key constraints
	// 1. Delete entity embeddings
	for _, id := range entityIDs {
		_, _ = b.pool.Exec(ctx, "DELETE FROM entity_embeddings WHERE entity_id = $1", id)
	}

	// 2. Delete entity mentions
	for _, id := range entityIDs {
		_, _ = b.pool.Exec(ctx, "DELETE FROM entity_mentions WHERE entity_id = $1", id)
	}

	// 3. Delete entity relationships (both sides)
	for _, id := range entityIDs {
		_, _ = b.pool.Exec(ctx, "DELETE FROM entity_relationships WHERE source_entity_id = $1 OR target_entity_id = $1", id)
	}

	// 4. Delete entity aliases
	for _, id := range entityIDs {
		_, _ = b.pool.Exec(ctx, "DELETE FROM entity_aliases WHERE entity_id = $1", id)
	}

	// 5. Delete the entities themselves
	var deleted int64
	for _, id := range entityIDs {
		result, err := b.pool.Exec(ctx, "DELETE FROM entities WHERE id = $1", id)
		if err != nil {
			continue
		}
		deleted += result.RowsAffected()
	}

	return deleted, nil
}

// DeleteOrphanedChunks removes chunks whose parent documents no longer exist.
// Returns the number of chunks deleted.
func (b *Backend) DeleteOrphanedChunks(ctx context.Context) (int64, error) {
	// First delete chunk embeddings for orphaned chunks
	_, err := b.pool.Exec(ctx,
		`DELETE FROM chunk_embeddings WHERE chunk_id IN (
			SELECT c.id FROM chunks c
			LEFT JOIN documents d ON c.document_id = d.id
			WHERE d.id IS NULL
		)`,
	)
	if err != nil {
		return 0, err
	}

	// Delete orphaned chunks
	result, err := b.pool.Exec(ctx,
		`DELETE FROM chunks WHERE document_id NOT IN (SELECT id FROM documents)`,
	)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected(), nil
}

// CleanupContextHistory removes context version history entries older than the specified duration.
// Returns the number of history entries deleted.
func (b *Backend) CleanupContextHistory(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)

	result, err := b.pool.Exec(ctx,
		"DELETE FROM context_history WHERE created_at < $1",
		cutoff,
	)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected(), nil
}

// CleanupOldRunContext removes run-scoped context entries older than the specified duration.
// Returns the number of entries deleted.
func (b *Backend) CleanupOldRunContext(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)

	// Delete history for old run-scoped context
	_, err := b.pool.Exec(ctx,
		`DELETE FROM context_history WHERE run_id IS NOT NULL
		 AND run_id != '' AND created_at < $1`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}

	// Delete old run-scoped context entries
	result, err := b.pool.Exec(ctx,
		`DELETE FROM context_entries WHERE run_id IS NOT NULL
		 AND run_id != '' AND updated_at < $1`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected(), nil
}
