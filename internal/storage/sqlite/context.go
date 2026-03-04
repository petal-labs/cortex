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

// GetContext retrieves a context entry by key.
// runID is optional: nil means persistent (cross-run) context.
func (b *Backend) GetContext(ctx context.Context, namespace, key string, runID *string) (*types.ContextEntry, error) {
	// Convert nil runID to empty string for persistent context
	runIDStr := ""
	if runID != nil {
		runIDStr = *runID
	}

	var entry types.ContextEntry
	var valueJSON string
	var updatedBy sql.NullString
	var ttlExpiresAt sql.NullInt64
	var updatedAtUnix int64

	err := b.db.QueryRowContext(ctx, `
		SELECT namespace, run_id, key, value, version, updated_at, updated_by, ttl_expires_at
		FROM context_entries
		WHERE namespace = ? AND run_id = ? AND key = ?
	`, namespace, runIDStr, key).Scan(
		&entry.Namespace, &runIDStr, &entry.Key, &valueJSON,
		&entry.Version, &updatedAtUnix, &updatedBy, &ttlExpiresAt,
	)

	if err == sql.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query context entry: %w", err)
	}

	// Parse the value JSON
	if err := json.Unmarshal([]byte(valueJSON), &entry.Value); err != nil {
		return nil, fmt.Errorf("failed to unmarshal context value: %w", err)
	}

	// Set optional fields
	if runIDStr != "" {
		entry.RunID = &runIDStr
	}
	entry.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()
	if updatedBy.Valid {
		entry.UpdatedBy = updatedBy.String
	}
	if ttlExpiresAt.Valid {
		t := time.Unix(ttlExpiresAt.Int64, 0).UTC()
		entry.TTLExpiresAt = &t
	}

	return &entry, nil
}

// SetContext stores a context entry, incrementing the version.
// If expectedVersion is provided, the operation fails if the current version doesn't match.
func (b *Backend) SetContext(ctx context.Context, entry *types.ContextEntry, expectedVersion *int64) error {
	// Convert nil runID to empty string for persistent context
	runIDStr := ""
	if entry.RunID != nil {
		runIDStr = *entry.RunID
	}

	// Marshal value to JSON
	valueJSON, err := json.Marshal(entry.Value)
	if err != nil {
		return fmt.Errorf("failed to marshal context value: %w", err)
	}

	// Set updated timestamp
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = time.Now().UTC()
	}

	// Convert TTL to unix timestamp
	var ttlExpiresAtUnix *int64
	if entry.TTLExpiresAt != nil {
		t := entry.TTLExpiresAt.Unix()
		ttlExpiresAtUnix = &t
	}

	return b.withTx(ctx, func(tx *sql.Tx) error {
		// Check current version if expectedVersion is provided
		var currentVersion int64
		var exists bool

		err := tx.QueryRowContext(ctx, `
			SELECT version FROM context_entries
			WHERE namespace = ? AND run_id = ? AND key = ?
		`, entry.Namespace, runIDStr, entry.Key).Scan(&currentVersion)

		if err == sql.ErrNoRows {
			exists = false
			currentVersion = 0
		} else if err != nil {
			return fmt.Errorf("failed to check current version: %w", err)
		} else {
			exists = true
		}

		// Optimistic concurrency check
		if expectedVersion != nil {
			if *expectedVersion != currentVersion {
				return storage.ErrVersionConflict
			}
		}

		// Calculate new version
		newVersion := currentVersion + 1
		entry.Version = newVersion

		if exists {
			// Update existing entry
			_, err = tx.ExecContext(ctx, `
				UPDATE context_entries
				SET value = ?, version = ?, updated_at = ?, updated_by = ?, ttl_expires_at = ?
				WHERE namespace = ? AND run_id = ? AND key = ?
			`, valueJSON, newVersion, entry.UpdatedAt.Unix(), entry.UpdatedBy, ttlExpiresAtUnix,
				entry.Namespace, runIDStr, entry.Key)
		} else {
			// Insert new entry
			_, err = tx.ExecContext(ctx, `
				INSERT INTO context_entries (namespace, run_id, key, value, version, updated_at, updated_by, ttl_expires_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`, entry.Namespace, runIDStr, entry.Key, valueJSON, newVersion,
				entry.UpdatedAt.Unix(), entry.UpdatedBy, ttlExpiresAtUnix)
		}

		if err != nil {
			return fmt.Errorf("failed to set context entry: %w", err)
		}

		// Record in history
		_, err = tx.ExecContext(ctx, `
			INSERT INTO context_history (namespace, run_id, key, value, version, operation, updated_at, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, entry.Namespace, runIDStr, entry.Key, valueJSON, newVersion, "set",
			entry.UpdatedAt.Unix(), entry.UpdatedBy)

		if err != nil {
			return fmt.Errorf("failed to record context history: %w", err)
		}

		return nil
	})
}

// ListContextKeys returns all keys in a namespace, optionally filtered by prefix.
func (b *Backend) ListContextKeys(ctx context.Context, namespace string, prefix *string, runID *string, cursor string, limit int) ([]string, string, error) {
	if limit <= 0 {
		limit = 50
	}

	// Convert nil runID to empty string for persistent context
	runIDStr := ""
	if runID != nil {
		runIDStr = *runID
	}

	var args []any
	query := `
		SELECT key FROM context_entries
		WHERE namespace = ? AND run_id = ?
	`
	args = append(args, namespace, runIDStr)

	// Add prefix filter if provided
	if prefix != nil && *prefix != "" {
		query += " AND key LIKE ?"
		args = append(args, *prefix+"%")
	}

	// Add cursor for pagination (keys are ordered alphabetically)
	if cursor != "" {
		query += " AND key > ?"
		args = append(args, cursor)
	}

	query += " ORDER BY key LIMIT ?"
	args = append(args, limit+1)

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query context keys: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, "", fmt.Errorf("failed to scan key: %w", err)
		}
		keys = append(keys, key)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate keys: %w", err)
	}

	// Determine next cursor
	var nextCursor string
	if len(keys) > limit {
		keys = keys[:limit]
		if len(keys) > 0 {
			nextCursor = keys[len(keys)-1]
		}
	}

	return keys, nextCursor, nil
}

// DeleteContext removes a context entry.
func (b *Backend) DeleteContext(ctx context.Context, namespace, key string, runID *string) error {
	// Convert nil runID to empty string for persistent context
	runIDStr := ""
	if runID != nil {
		runIDStr = *runID
	}

	return b.withTx(ctx, func(tx *sql.Tx) error {
		// Get current value for history
		var valueJSON string
		var version int64
		err := tx.QueryRowContext(ctx, `
			SELECT value, version FROM context_entries
			WHERE namespace = ? AND run_id = ? AND key = ?
		`, namespace, runIDStr, key).Scan(&valueJSON, &version)

		if err == sql.ErrNoRows {
			return storage.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("failed to get context entry for delete: %w", err)
		}

		// Delete the entry
		_, err = tx.ExecContext(ctx, `
			DELETE FROM context_entries
			WHERE namespace = ? AND run_id = ? AND key = ?
		`, namespace, runIDStr, key)

		if err != nil {
			return fmt.Errorf("failed to delete context entry: %w", err)
		}

		// Record deletion in history
		_, err = tx.ExecContext(ctx, `
			INSERT INTO context_history (namespace, run_id, key, value, version, operation, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, namespace, runIDStr, key, valueJSON, version+1, "delete", time.Now().UTC().Unix())

		if err != nil {
			return fmt.Errorf("failed to record context deletion history: %w", err)
		}

		return nil
	})
}

