package sqlite

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestNewBackend(t *testing.T) {
	// Use temp directory
	tmpDir := t.TempDir()

	cfg := &testConfig{
		dataDir: tmpDir,
	}

	backend, err := newTestBackend(cfg)
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	defer backend.Close()

	// Verify health check works
	if err := backend.Health(context.Background()); err != nil {
		t.Errorf("health check failed: %v", err)
	}
}

func TestMigrations(t *testing.T) {
	// Create in-memory database
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Run migrations
	if err := runMigrations(ctx, db); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// Verify schema version (2 is current with FTS5 migration)
	version, err := GetSchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("failed to get schema version: %v", err)
	}
	if version != 2 {
		t.Errorf("expected schema version 2, got %d", version)
	}

	// Verify tables exist
	tables := []string{
		"threads", "messages", "message_embeddings",
		"collections", "documents", "chunks", "chunk_embeddings",
		"context_entries", "context_history",
		"entities", "entity_aliases", "entity_embeddings",
		"entity_mentions", "entity_relationships", "entity_extraction_queue",
	}

	for _, table := range tables {
		var count int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		if err != nil {
			t.Errorf("failed to check table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("expected table %s to exist", table)
		}
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	// Create in-memory database
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Run migrations twice - should not error
	if err := runMigrations(ctx, db); err != nil {
		t.Fatalf("first migration failed: %v", err)
	}

	if err := runMigrations(ctx, db); err != nil {
		t.Fatalf("second migration failed: %v", err)
	}

	// Verify schema version is still 2 (current with FTS5)
	version, err := GetSchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("failed to get schema version: %v", err)
	}
	if version != 2 {
		t.Errorf("expected schema version 2, got %d", version)
	}
}

func TestForeignKeyConstraints(t *testing.T) {
	// Create in-memory database with foreign keys enabled
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Run migrations
	if err := runMigrations(ctx, db); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// Verify foreign keys are enabled
	var fkEnabled int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fkEnabled); err != nil {
		t.Fatalf("failed to check foreign keys: %v", err)
	}
	if fkEnabled != 1 {
		t.Error("expected foreign keys to be enabled")
	}

	// Try to insert a message without a thread - should fail
	_, err = db.ExecContext(ctx, `
		INSERT INTO messages (id, namespace, thread_id, role, content, created_at)
		VALUES ('msg1', 'test', 'nonexistent_thread', 'user', 'hello', strftime('%s', 'now'))
	`)
	if err == nil {
		t.Error("expected foreign key violation for message without thread")
	}
}

// testConfig implements a minimal config for testing
type testConfig struct {
	dataDir string
}

func newTestBackend(cfg *testConfig) (*Backend, error) {
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=ON")
	if err != nil {
		return nil, err
	}

	backend := &Backend{
		db:     db,
		dbPath: ":memory:",
	}

	if err := backend.Migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}

	return backend, nil
}

func newTestBackendWithDB(t *testing.T) *Backend {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	backend := &Backend{
		db:     db,
		dbPath: ":memory:",
	}

	if err := backend.Migrate(context.Background()); err != nil {
		db.Close()
		t.Fatalf("failed to run migrations: %v", err)
	}

	return backend
}
