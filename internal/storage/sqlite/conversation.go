package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// AppendMessage adds a message to a thread, creating the thread if it doesn't exist.
func (b *Backend) AppendMessage(ctx context.Context, msg *types.Message) error {
	return b.withTx(ctx, func(tx *sql.Tx) error {
		// Generate ID if not provided
		if msg.ID == "" {
			msg.ID = uuid.New().String()
		}

		// Set timestamp
		if msg.CreatedAt.IsZero() {
			msg.CreatedAt = time.Now().UTC()
		}

		// Check if thread exists, create if not
		var threadExists bool
		err := tx.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM threads WHERE id = ? AND namespace = ?)",
			msg.ThreadID, msg.Namespace,
		).Scan(&threadExists)
		if err != nil {
			return fmt.Errorf("failed to check thread existence: %w", err)
		}

		if !threadExists {
			// Create thread
			_, err := tx.ExecContext(ctx, `
				INSERT INTO threads (id, namespace, created_at, updated_at)
				VALUES (?, ?, ?, ?)
			`, msg.ThreadID, msg.Namespace, msg.CreatedAt.Unix(), msg.CreatedAt.Unix())
			if err != nil {
				return fmt.Errorf("failed to create thread: %w", err)
			}
		} else {
			// Update thread's updated_at
			_, err := tx.ExecContext(ctx,
				"UPDATE threads SET updated_at = ? WHERE id = ? AND namespace = ?",
				msg.CreatedAt.Unix(), msg.ThreadID, msg.Namespace,
			)
			if err != nil {
				return fmt.Errorf("failed to update thread timestamp: %w", err)
			}
		}

		// Marshal metadata to JSON
		var metadataJSON []byte
		if msg.Metadata != nil {
			var err error
			metadataJSON, err = json.Marshal(msg.Metadata)
			if err != nil {
				return fmt.Errorf("failed to marshal metadata: %w", err)
			}
		}

		// Insert message
		_, err = tx.ExecContext(ctx, `
			INSERT INTO messages (id, namespace, thread_id, role, content, metadata, summarized, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, msg.ID, msg.Namespace, msg.ThreadID, msg.Role, msg.Content, metadataJSON, msg.Summarized, msg.CreatedAt.Unix())
		if err != nil {
			return fmt.Errorf("failed to insert message: %w", err)
		}

		return nil
	})
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
		WHERE namespace = ? AND thread_id = ?
	`
	args = append(args, namespace, threadID)

	// Handle cursor-based pagination (cursor is the created_at timestamp)
	if cursor != "" {
		query += " AND created_at < ?"
		args = append(args, cursor)
	}

	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit+1) // Fetch one extra to determine if there are more

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query messages: %w", err)
	}
	defer rows.Close()

	var messages []*types.Message
	for rows.Next() {
		msg := &types.Message{}
		var metadataJSON sql.NullString
		var createdAtUnix int64

		if err := rows.Scan(&msg.ID, &msg.Namespace, &msg.ThreadID, &msg.Role, &msg.Content, &metadataJSON, &msg.Summarized, &createdAtUnix); err != nil {
			return nil, "", fmt.Errorf("failed to scan message: %w", err)
		}

		msg.CreatedAt = time.Unix(createdAtUnix, 0).UTC()

		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &msg.Metadata); err != nil {
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
		// There are more messages, use the last message's timestamp as cursor
		messages = messages[:limit]
		if len(messages) > 0 {
			nextCursor = fmt.Sprintf("%d", messages[len(messages)-1].CreatedAt.Unix())
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
		WHERE namespace = ?
	`
	args = append(args, namespace)

	if cursor != "" {
		query += " AND updated_at < ?"
		args = append(args, cursor)
	}

	query += " ORDER BY updated_at DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query threads: %w", err)
	}
	defer rows.Close()

	var threads []*types.Thread
	for rows.Next() {
		thread := &types.Thread{}
		var title, summary, metadataJSON sql.NullString
		var createdAtUnix, updatedAtUnix int64

		if err := rows.Scan(&thread.ID, &thread.Namespace, &title, &summary, &metadataJSON, &createdAtUnix, &updatedAtUnix); err != nil {
			return nil, "", fmt.Errorf("failed to scan thread: %w", err)
		}

		thread.Title = title.String
		thread.Summary = summary.String
		thread.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
		thread.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()

		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &thread.Metadata); err != nil {
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
			nextCursor = fmt.Sprintf("%d", threads[len(threads)-1].UpdatedAt.Unix())
		}
	}

	return threads, nextCursor, nil
}

// GetThread retrieves a single thread by ID.
func (b *Backend) GetThread(ctx context.Context, namespace, threadID string) (*types.Thread, error) {
	thread := &types.Thread{}
	var title, summary, metadataJSON sql.NullString
	var createdAtUnix, updatedAtUnix int64

	err := b.db.QueryRowContext(ctx, `
		SELECT id, namespace, title, summary, metadata, created_at, updated_at
		FROM threads
		WHERE id = ? AND namespace = ?
	`, threadID, namespace).Scan(&thread.ID, &thread.Namespace, &title, &summary, &metadataJSON, &createdAtUnix, &updatedAtUnix)

	if err == sql.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query thread: %w", err)
	}

	thread.Title = title.String
	thread.Summary = summary.String
	thread.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	thread.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()

	if metadataJSON.Valid && metadataJSON.String != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &thread.Metadata); err != nil {
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

	result, err := b.db.ExecContext(ctx, `
		UPDATE threads
		SET title = ?, summary = ?, metadata = ?, updated_at = ?
		WHERE id = ? AND namespace = ?
	`, thread.Title, thread.Summary, metadataJSON, time.Now().UTC().Unix(), thread.ID, thread.Namespace)

	if err != nil {
		return fmt.Errorf("failed to update thread: %w", err)
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

// DeleteThread removes a thread and all its messages.
func (b *Backend) DeleteThread(ctx context.Context, namespace, threadID string) error {
	result, err := b.db.ExecContext(ctx,
		"DELETE FROM threads WHERE id = ? AND namespace = ?",
		threadID, namespace,
	)
	if err != nil {
		return fmt.Errorf("failed to delete thread: %w", err)
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

// StoreMessageEmbedding stores the embedding for a message.
// Embeddings are stored in binary format for use with sqlite-vec.
func (b *Backend) StoreMessageEmbedding(ctx context.Context, messageID string, embedding []float32) error {
	// Encode embedding as binary for sqlite-vec compatibility
	embeddingBytes := encodeVectorBinary(embedding)

	_, err := b.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO message_embeddings (message_id, embedding)
		VALUES (?, ?)
	`, messageID, embeddingBytes)

	if err != nil {
		return fmt.Errorf("failed to store embedding: %w", err)
	}

	return nil
}

