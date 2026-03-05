package cmd

import (
	"fmt"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/internal/storage/pgvector"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
)

// createStorage creates a storage backend based on configuration.
// Supported backends: "sqlite" (default), "pgvector".
func createStorage(cfg *config.Config) (storage.Backend, error) {
	switch cfg.Storage.Backend {
	case "sqlite", "":
		return sqlite.New(cfg)

	case "pgvector", "postgres", "postgresql":
		if cfg.Storage.DatabaseURL == "" {
			return nil, fmt.Errorf("database_url is required for pgvector backend")
		}
		return pgvector.New(cfg)

	default:
		return nil, fmt.Errorf("unknown storage backend: %q (supported: sqlite, pgvector)", cfg.Storage.Backend)
	}
}
