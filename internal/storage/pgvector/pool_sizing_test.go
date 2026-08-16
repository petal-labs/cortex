package pgvector

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petal-labs/cortex/internal/config"
)

// parsePoolConfig parses a database URL into a pgxpool config without
// connecting; ParseConfig applies any pool_* query params itself, exactly
// as production does.
func parsePoolConfig(t *testing.T, dbURL string) *pgxpool.Config {
	t.Helper()
	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("failed to parse database URL: %v", err)
	}
	return poolConfig
}

func storageCfg(dbURL string, maxConns, minConns int) *config.Config {
	return &config.Config{
		Storage: config.StorageConfig{
			Backend:      "pgvector",
			DatabaseURL:  dbURL,
			PoolMaxConns: maxConns,
			PoolMinConns: minConns,
		},
	}
}

func TestApplyPoolSizing(t *testing.T) {
	t.Run("defaults when nothing set", func(t *testing.T) {
		pc := parsePoolConfig(t, "postgres://u:p@localhost:5432/db")
		if err := applyPoolSizing(pc, storageCfg("postgres://u:p@localhost:5432/db", 0, 0)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pc.MaxConns != 25 || pc.MinConns != 5 {
			t.Errorf("expected historical defaults 25/5, got %d/%d", pc.MaxConns, pc.MinConns)
		}
	})

	t.Run("URL params honored over defaults", func(t *testing.T) {
		dbURL := "postgres://u:p@localhost:5432/db?pool_max_conns=7&pool_min_conns=3"
		pc := parsePoolConfig(t, dbURL)
		if err := applyPoolSizing(pc, storageCfg(dbURL, 0, 0)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pc.MaxConns != 7 || pc.MinConns != 3 {
			t.Errorf("expected URL params 7/3 preserved, got %d/%d", pc.MaxConns, pc.MinConns)
		}
	})

	t.Run("config overrides URL params", func(t *testing.T) {
		dbURL := "postgres://u:p@localhost:5432/db?pool_max_conns=7&pool_min_conns=3"
		pc := parsePoolConfig(t, dbURL)
		if err := applyPoolSizing(pc, storageCfg(dbURL, 40, 10)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pc.MaxConns != 40 || pc.MinConns != 10 {
			t.Errorf("expected config 40/10 to win, got %d/%d", pc.MaxConns, pc.MinConns)
		}
	})

	t.Run("config sets only max; URL min preserved", func(t *testing.T) {
		dbURL := "postgres://u:p@localhost:5432/db?pool_min_conns=3"
		pc := parsePoolConfig(t, dbURL)
		if err := applyPoolSizing(pc, storageCfg(dbURL, 40, 0)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pc.MaxConns != 40 || pc.MinConns != 3 {
			t.Errorf("expected 40 from config and 3 from URL, got %d/%d", pc.MaxConns, pc.MinConns)
		}
	})

	t.Run("zero config means unset, not zero conns", func(t *testing.T) {
		dbURL := "postgres://u:p@localhost:5432/db"
		pc := parsePoolConfig(t, dbURL)
		if err := applyPoolSizing(pc, storageCfg(dbURL, 0, 0)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pc.MaxConns == 0 || pc.MinConns == 0 {
			t.Errorf("zero config must not produce zero conns, got %d/%d", pc.MaxConns, pc.MinConns)
		}
	})

	t.Run("effective min exceeding max rejected", func(t *testing.T) {
		dbURL := "postgres://u:p@localhost:5432/db?pool_min_conns=5"
		pc := parsePoolConfig(t, dbURL)
		err := applyPoolSizing(pc, storageCfg(dbURL, 2, 0))
		if err == nil {
			t.Fatal("expected error for min > max")
		}
		if !strings.Contains(err.Error(), "pool_min_conns (5) exceeds pool_max_conns (2)") {
			t.Errorf("expected actionable message, got: %v", err)
		}
	})

	t.Run("config min exceeding config max rejected", func(t *testing.T) {
		dbURL := "postgres://u:p@localhost:5432/db"
		pc := parsePoolConfig(t, dbURL)
		err := applyPoolSizing(pc, storageCfg(dbURL, 4, 9))
		if err == nil {
			t.Fatal("expected error for config min > max")
		}
	})
}
