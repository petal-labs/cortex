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

// parseSQLiteMessageCursor parses either a composite "<unix>:<rowid>" cursor
// or a legacy "<unix>" cursor.
func parseSQLiteMessageCursor(cursor string) (int64, int64, bool) {
	if idx := strings.IndexByte(cursor, ':'); idx > 0 {
		var ts, rowid int64
		if _, err := fmt.Sscanf(cursor[:idx], "%d", &ts); err == nil {
			if _, err := fmt.Sscanf(cursor[idx+1:], "%d", &rowid); err == nil {
				return ts, rowid, true
			}
		}
	}
	var ts int64
	if _, err := fmt.Sscanf(cursor, "%d", &ts); err == nil {
		return ts, 0, false
	}
	return 0, 0, false
}

// GetMessages retrieves messages from a thread, ordered by creation time.
func (b *Backend) GetMessages(ctx context.Context, namespace, threadID string, limit int, cursor string) ([]*types.Message, string, error) {
	if limit <= 0 {
		limit = 20
	}

	var args []any
	query := `
		SELECT rowid, id, namespace, thread_id, role, content, metadata, summarized, created_at
		FROM messages
		WHERE namespace = ? AND thread_id = ?
	`
	args = append(args, namespace, threadID)

	// Handle cursor-based pagination (supports composite "<unix>:<rowid>" and legacy "<unix>")
	if cursor != "" {
		ts, rowid, composite := parseSQLiteMessageCursor(cursor)
		if composite {
			query += " AND (created_at < ? OR (created_at = ? AND rowid < ?))"
			args = append(args, ts, ts, rowid)
		} else {
			query += " AND created_at < ?"
			args = append(args, cursor)
		}
	}

	query += " ORDER BY created_at DESC, rowid DESC LIMIT ?"
	args = append(args, limit+1) // Fetch one extra to determine if there are more

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query messages: %w", err)
	}
	defer rows.Close()

	type scannedMsg struct {
		rowid int64
		msg   *types.Message
	}
	var scanned []scannedMsg
	for rows.Next() {
		msg := &types.Message{}
		var rowid int64
		var metadataJSON sql.NullString
		var createdAtUnix int64

		if err := rows.Scan(&rowid, &msg.ID, &msg.Namespace, &msg.ThreadID, &msg.Role, &msg.Content, &metadataJSON, &msg.Summarized, &createdAtUnix); err != nil {
			return nil, "", fmt.Errorf("failed to scan message: %w", err)
		}

		msg.CreatedAt = time.Unix(createdAtUnix, 0).UTC()

		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &msg.Metadata); err != nil {
				return nil, "", fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		scanned = append(scanned, scannedMsg{rowid: rowid, msg: msg})
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate messages: %w", err)
	}

	// Determine next cursor
	var nextCursor string
	if len(scanned) > limit {
		scanned = scanned[:limit]
		if len(scanned) > 0 {
			oldest := scanned[len(scanned)-1]
			nextCursor = fmt.Sprintf("%d:%d", oldest.msg.CreatedAt.Unix(), oldest.rowid)
		}
	}

	// Reverse to get chronological order (oldest first)
	messages := make([]*types.Message, len(scanned))
	for i := range scanned {
		messages[len(scanned)-1-i] = scanned[i].msg
	}

	return messages, nextCursor, nil
}

