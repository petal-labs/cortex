package sqlite

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/petal-labs/cortex/v2/internal/storage/conformance"
)

// TestConformance runs the dual-backend conformance suite against an
// in-memory SQLite backend. This runs on every OS in CI (no Docker required).
func TestConformance(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// In-memory SQLite databases are per-connection; pin to 1 so concurrent
	// conformance tests exercise the same database instance.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	backend := NewWithDB(db)
	ctx := context.Background()
	if err := backend.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	defer backend.Close()

	conformance.RunSuite(t, backend, 384)
}
