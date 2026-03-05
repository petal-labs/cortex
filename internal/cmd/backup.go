package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
)

var (
	backupOutput string
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Create a backup of the database",
	Long: `Create a consistent backup of the Cortex database.

For SQLite, this uses the online backup API for hot backups without downtime.
For PostgreSQL, use standard pg_dump instead.

Examples:
  # Backup to a specific file
  cortex backup --output /path/to/backup.db

  # Backup with auto-generated filename (includes timestamp)
  cortex backup`,
	RunE: runBackup,
}

func init() {
	rootCmd.AddCommand(backupCmd)
	backupCmd.Flags().StringVarP(&backupOutput, "output", "o", "", "Output file path (default: cortex-backup-TIMESTAMP.db)")
}

func runBackup(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Check backend type
	backend := cfg.Storage.Backend
	if backend == "" {
		backend = "sqlite"
	}

	switch backend {
	case "sqlite", "":
		return runSQLiteBackup(cfg)
	case "pgvector", "postgres", "postgresql":
		fmt.Println("PostgreSQL backup is not managed by Cortex.")
		fmt.Println("Use standard PostgreSQL backup tools:")
		fmt.Println("  pg_dump -Fc your_database > backup.dump")
		fmt.Println("  pg_dump your_database > backup.sql")
		return nil
	default:
		return fmt.Errorf("unknown backend: %s", backend)
	}
}

func runSQLiteBackup(cfg *config.Config) error {
	// Determine output path
	outputPath := backupOutput
	if outputPath == "" {
		timestamp := time.Now().Format("20060102-150405")
		outputPath = fmt.Sprintf("cortex-backup-%s.db", timestamp)
	}

	// Make output path absolute
	absPath, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("invalid output path: %w", err)
	}

	// Ensure output directory exists
	outputDir := filepath.Dir(absPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Open the source database
	store, err := sqlite.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer store.Close()

	fmt.Printf("Backing up database to: %s\n", absPath)

	ctx := context.Background()
	start := time.Now()

	// Run backup with progress reporting
	err = store.BackupWithProgress(ctx, absPath, func(progress sqlite.BackupProgress) {
		if progress.Total > 0 {
			pct := float64(progress.Total-progress.Remaining) / float64(progress.Total) * 100
			fmt.Printf("\rProgress: %.1f%% (%d/%d pages)", pct, progress.Total-progress.Remaining, progress.Total)
		}
	})
	fmt.Println() // New line after progress

	if err != nil {
		// Clean up partial backup on failure
		os.Remove(absPath)
		return fmt.Errorf("backup failed: %w", err)
	}

	// Get file size
	info, err := os.Stat(absPath)
	if err == nil {
		fmt.Printf("Backup completed in %v\n", time.Since(start).Round(time.Millisecond))
		fmt.Printf("Backup size: %s\n", formatBytes(info.Size()))
	}

	fmt.Printf("Backup saved to: %s\n", absPath)
	return nil
}

func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}