// MarkMessagesSummarized marks messages as having been included in a summary.
func (b *Backend) MarkMessagesSummarized(ctx context.Context, namespace, threadID string, beforeTime int64) error {
	_, err := b.db.ExecContext(ctx, `
		UPDATE messages
		SET summarized = 1
		WHERE namespace = ? AND thread_id = ? AND created_at < ?
	`, namespace, threadID, beforeTime)

	if err != nil {
		return fmt.Errorf("failed to mark messages summarized: %w", err)
	}

	return nil
}

// SearchMessages performs semantic search across messages in a namespace.
// Uses sqlite-vec's vec_distance_cosine for brute-force KNN search.
func (b *Backend) SearchMessages(ctx context.Context, namespace string, embedding []float32, opts storage.MessageSearchOpts) ([]*types.MessageResult, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	// Encode query embedding as binary for sqlite-vec
	queryEmbedding := encodeVectorBinary(embedding)

	// Build the query
	var args []any
	query := `
		SELECT m.id, m.namespace, m.thread_id, m.role, m.content, m.metadata,
		       m.summarized, m.created_at,
		       vec_distance_cosine(e.embedding, ?) AS distance
		FROM message_embeddings e
		JOIN messages m ON m.id = e.message_id
		WHERE m.namespace = ?
	`
	args = append(args, queryEmbedding, namespace)

	// Optional: filter by thread
	if opts.ThreadID != nil {
		query += " AND m.thread_id = ?"
		args = append(args, *opts.ThreadID)
	}

	query += " ORDER BY distance ASC LIMIT ?"
	args = append(args, opts.TopK)

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search messages: %w", err)
	}
	defer rows.Close()

	var results []*types.MessageResult
	for rows.Next() {
		msg := &types.Message{}
		var metadataJSON sql.NullString
		var createdAtUnix int64
		var distance float64

		if err := rows.Scan(
			&msg.ID, &msg.Namespace, &msg.ThreadID, &msg.Role, &msg.Content,
			&metadataJSON, &msg.Summarized, &createdAtUnix, &distance,
		); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}

		msg.CreatedAt = time.Unix(createdAtUnix, 0).UTC()

		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &msg.Metadata); err != nil {
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
