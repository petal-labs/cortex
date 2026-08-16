package pgvector

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage"
)

// Backend implements storage.Backend using PostgreSQL with pgvector.
type Backend struct {
	pool *pgxpool.Pool
	cfg  *config.Config
}

// Historical pool sizing defaults, kept so unset deployments behave
// exactly as before the knobs existed.
const (
	defaultMaxConns = 25
	defaultMinConns = 5

	// defaultMaxConnsLimit is a hard ceiling on configured pool sizes,
	// far beyond any sane deployment and within int32 for pgxpool.
	defaultMaxConnsLimit = 1 << 20
)

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

	// Configure connection pool sizing.
	if err := applyPoolSizing(poolConfig, cfg); err != nil {
		return nil, err
	}

	// Ensure the pgvector extension exists and register vector types on every
	// pooled connection. RegisterTypes queries to_regtype('vector')::oid, which
	// returns NULL if the extension is missing, so CREATE EXTENSION must run
	// first. Extensions are per-database, so a single connection suffices.
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
			return fmt.Errorf("failed to create vector extension: %w", err)
		}
		if err := pgxvec.RegisterTypes(ctx, conn); err != nil {
			return fmt.Errorf("failed to register pgvector types: %w", err)
		}
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

// applyPoolSizing resolves effective pool sizes onto poolConfig with
// precedence per parameter: explicit config (storage.pool_max_conns /
// storage.pool_min_conns, > 0) > database URL query params
// (pool_max_conns / pool_min_conns, already applied by ParseConfig) >
// historical defaults. This lets operators tune the pool either way;
// previously hardcoded values silently stomped both.
func applyPoolSizing(poolConfig *pgxpool.Config, cfg *config.Config) error {
	urlQuery := url.Values{}
	if u, err := url.Parse(cfg.Storage.DatabaseURL); err == nil {
		urlQuery = u.Query()
	}

	// Config validation bounds these to sane values; the guards keep the
	// int->int32 conversions overflow-safe regardless of construction path.
	if v := cfg.Storage.PoolMaxConns; v > 0 {
		if v > defaultMaxConnsLimit {
			v = defaultMaxConnsLimit
		}
		poolConfig.MaxConns = int32(v)
	} else if !urlQuery.Has("pool_max_conns") {
		poolConfig.MaxConns = defaultMaxConns
	}

	if v := cfg.Storage.PoolMinConns; v > 0 {
		if v > defaultMaxConnsLimit {
			v = defaultMaxConnsLimit
		}
		poolConfig.MinConns = int32(v)
	} else if !urlQuery.Has("pool_min_conns") {
		poolConfig.MinConns = defaultMinConns
	}

	if poolConfig.MinConns > poolConfig.MaxConns {
		return fmt.Errorf("invalid pool sizing: pool_min_conns (%d) exceeds pool_max_conns (%d) (check storage.pool_* config and database URL params)",
			poolConfig.MinConns, poolConfig.MaxConns)
	}
	return nil
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

// isUniqueConstraintError checks if the error is a unique constraint violation.
// It inspects the typed *pgconn.PgError rather than matching error text, so it
// is unaffected by localized or reworded driver messages.
func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// PostgreSQL unique constraint violation code is 23505.
		return pgErr.Code == "23505"
	}
	return false
}
