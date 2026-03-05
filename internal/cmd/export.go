package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
)

var (
	exportNamespace     string
	exportOutput        string
	exportNoEmbeddings  bool
	exportConversations bool
	exportKnowledge     bool
	exportContext       bool
	exportEntities      bool
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export namespace data to JSON",
	Long: `Export all data from a namespace to a JSON file.

This is useful for:
  - Migrating data between backends (SQLite to PostgreSQL)
  - Creating portable backups
  - Auditing namespace contents

Examples:
  # Export all data from a namespace
  cortex export --namespace my-project --output export.json

  # Export without embeddings (smaller file)
  cortex export --namespace my-project --output export.json --no-embeddings

  # Export only conversations
  cortex export --namespace my-project --output export.json --conversations-only

  # Export only knowledge store
  cortex export --namespace my-project --output export.json --knowledge-only`,
	RunE: runExport,
}

func init() {
	rootCmd.AddCommand(exportCmd)

	exportCmd.Flags().StringVarP(&exportNamespace, "namespace", "n", "", "Namespace to export (required)")
	exportCmd.Flags().StringVarP(&exportOutput, "output", "o", "", "Output file path (required)")
	exportCmd.Flags().BoolVar(&exportNoEmbeddings, "no-embeddings", false, "Exclude embeddings (smaller file)")
	exportCmd.Flags().BoolVar(&exportConversations, "conversations-only", false, "Export only conversations")
	exportCmd.Flags().BoolVar(&exportKnowledge, "knowledge-only", false, "Export only knowledge store")
	exportCmd.Flags().BoolVar(&exportContext, "context-only", false, "Export only context")
	exportCmd.Flags().BoolVar(&exportEntities, "entities-only", false, "Export only entities")

	exportCmd.MarkFlagRequired("namespace")
	exportCmd.MarkFlagRequired("output")
}

func runExport(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Determine export options
	opts := storage.ExportOptions{
		IncludeEmbeddings: !exportNoEmbeddings,
	}

	// If any specific flag is set, only export that
	specificFlagSet := exportConversations || exportKnowledge || exportContext || exportEntities
	if specificFlagSet {
		opts.IncludeConversations = exportConversations
		opts.IncludeKnowledge = exportKnowledge
		opts.IncludeContext = exportContext
		opts.IncludeEntities = exportEntities
	} else {
		// Export everything
		opts.IncludeConversations = true
		opts.IncludeKnowledge = true
		opts.IncludeContext = true
		opts.IncludeEntities = true
	}

	// Currently only SQLite export is implemented
	backend := cfg.Storage.Backend
	if backend == "" {
		backend = "sqlite"
	}

	switch backend {
	case "sqlite", "":
		return runSQLiteExport(cfg, exportNamespace, exportOutput, opts)
	case "pgvector", "postgres", "postgresql":
		return fmt.Errorf("PostgreSQL export not yet implemented; use pg_dump for full database export")
	default:
		return fmt.Errorf("unknown backend: %s", backend)
	}
}

func runSQLiteExport(cfg *config.Config, namespace, output string, opts storage.ExportOptions) error {
	store, err := sqlite.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer store.Close()

	ctx := context.Background()

	fmt.Printf("Exporting namespace '%s'...\n", namespace)

	data, err := store.Export(ctx, namespace, opts)
	if err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	// Marshal to JSON with indentation for readability
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	// Write to file
	if err := os.WriteFile(output, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Print summary
	fmt.Printf("\nExport completed:\n")
	fmt.Printf("  Threads:       %d\n", len(data.Threads))
	fmt.Printf("  Messages:      %d\n", len(data.Messages))
	fmt.Printf("  Collections:   %d\n", len(data.Collections))
	fmt.Printf("  Documents:     %d\n", len(data.Documents))
	fmt.Printf("  Chunks:        %d\n", len(data.Chunks))
	fmt.Printf("  Context:       %d entries\n", len(data.ContextEntries))
	fmt.Printf("  Entities:      %d\n", len(data.Entities))
	fmt.Printf("  Relationships: %d\n", len(data.Relationships))
	fmt.Printf("  Mentions:      %d\n", len(data.Mentions))
	fmt.Printf("\nSaved to: %s (%s)\n", output, formatBytes(int64(len(jsonData))))

	return nil
}