// GetContextHistory returns the version history for a context key.
func (b *Backend) GetContextHistory(ctx context.Context, namespace, key string, runID *string, cursor string, limit int) ([]*types.ContextHistoryEntry, string, error) {
	if limit <= 0 {
		limit = 20
	}

	// Convert nil runID to empty string for persistent context
	runIDStr := ""
	if runID != nil {
		runIDStr = *runID
	}

	var args []any
	query := `
		SELECT version, value, operation, updated_at, updated_by
		FROM context_history
		WHERE namespace = ? AND run_id = ? AND key = ?
	`
	args = append(args, namespace, runIDStr, key)

	// Cursor is the version number
	if cursor != "" {
		query += " AND version < ?"
		args = append(args, cursor)
	}

	query += " ORDER BY version DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query context history: %w", err)
	}
	defer rows.Close()

	var entries []*types.ContextHistoryEntry
	for rows.Next() {
		entry := &types.ContextHistoryEntry{}
		var valueJSON string
		var updatedAtUnix int64
		var updatedBy sql.NullString

		if err := rows.Scan(&entry.Version, &valueJSON, &entry.Operation, &updatedAtUnix, &updatedBy); err != nil {
			return nil, "", fmt.Errorf("failed to scan history entry: %w", err)
		}

		// Parse the value JSON
		if err := json.Unmarshal([]byte(valueJSON), &entry.Value); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal history value: %w", err)
		}

		entry.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()
		if updatedBy.Valid {
			entry.UpdatedBy = updatedBy.String
		}

		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate history: %w", err)
	}

	// Determine next cursor
	var nextCursor string
	if len(entries) > limit {
		entries = entries[:limit]
		if len(entries) > 0 {
			nextCursor = fmt.Sprintf("%d", entries[len(entries)-1].Version)
		}
	}

	return entries, nextCursor, nil
}

// CleanupExpiredContext removes entries past their TTL expiration.
// Returns the number of entries deleted.
func (b *Backend) CleanupExpiredContext(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Unix()

	result, err := b.db.ExecContext(ctx, `
		DELETE FROM context_entries
		WHERE ttl_expires_at IS NOT NULL AND ttl_expires_at < ?
	`, now)

	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired context: %w", err)
	}

	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return deleted, nil
}

// CleanupRunContext removes all context entries for a specific run.
func (b *Backend) CleanupRunContext(ctx context.Context, namespace, runID string) error {
	_, err := b.db.ExecContext(ctx, `
		DELETE FROM context_entries
		WHERE namespace = ? AND run_id = ?
	`, namespace, runID)

	if err != nil {
		return fmt.Errorf("failed to cleanup run context: %w", err)
	}

	return nil
}
