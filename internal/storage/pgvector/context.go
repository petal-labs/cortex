package pgvector

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// GetContext retrieves a context entry by key.
// runID is optional: nil means persistent (cross-run) context.
func (b *Backend) GetContext(ctx context.Context, namespace, key string, runID *string) (*types.ContextEntry, error) {
	var entry types.ContextEntry
	var valueJSON []byte
	var updatedBy *string
	var expiresAt *time.Time
	var runIDValue *string

	err := b.pool.QueryRow(ctx, `
		SELECT namespace, run_id, key, value, version, updated_at, updated_by, expires_at
		FROM context_entries
		WHERE namespace = $1 AND key = $2 AND COALESCE(run_id, '') = COALESCE($3, '')
	`, namespace, key, runID).Scan(
		&entry.Namespace, &runIDValue, &entry.Key, &valueJSON,
		&entry.Version, &entry.UpdatedAt, &updatedBy, &expiresAt,
	)

	if err == pgx.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query context entry: %w", err)
	}

	// Parse the value JSON
	if err := json.Unmarshal(valueJSON, &entry.Value); err != nil {
		return nil, fmt.Errorf("failed to unmarshal context value: %w", err)
	}

	// Set optional fields
	entry.RunID = runIDValue
	if updatedBy != nil {
		entry.UpdatedBy = *updatedBy
	}
	entry.TTLExpiresAt = expiresAt

	return &entry, nil
}

