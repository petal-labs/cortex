package pgvector

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage/conformance"
)

// TestConformance runs the dual-backend conformance suite against a real
// Postgres+pgvector instance via testcontainers. This requires Docker and is
// skipped when Docker is unavailable or when running in short mode.
func TestConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pgvector conformance in short mode")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("cortex_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("failed to start pgvector container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			t.Errorf("failed to terminate container: %v", err)
		}
	})

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	// Create the vector extension before constructing the backend. The pool's
	// AfterConnect hook runs CREATE EXTENSION on every connection (MinConns=5),
	// which races and fails with a duplicate-key error when multiple connections
	// execute it simultaneously. Pre-creating it makes the IF NOT EXISTS a true
	// no-op.
	initConn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("failed to connect for extension setup: %v", err)
	}
	if _, err := initConn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		initConn.Close(ctx)
		t.Fatalf("failed to create vector extension: %v", err)
	}
	initConn.Close(ctx)

	cfg := &config.Config{
		Storage: config.StorageConfig{
			Backend:     "pgvector",
			DatabaseURL: connStr,
		},
		Embedding: config.EmbeddingConfig{
			Dimensions: 384,
		},
	}

	backend, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create pgvector backend: %v", err)
	}
	defer backend.Close()

	conformance.RunSuite(t, backend, 384)
}
