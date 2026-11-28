package cmd

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/petal-labs/cortex/v2/internal/config"
	"github.com/petal-labs/cortex/v2/internal/storage"
	"github.com/petal-labs/cortex/v2/internal/storage/sqlite"
	"github.com/petal-labs/cortex/v2/pkg/types"
)

func TestRunSQLiteExportFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = tmpDir

	store, err := sqlite.New(cfg)
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := store.AppendMessage(context.Background(), &types.Message{
		Namespace: "perm-ns",
		ThreadID:  "t1",
		Role:      "user",
		Content:   "secret message",
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	_ = store.Close()

	outPath := filepath.Join(tmpDir, "export.json")
	if err := runSQLiteExport(cfg, "perm-ns", outPath, storage.DefaultExportOptions()); err != nil {
		t.Fatalf("runSQLiteExport: %v", err)
	}

	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("os.Stat(%s): %v", outPath, err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("expected export file permissions 0600, got %04o", perm)
		}
	}
}
