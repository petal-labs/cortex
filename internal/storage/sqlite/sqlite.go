package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "github.com/mattn/go-sqlite3" // SQLite driver

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage"
)

// Backend implements storage.Backend using SQLite with the vec0 extension.
type Backend struct {
	db     *sql.DB
	dbPath string
	mu     sync.RWMutex // Protects concurrent access
}

// Verify Backend implements storage.Backend at compile time.
var _ storage.Backend = (*Backend)(nil)

// New creates a new SQLite storage backend.
func New(cfg *config.Config) (*Backend, error) {
	// Ensure data directory exists
	dataDir := cfg.Storage.DataDir
	if dataDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		dataDir = filepath.Join(homeDir, ".cortex", "data")
	}

	// Expand ~ in path
	if len(dataDir) > 0 && dataDir[0] == '~' {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to expand home directory: %w", err)
		}
		dataDir = filepath.Join(homeDir, dataDir[1:])
	}

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	dbPath := filepath.Join(dataDir, "cortex.db")

	// Open database with appropriate pragmas for performance
	// Using WAL mode for better concurrent read/write performance
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_cache_size=10000&_foreign_keys=ON", dbPath)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool settings
	// SQLite is single-writer, so we limit max connections
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Verify connection
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	backend := &Backend{
		db:     db,
		dbPath: dbPath,
	}

	return backend, nil
}

// NewWithDB creates a Backend with an existing database connection.
// Useful for testing with in-memory databases.
func NewWithDB(db *sql.DB) *Backend {
	return &Backend{
		db:     db,
		dbPath: ":memory:",
	}
}

// DB returns the underlying database connection.
// Use with caution - prefer using Backend methods.
func (b *Backend) DB() *sql.DB {
	return b.db
}

// Close closes the database connection.
func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.db != nil {
		return b.db.Close()
	}
	return nil
}

// Health checks the database connection.
func (b *Backend) Health(ctx context.Context) error {
	return b.db.PingContext(ctx)
}

// Migrate runs database migrations.
func (b *Backend) Migrate(ctx context.Context) error {
	return runMigrations(ctx, b.db)
}

// Transaction helper for executing operations in a transaction.
func (b *Backend) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("failed to rollback transaction: %v (original error: %w)", rbErr, err)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// Placeholder implementations - will be filled in by subsequent tasks
// Conversation operations are in conversation.go
// Knowledge operations are in knowledge.go

// Context Storage operations are in context.go
// Entity Storage operations are in entity.go
