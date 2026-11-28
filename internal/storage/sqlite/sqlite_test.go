package sqlite

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/petal-labs/cortex/v2/internal/config"
	"github.com/petal-labs/cortex/v2/pkg/types"
)

func TestWALReadWritePoolSplit(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Storage.Backend = "sqlite"
	cfg.Storage.DataDir = t.TempDir()

	backend, err := New(cfg)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer backend.Close()

	if backend.ReadDB() == backend.DB() {
		t.Fatal("expected file-backed SQLite backend to maintain a separate read pool")
	}

	ctx := context.Background()
	if err := backend.Health(ctx); err != nil {
		t.Fatalf("Health() failed: %v", err)
	}

	// Read pool must reject writes (query_only / mode=ro).
	if _, err := backend.ReadDB().ExecContext(ctx,
		`INSERT INTO cortex_metadata (key, value) VALUES ('should_fail', '1')`,
	); err == nil {
		t.Fatal("expected write on read pool to fail")
	}

	// Seed an entity on the writer handle.
	ent := &types.Entity{
		ID:        "ent-wal-1",
		Namespace: "wal-test",
		Name:      "PaymentsDB",
		Type:      types.EntityTypeProduct,
		Summary:   "Primary transactional database",
	}
	if err := backend.UpsertEntity(ctx, ent); err != nil {
		t.Fatalf("UpsertEntity failed: %v", err)
	}

	// Hold an open write transaction on the writer handle while running
	// concurrent reads on the read pool. Without a separate WAL read pool,
	// these reads would deadlock behind MaxOpenConns(1) until the tx commits.
	tx, err := backend.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx failed: %v", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO entities (id, namespace, name, type, first_seen_at, last_seen_at) VALUES ('ent-wal-tx', 'wal-test', 'Uncommitted', 'concept', 1, 1)`,
	); err != nil {
		tx.Rollback()
		t.Fatalf("insert in tx failed: %v", err)
	}

	const readers = 8
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := backend.GetEntityByID(ctx, "wal-test", "ent-wal-1")
			if err != nil {
				errs <- err
				return
			}
			if got.Name != "PaymentsDB" {
				errs <- sql.ErrNoRows
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		tx.Rollback()
		t.Fatalf("concurrent read during active write tx failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	got, err := backend.GetEntityByID(ctx, "wal-test", "ent-wal-tx")
	if err != nil {
		t.Fatalf("expected committed entity to be readable after commit: %v", err)
	}
	if got.Name != "Uncommitted" {
		t.Fatalf("got name %q, want %q", got.Name, "Uncommitted")
	}
}

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

	// Verify schema version (4 includes graph_rag_variant_b)
	version, err := GetSchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("failed to get schema version: %v", err)
	}
	if version != 4 {
		t.Errorf("expected schema version 4, got %d", version)
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

	// Verify schema version is still 4 (graph_rag_variant_b)
	version, err := GetSchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("failed to get schema version: %v", err)
	}
	if version != 4 {
		t.Errorf("expected schema version 4, got %d", version)
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