// GetUnsummarizedMessages retrieves unsummarized messages from a thread in chronological order.
// If limit > 0, returns up to the most recent limit unsummarized messages with cursor pagination.
// If limit <= 0, returns all unsummarized messages.
func (b *Backend) GetUnsummarizedMessages(ctx context.Context, namespace, threadID string, limit int, cursor string) ([]*types.Message, string, error) {
	var args []any
	query := `
		SELECT rowid, id, namespace, thread_id, role, content, metadata, summarized, created_at
		FROM messages
		WHERE namespace = ? AND thread_id = ? AND summarized = 0
	`
	args = append(args, namespace, threadID)

	if cursor != "" {
		ts, rowid, composite := parseSQLiteMessageCursor(cursor)
		if composite {
			query += " AND (created_at < ? OR (created_at = ? AND rowid < ?))"
			args = append(args, ts, ts, rowid)
		} else {
			query += " AND created_at < ?"
			args = append(args, cursor)
		}
	}

	if limit > 0 {
		query += " ORDER BY created_at DESC, rowid DESC LIMIT ?"
		args = append(args, limit+1)
	} else {
		query += " ORDER BY created_at ASC, rowid ASC"
	}

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query unsummarized messages: %w", err)
	}
	defer rows.Close()

	type scannedMsg struct {
		rowid int64
		msg   *types.Message
	}
	var scanned []scannedMsg
	for rows.Next() {
		msg := &types.Message{}
		var rowid int64
		var metadataJSON sql.NullString
		var createdAtUnix int64

		if err := rows.Scan(&rowid, &msg.ID, &msg.Namespace, &msg.ThreadID, &msg.Role, &msg.Content, &metadataJSON, &msg.Summarized, &createdAtUnix); err != nil {
			return nil, "", fmt.Errorf("failed to scan message: %w", err)
		}

		msg.CreatedAt = time.Unix(createdAtUnix, 0).UTC()

		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &msg.Metadata); err != nil {
				return nil, "", fmt.Errorf("failed to unmarshal metadata: %w", err)
			}
		}

		scanned = append(scanned, scannedMsg{rowid: rowid, msg: msg})
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate unsummarized messages: %w", err)
	}

	var nextCursor string
	var messages []*types.Message
	if limit > 0 {
		if len(scanned) > limit {
			scanned = scanned[:limit]
			if len(scanned) > 0 {
				oldest := scanned[len(scanned)-1]
				nextCursor = fmt.Sprintf("%d:%d", oldest.msg.CreatedAt.Unix(), oldest.rowid)
			}
		}
		// Reverse to get chronological order (oldest first)
		messages = make([]*types.Message, len(scanned))
		for i := range scanned {
			messages[len(scanned)-1-i] = scanned[i].msg
		}
	} else {
		messages = make([]*types.Message, len(scanned))
		for i := range scanned {
			messages[i] = scanned[i].msg
		}
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

// MarkMessagesSummarizedByIDs marks the specified message IDs as having been included in a summary.
func (b *Backend) MarkMessagesSummarizedByIDs(ctx context.Context, namespace, threadID string, messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}

	const batchSize = 500
	return b.withTx(ctx, func(tx *sql.Tx) error {
		for start := 0; start < len(messageIDs); start += batchSize {
			end := start + batchSize
			if end > len(messageIDs) {
				end = len(messageIDs)
			}
			batch := messageIDs[start:end]

			placeholders := strings.Repeat("?,", len(batch))
			placeholders = placeholders[:len(placeholders)-1]

			args := make([]any, 0, 2+len(batch))
			args = append(args, namespace, threadID)
			for _, id := range batch {
				args = append(args, id)
			}

			query := fmt.Sprintf(`
				UPDATE messages
				SET summarized = 1
				WHERE namespace = ? AND thread_id = ? AND id IN (%s)
			`, placeholders)
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
				return fmt.Errorf("failed to mark messages summarized by IDs: %w", err)
			}
		}
		return nil
	})
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

// DeleteVoiceSession removes all voice transcript messages belonging to
// sessionID strictly within namespace, along with their embeddings and any
// threads left empty.
func (b *Backend) DeleteVoiceSession(ctx context.Context, namespace, sessionID string) (int64, error) {
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(sessionID) == "" {
		return 0, storage.ErrInvalidInput
	}

	var deleted int64
	err := b.withTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, thread_id
			FROM messages
			WHERE namespace = ?
			  AND metadata IS NOT NULL
			  AND json_valid(metadata)
			  AND json_extract(metadata, '$."voice.session_id"') = ?
		`, namespace, sessionID)
		if err != nil {
			return fmt.Errorf("failed to query voice session messages: %w", err)
		}

		var msgIDs []string
		affectedThreads := make(map[string]struct{})
		for rows.Next() {
			var id, tid string
			if err := rows.Scan(&id, &tid); err != nil {
				rows.Close()
				return fmt.Errorf("failed to scan voice session message: %w", err)
			}
			msgIDs = append(msgIDs, id)
			affectedThreads[tid] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("failed to iterate voice session messages: %w", err)
		}
		if len(msgIDs) == 0 {
			return nil
		}

		for _, id := range msgIDs {
			if _, err := tx.ExecContext(ctx, "DELETE FROM message_embeddings WHERE message_id = ?", id); err != nil {
				return fmt.Errorf("failed to delete voice message embedding: %w", err)
			}
			res, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE namespace = ? AND id = ?", namespace, id)
			if err != nil {
				return fmt.Errorf("failed to delete voice message: %w", err)
			}
			n, _ := res.RowsAffected()
			deleted += n
		}

		for tid := range affectedThreads {
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM threads
				WHERE namespace = ? AND id = ?
				  AND NOT EXISTS (
				      SELECT 1 FROM messages WHERE namespace = ? AND thread_id = ?
				  )
			`, namespace, tid, namespace, tid); err != nil {
				return fmt.Errorf("failed to cleanup empty voice thread: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}
