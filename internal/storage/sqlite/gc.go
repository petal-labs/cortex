package sqlite

import (
	"context"
	"time"
)

// DeleteOldConversations removes threads and their messages older than the specified duration.
// Returns the number of threads deleted.
func (b *Backend) DeleteOldConversations(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).UnixMilli()

	// First, get the count of threads to be deleted
	var count int64
	err := b.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM threads WHERE updated_at < ?",
		cutoff,
	).Scan(&count)
	if err != nil {
		return 0, err
	}

	if count == 0 {
		return 0, nil
	}

	// Delete messages for old threads (cascade delete via embeddings)
	_, err = b.db.ExecContext(ctx,
		`DELETE FROM message_embeddings WHERE message_id IN (
			SELECT m.id FROM messages m
			JOIN threads t ON m.thread_id = t.id AND m.namespace = t.namespace
			WHERE t.updated_at < ?
		)`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}

	// Delete messages for old threads
	_, err = b.db.ExecContext(ctx,
		`DELETE FROM messages WHERE thread_id IN (
			SELECT id FROM threads WHERE updated_at < ?
		)`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}

	// Delete the threads
	result, err := b.db.ExecContext(ctx,
		"DELETE FROM threads WHERE updated_at < ?",
		cutoff,
	)
	if err != nil {
		return 0, err
	}

	deleted, _ := result.RowsAffected()
	return deleted, nil
}

// PruneStaleEntities removes entities that haven't been mentioned recently
// and have fewer mentions than the threshold.
// Returns the number of entities deleted.
func (b *Backend) PruneStaleEntities(ctx context.Context, staleDuration time.Duration, minMentions int) (int64, error) {
	cutoff := time.Now().Add(-staleDuration)

	// First, get the count of entities to be deleted
	var count int64
	err := b.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entities
		 WHERE last_seen_at < ? AND mention_count < ?`,
		cutoff, minMentions,
	).Scan(&count)
	if err != nil {
		return 0, err
	}

	if count == 0 {
		return 0, nil
	}

	// Get IDs of entities to delete
	rows, err := b.db.QueryContext(ctx,
		`SELECT id FROM entities
		 WHERE last_seen_at < ? AND mention_count < ?`,
		cutoff, minMentions,
	)
	if err != nil {
		return 0, err
	}

	var entityIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		entityIDs = append(entityIDs, id)
	}
	rows.Close()

	if len(entityIDs) == 0 {
		return 0, nil
	}

	// Delete in order to respect foreign key constraints
	// 1. Delete entity embeddings
	for _, id := range entityIDs {
		_, _ = b.db.ExecContext(ctx, "DELETE FROM entity_embeddings WHERE entity_id = ?", id)
	}

	// 2. Delete entity mentions
	for _, id := range entityIDs {
		_, _ = b.db.ExecContext(ctx, "DELETE FROM entity_mentions WHERE entity_id = ?", id)
	}

	// 3. Delete entity relationships (both sides)
	for _, id := range entityIDs {
		_, _ = b.db.ExecContext(ctx, "DELETE FROM entity_relationships WHERE source_entity_id = ? OR target_entity_id = ?", id, id)
	}

	// 4. Delete entity aliases
	for _, id := range entityIDs {
		_, _ = b.db.ExecContext(ctx, "DELETE FROM entity_aliases WHERE entity_id = ?", id)
	}

	// 5. Delete the entities themselves
	var deleted int64
	for _, id := range entityIDs {
		result, err := b.db.ExecContext(ctx, "DELETE FROM entities WHERE id = ?", id)
		if err != nil {
			continue
		}
		affected, _ := result.RowsAffected()
		deleted += affected
	}

	return deleted, nil
}

// DeleteOrphanedChunks removes chunks whose parent documents no longer exist.
// Returns the number of chunks deleted.
func (b *Backend) DeleteOrphanedChunks(ctx context.Context) (int64, error) {
	// First delete chunk embeddings for orphaned chunks
	_, err := b.db.ExecContext(ctx,
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
	result, err := b.db.ExecContext(ctx,
		`DELETE FROM chunks WHERE document_id NOT IN (SELECT id FROM documents)`,
	)
	if err != nil {
		return 0, err
	}

	deleted, _ := result.RowsAffected()
	return deleted, nil
}

// CleanupContextHistory removes context version history entries older than the specified duration.
// Returns the number of history entries deleted.
func (b *Backend) CleanupContextHistory(ctx context.Context, olderThan time.Duration) (int64, error) {
	// Convert duration to unix timestamp (history uses integer timestamps)
	cutoff := time.Now().Add(-olderThan).Unix()

	result, err := b.db.ExecContext(ctx,
		"DELETE FROM context_history WHERE updated_at < ?",
		cutoff,
	)
	if err != nil {
		return 0, err
	}

	deleted, _ := result.RowsAffected()
	return deleted, nil
}

// CleanupOldRunContext removes run-scoped context entries older than the specified duration.
// Returns the number of entries deleted.
func (b *Backend) CleanupOldRunContext(ctx context.Context, olderThan time.Duration) (int64, error) {
	// Convert duration to unix timestamp (context uses integer timestamps)
	cutoff := time.Now().Add(-olderThan).Unix()

	// Delete history for old run-scoped context
	_, err := b.db.ExecContext(ctx,
		`DELETE FROM context_history WHERE run_id IS NOT NULL
		 AND run_id != '' AND updated_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}

	// Delete old run-scoped context entries
	result, err := b.db.ExecContext(ctx,
		`DELETE FROM context_entries WHERE run_id IS NOT NULL
		 AND run_id != '' AND updated_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}

	deleted, _ := result.RowsAffected()
	return deleted, nil
}