// SetContext stores a context entry, incrementing the version.
// If expectedVersion is provided, the operation fails if the current version doesn't match.
func (b *Backend) SetContext(ctx context.Context, entry *types.ContextEntry, expectedVersion *int64) error {
	// Marshal value to JSON
	valueJSON, err := json.Marshal(entry.Value)
	if err != nil {
		return fmt.Errorf("failed to marshal context value: %w", err)
	}

	// Set updated timestamp
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = time.Now().UTC()
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Check current version if expectedVersion is provided
	var currentVersion int64
	var exists bool

	err = tx.QueryRow(ctx, `
		SELECT version FROM context_entries
		WHERE namespace = $1 AND key = $2 AND COALESCE(run_id, '') = COALESCE($3, '')
	`, entry.Namespace, entry.Key, entry.RunID).Scan(&currentVersion)

	if err == pgx.ErrNoRows {
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
		_, err = tx.Exec(ctx, `
			UPDATE context_entries
			SET value = $1, version = $2, updated_at = $3, updated_by = $4, expires_at = $5
			WHERE namespace = $6 AND key = $7 AND COALESCE(run_id, '') = COALESCE($8, '')
		`, valueJSON, newVersion, entry.UpdatedAt, entry.UpdatedBy, entry.TTLExpiresAt,
			entry.Namespace, entry.Key, entry.RunID)
	} else {
		// Insert new entry
		_, err = tx.Exec(ctx, `
			INSERT INTO context_entries (namespace, run_id, key, value, version, updated_at, updated_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		`, entry.Namespace, entry.RunID, entry.Key, valueJSON, newVersion,
			entry.UpdatedAt, entry.UpdatedBy, entry.TTLExpiresAt)
	}

	if err != nil {
		return fmt.Errorf("failed to set context entry: %w", err)
	}

	// Record in history
	_, err = tx.Exec(ctx, `
		INSERT INTO context_history (namespace, run_id, key, value, version, operation, updated_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, entry.Namespace, entry.RunID, entry.Key, valueJSON, newVersion, "set",
		entry.UpdatedBy, entry.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to record context history: %w", err)
	}

	return tx.Commit(ctx)
}

// ListContextKeys returns all keys in a namespace, optionally filtered by prefix.
func (b *Backend) ListContextKeys(ctx context.Context, namespace string, prefix *string, runID *string, cursor string, limit int) ([]string, string, error) {
	if limit <= 0 {
		limit = 50
	}

	var args []any
	query := `
		SELECT key FROM context_entries
		WHERE namespace = $1 AND COALESCE(run_id, '') = COALESCE($2, '')
	`
	args = append(args, namespace, runID)
	argNum := 3

	// Add prefix filter if provided
	if prefix != nil && *prefix != "" {
		query += fmt.Sprintf(" AND key LIKE $%d", argNum)
		args = append(args, *prefix+"%")
		argNum++
	}

	// Add cursor for pagination (keys are ordered alphabetically)
	if cursor != "" {
		query += fmt.Sprintf(" AND key > $%d", argNum)
		args = append(args, cursor)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY key LIMIT $%d", argNum)
	args = append(args, limit+1)

	rows, err := b.pool.Query(ctx, query, args...)
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
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Get current value for history
	var valueJSON []byte
	var version int64
	err = tx.QueryRow(ctx, `
		SELECT value, version FROM context_entries
		WHERE namespace = $1 AND key = $2 AND COALESCE(run_id, '') = COALESCE($3, '')
	`, namespace, key, runID).Scan(&valueJSON, &version)

	if err == pgx.ErrNoRows {
		return storage.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to get context entry for delete: %w", err)
	}

	// Delete the entry
	_, err = tx.Exec(ctx, `
		DELETE FROM context_entries
		WHERE namespace = $1 AND key = $2 AND COALESCE(run_id, '') = COALESCE($3, '')
	`, namespace, key, runID)

	if err != nil {
		return fmt.Errorf("failed to delete context entry: %w", err)
	}

	// Record deletion in history
	_, err = tx.Exec(ctx, `
		INSERT INTO context_history (namespace, run_id, key, value, version, operation, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, namespace, runID, key, valueJSON, version+1, "delete", time.Now().UTC())

	if err != nil {
		return fmt.Errorf("failed to record context deletion history: %w", err)
	}

	return tx.Commit(ctx)
}

// GetContextHistory returns the version history for a context key.
func (b *Backend) GetContextHistory(ctx context.Context, namespace, key string, runID *string, cursor string, limit int) ([]*types.ContextHistoryEntry, string, error) {
	if limit <= 0 {
		limit = 20
	}

	var args []any
	query := `
		SELECT version, value, operation, created_at, updated_by
		FROM context_history
		WHERE namespace = $1 AND key = $2 AND COALESCE(run_id, '') = COALESCE($3, '')
	`
	args = append(args, namespace, key, runID)
	argNum := 4

	// Cursor is the version number
	if cursor != "" {
		query += fmt.Sprintf(" AND version < $%d", argNum)
		args = append(args, cursor)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY version DESC LIMIT $%d", argNum)
	args = append(args, limit+1)

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query context history: %w", err)
	}
	defer rows.Close()

	var entries []*types.ContextHistoryEntry
	for rows.Next() {
		entry := &types.ContextHistoryEntry{}
		var valueJSON []byte
		var updatedBy *string

		if err := rows.Scan(&entry.Version, &valueJSON, &entry.Operation, &entry.UpdatedAt, &updatedBy); err != nil {
			return nil, "", fmt.Errorf("failed to scan history entry: %w", err)
		}

		// Parse the value JSON
		if err := json.Unmarshal(valueJSON, &entry.Value); err != nil {
			return nil, "", fmt.Errorf("failed to unmarshal history value: %w", err)
		}

		if updatedBy != nil {
			entry.UpdatedBy = *updatedBy
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
	result, err := b.pool.Exec(ctx, `
		DELETE FROM context_entries
		WHERE expires_at IS NOT NULL AND expires_at < NOW()
	`)

	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired context: %w", err)
	}

	return result.RowsAffected(), nil
}

// CleanupRunContext removes all context entries for a specific run.
func (b *Backend) CleanupRunContext(ctx context.Context, namespace, runID string) error {
	_, err := b.pool.Exec(ctx, `
		DELETE FROM context_entries
		WHERE namespace = $1 AND run_id = $2
	`, namespace, runID)

	if err != nil {
		return fmt.Errorf("failed to cleanup run context: %w", err)
	}

	return nil
}
