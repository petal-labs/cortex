package pgvector

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// AppendMessage adds a message to a thread, creating the thread if it doesn't exist.
func (b *Backend) AppendMessage(ctx context.Context, msg *types.Message) error {
	// Generate ID if not provided
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}

	// Set timestamp
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Check if thread exists, create if not
	var threadExists bool
	err = tx.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM threads WHERE id = $1 AND namespace = $2)",
		msg.ThreadID, msg.Namespace,
	).Scan(&threadExists)
	if err != nil {
		return fmt.Errorf("failed to check thread existence: %w", err)
	}

	if !threadExists {
		// Create thread
		_, err := tx.Exec(ctx, `
			INSERT INTO threads (id, namespace, created_at, updated_at)
			VALUES ($1, $2, $3, $4)
		`, msg.ThreadID, msg.Namespace, msg.CreatedAt, msg.CreatedAt)
		if err != nil {
			return fmt.Errorf("failed to create thread: %w", err)
		}
	} else {
		// Update thread's updated_at
		_, err := tx.Exec(ctx,
			"UPDATE threads SET updated_at = $1 WHERE id = $2 AND namespace = $3",
			msg.CreatedAt, msg.ThreadID, msg.Namespace,
		)
		if err != nil {
			return fmt.Errorf("failed to update thread timestamp: %w", err)
		}
	}

	// Marshal metadata to JSON
	var metadataJSON []byte
	if msg.Metadata != nil {
		metadataJSON, err = json.Marshal(msg.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	// Insert message
	_, err = tx.Exec(ctx, `
		INSERT INTO messages (id, namespace, thread_id, role, content, metadata, summarized, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, msg.ID, msg.Namespace, msg.ThreadID, msg.Role, msg.Content, metadataJSON, msg.Summarized, msg.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert message: %w", err)
	}

	return tx.Commit(ctx)
}

// GetMessages retrieves messages from a thread, ordered by creation time.
func (b *Backend) GetMessages(ctx context.Context, namespace, threadID string, limit int, cursor string) ([]*types.Message, string, error) {
	if limit <= 0 {
		limit = 20
	}

	var args []any
	query := `
		SELECT id, namespace, thread_id, role, content, metadata, summarized, created_at
		FROM messages
		WHERE namespace = $1 AND thread_id = $2
	`
	args = append(args, namespace, threadID)
	argNum := 3

	// Handle cursor-based pagination (cursor is the created_at timestamp)
	if cursor != "" {
		query += fmt.Sprintf(" AND created_at < $%d", argNum)
		args = append(args, cursor)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", argNum)
	args = append(args, limit+1) // Fetch one extra to determine if there are more

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query messages: %w", err)
	}
	defer rows.Close()

	var messages []*types.Message
	for rows.Next() {
		msg := &types.Message{}
		var metadataJSON []byte

		if err := rows.Scan(&msg.ID, &msg.Namespace, &msg.ThreadID, &msg.Role, &msg.Content, &metadataJSON, &msg.Summarized, &msg.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("failed to scan message: %w", err)
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &msg.Metadata); err != nil {
				return nil, "", fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate messages: %w", err)
	}

	// Determine next cursor
	var nextCursor string
	if len(messages) > limit {
		messages = messages[:limit]
		if len(messages) > 0 {
			nextCursor = messages[len(messages)-1].CreatedAt.Format(time.RFC3339Nano)
		}
	}

	// Reverse to get chronological order (oldest first)
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nextCursor, nil
}

// ListThreads returns all threads in a namespace.
func (b *Backend) ListThreads(ctx context.Context, namespace string, cursor string, limit int) ([]*types.Thread, string, error) {
	if limit <= 0 {
		limit = 50
	}

	var args []any
	query := `
		SELECT id, namespace, title, summary, metadata, created_at, updated_at
		FROM threads
		WHERE namespace = $1
	`
	args = append(args, namespace)
	argNum := 2

	if cursor != "" {
		query += fmt.Sprintf(" AND updated_at < $%d", argNum)
		args = append(args, cursor)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY updated_at DESC LIMIT $%d", argNum)
	args = append(args, limit+1)

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query threads: %w", err)
	}
	defer rows.Close()

	var threads []*types.Thread
	for rows.Next() {
		thread := &types.Thread{}
		var title, summary *string
		var metadataJSON []byte

		if err := rows.Scan(&thread.ID, &thread.Namespace, &title, &summary, &metadataJSON, &thread.CreatedAt, &thread.UpdatedAt); err != nil {
			return nil, "", fmt.Errorf("failed to scan thread: %w", err)
		}

		if title != nil {
			thread.Title = *title
		}
		if summary != nil {
			thread.Summary = *summary
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &thread.Metadata); err != nil {
				return nil, "", fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		threads = append(threads, thread)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate threads: %w", err)
	}

	var nextCursor string
	if len(threads) > limit {
		threads = threads[:limit]
		if len(threads) > 0 {
			nextCursor = threads[len(threads)-1].UpdatedAt.Format(time.RFC3339Nano)
		}
	}

	return threads, nextCursor, nil
}

// GetThread retrieves a single thread by ID.
func (b *Backend) GetThread(ctx context.Context, namespace, threadID string) (*types.Thread, error) {
	thread := &types.Thread{}
	var title, summary *string
	var metadataJSON []byte

	err := b.pool.QueryRow(ctx, `
		SELECT id, namespace, title, summary, metadata, created_at, updated_at
		FROM threads
		WHERE id = $1 AND namespace = $2
	`, threadID, namespace).Scan(&thread.ID, &thread.Namespace, &title, &summary, &metadataJSON, &thread.CreatedAt, &thread.UpdatedAt)

	if err == pgx.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query thread: %w", err)
	}

	if title != nil {
		thread.Title = *title
	}
	if summary != nil {
		thread.Summary = *summary
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &thread.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	return thread, nil
}

// UpdateThread updates a thread's metadata (title, summary).
func (b *Backend) UpdateThread(ctx context.Context, thread *types.Thread) error {
	var metadataJSON []byte
	if thread.Metadata != nil {
		var err error
		metadataJSON, err = json.Marshal(thread.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	result, err := b.pool.Exec(ctx, `
		UPDATE threads
		SET title = $1, summary = $2, metadata = $3, updated_at = $4
		WHERE id = $5 AND namespace = $6
	`, thread.Title, thread.Summary, metadataJSON, time.Now().UTC(), thread.ID, thread.Namespace)

	if err != nil {
		return fmt.Errorf("failed to update thread: %w", err)
	}

	if result.RowsAffected() == 0 {
		return storage.ErrNotFound
	}

	return nil
}

// DeleteThread removes a thread and all its messages.
func (b *Backend) DeleteThread(ctx context.Context, namespace, threadID string) error {
	result, err := b.pool.Exec(ctx,
		"DELETE FROM threads WHERE id = $1 AND namespace = $2",
		threadID, namespace,
	)
	if err != nil {
		return fmt.Errorf("failed to delete thread: %w", err)
	}

	if result.RowsAffected() == 0 {
		return storage.ErrNotFound
	}

	return nil
}

// StoreMessageEmbedding stores the embedding for a message.
func (b *Backend) StoreMessageEmbedding(ctx context.Context, messageID string, embedding []float32) error {
	_, err := b.pool.Exec(ctx, `
		INSERT INTO message_embeddings (message_id, embedding)
		VALUES ($1, $2)
		ON CONFLICT (message_id) DO UPDATE SET embedding = EXCLUDED.embedding
	`, messageID, pgvector.NewVector(embedding))

	if err != nil {
		return fmt.Errorf("failed to store embedding: %w", err)
	}

	return nil
}

// MarkMessagesSummarized marks messages as having been included in a summary.
func (b *Backend) MarkMessagesSummarized(ctx context.Context, namespace, threadID string, beforeTime int64) error {
	_, err := b.pool.Exec(ctx, `
		UPDATE messages
		SET summarized = TRUE
		WHERE namespace = $1 AND thread_id = $2 AND created_at < to_timestamp($3)
	`, namespace, threadID, beforeTime)

	if err != nil {
		return fmt.Errorf("failed to mark messages summarized: %w", err)
	}

	return nil
}

// SearchMessages performs semantic search across messages in a namespace.
// Uses pgvector's HNSW index for approximate nearest neighbor search.
func (b *Backend) SearchMessages(ctx context.Context, namespace string, embedding []float32, opts storage.MessageSearchOpts) ([]*types.MessageResult, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	queryVector := pgvector.NewVector(embedding)

	// Build the query using cosine distance operator <=>
	var args []any
	query := `
		SELECT m.id, m.namespace, m.thread_id, m.role, m.content, m.metadata,
		       m.summarized, m.created_at,
		       1 - (e.embedding <=> $1) AS score
		FROM message_embeddings e
		JOIN messages m ON m.id = e.message_id
		WHERE m.namespace = $2
	`
	args = append(args, queryVector, namespace)
	argNum := 3

	// Optional: filter by thread
	if opts.ThreadID != nil {
		query += fmt.Sprintf(" AND m.thread_id = $%d", argNum)
		args = append(args, *opts.ThreadID)
		argNum++
	}

	// Optional: minimum score filter
	if opts.MinScore > 0 {
		query += fmt.Sprintf(" AND 1 - (e.embedding <=> $1) >= $%d", argNum)
		args = append(args, opts.MinScore)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY e.embedding <=> $1 ASC LIMIT $%d", argNum)
	args = append(args, opts.TopK)

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search messages: %w", err)
	}
	defer rows.Close()

	var results []*types.MessageResult
	for rows.Next() {
		msg := &types.Message{}
		var metadataJSON []byte
		var score float64

		if err := rows.Scan(
			&msg.ID, &msg.Namespace, &msg.ThreadID, &msg.Role, &msg.Content,
			&metadataJSON, &msg.Summarized, &msg.CreatedAt, &score,
		); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &msg.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		results = append(results, &types.MessageResult{
			Message:  msg,
			Score:    score,
			ThreadID: msg.ThreadID,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate messages: %w", err)
	}

	return results, nil
}
