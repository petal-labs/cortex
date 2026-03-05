package pgvector

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage"
)

// Backend implements storage.Backend using PostgreSQL with pgvector.
type Backend struct {
	pool *pgxpool.Pool
	cfg  *config.Config
}

// Ensure Backend implements storage.Backend.
var _ storage.Backend = (*Backend)(nil)

// New creates a new pgvector backend with connection pooling.
func New(cfg *config.Config) (*Backend, error) {
	if cfg.Storage.DatabaseURL == "" {
		return nil, fmt.Errorf("database_url is required for pgvector backend")
	}

	poolConfig, err := pgxpool.ParseConfig(cfg.Storage.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	// Configure connection pool
	poolConfig.MaxConns = 25
	poolConfig.MinConns = 5

	// Register pgvector type
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		// pgvector-go automatically registers types when using the pgx driver
		return nil
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Verify connection
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	b := &Backend{
		pool: pool,
		cfg:  cfg,
	}

	// Run migrations
	if err := b.Migrate(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return b, nil
}

// Close releases the connection pool.
func (b *Backend) Close() error {
	b.pool.Close()
	return nil
}

// Health checks the database connection.
func (b *Backend) Health(ctx context.Context) error {
	return b.pool.Ping(ctx)
}

// Migrate runs schema migrations.
func (b *Backend) Migrate(ctx context.Context) error {
	return b.runMigrations(ctx)
}

// Helper to convert []float32 to pgvector.Vector
func toVector(embedding []float32) pgvector.Vector {
	return pgvector.NewVector(embedding)
}

// Helper to convert pgvector.Vector to []float32
func fromVector(v pgvector.Vector) []float32 {
	return v.Slice()
}

// isUniqueConstraintError checks if the error is a unique constraint violation.
func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	// PostgreSQL unique constraint violation code is 23505
	errStr := err.Error()
	return contains(errStr, "23505") || contains(errStr, "unique constraint") || contains(errStr, "duplicate key")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsHelper(s, substr)
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
