package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
)

var namespaceCmd = &cobra.Command{
	Use:   "namespace",
	Short: "Manage namespaces",
	Long:  `Commands for managing namespaces and viewing namespace-level statistics.`,
}

var namespaceStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show statistics for a namespace",
	Long: `Display comprehensive statistics for a namespace including counts of threads, collections, entities, and context keys.

Examples:
  cortex namespace stats --namespace myapp`,
	RunE: runNamespaceStats,
}

var namespaceDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete all data in a namespace",
	Long: `Delete all data in a namespace. This is a destructive operation and cannot be undone.

Examples:
  cortex namespace delete --namespace myapp --confirm`,
	RunE: runNamespaceDelete,
}

func init() {
	rootCmd.AddCommand(namespaceCmd)

	// stats command
	namespaceCmd.AddCommand(namespaceStatsCmd)
	namespaceStatsCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	namespaceStatsCmd.MarkFlagRequired("namespace")

	// delete command
	namespaceCmd.AddCommand(namespaceDeleteCmd)
	namespaceDeleteCmd.Flags().StringP("namespace", "n", "", "Namespace to delete (required)")
	namespaceDeleteCmd.Flags().Bool("confirm", false, "Confirm deletion (required)")
	namespaceDeleteCmd.MarkFlagRequired("namespace")
	namespaceDeleteCmd.MarkFlagRequired("confirm")
}

func runNamespaceStats(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := sqlite.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	ctx := context.Background()

	fmt.Printf("Namespace: %s\n\n", namespace)

	// Conversation stats
	threads, _, err := store.ListThreads(ctx, namespace, "", 1000)
	if err != nil {
		return fmt.Errorf("failed to list threads: %w", err)
	}
	fmt.Printf("Conversations:\n")
	fmt.Printf("  Threads: %d\n", len(threads))

	// Knowledge stats
	collections, _, err := store.ListCollections(ctx, namespace, "", 1000)
	if err != nil {
		return fmt.Errorf("failed to list collections: %w", err)
	}
	totalDocs := int64(0)
	totalChunks := int64(0)
	for _, col := range collections {
		stats, err := store.CollectionStats(ctx, namespace, col.ID)
		if err == nil {
			totalDocs += stats.DocumentCount
			totalChunks += stats.ChunkCount
		}
	}
	fmt.Printf("\nKnowledge:\n")
	fmt.Printf("  Collections: %d\n", len(collections))
	fmt.Printf("  Documents: %d\n", totalDocs)
	fmt.Printf("  Chunks: %d\n", totalChunks)

	// Entity stats
	entities, _, err := store.ListEntities(ctx, namespace, storage.EntityListOpts{Limit: 10000})
	if err != nil {
		return fmt.Errorf("failed to list entities: %w", err)
	}
	fmt.Printf("\nEntities:\n")
	fmt.Printf("  Total: %d\n", len(entities))

	// Context stats
	keys, _, err := store.ListContextKeys(ctx, namespace, nil, nil, "", 10000)
	if err != nil {
		return fmt.Errorf("failed to list context keys: %w", err)
	}
	fmt.Printf("\nContext:\n")
	fmt.Printf("  Keys: %d\n", len(keys))

	return nil
}

func runNamespaceDelete(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := sqlite.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	confirm, _ := cmd.Flags().GetBool("confirm")

	if !confirm {
		return fmt.Errorf("must specify --confirm to delete namespace data")
	}

	ctx := context.Background()

	fmt.Printf("Deleting all data in namespace: %s\n", namespace)

	// Delete threads (and their messages)
	threads, _, err := store.ListThreads(ctx, namespace, "", 10000)
	if err != nil {
		return fmt.Errorf("failed to list threads: %w", err)
	}
	for _, t := range threads {
		if err := store.DeleteThread(ctx, namespace, t.ID); err != nil {
			fmt.Printf("  Warning: failed to delete thread %s: %v\n", t.ID, err)
		}
	}
	fmt.Printf("  Deleted %d threads\n", len(threads))

	// Delete collections (and their documents/chunks)
	collections, _, err := store.ListCollections(ctx, namespace, "", 1000)
	if err != nil {
		return fmt.Errorf("failed to list collections: %w", err)
	}
	for _, col := range collections {
		if err := store.DeleteCollection(ctx, namespace, col.ID); err != nil {
			fmt.Printf("  Warning: failed to delete collection %s: %v\n", col.ID, err)
		}
	}
	fmt.Printf("  Deleted %d collections\n", len(collections))

	// Delete entities
	entities, _, err := store.ListEntities(ctx, namespace, storage.EntityListOpts{Limit: 10000})
	if err != nil {
		return fmt.Errorf("failed to list entities: %w", err)
	}
	for _, e := range entities {
		if err := store.DeleteEntity(ctx, namespace, e.ID); err != nil {
			fmt.Printf("  Warning: failed to delete entity %s: %v\n", e.ID, err)
		}
	}
	fmt.Printf("  Deleted %d entities\n", len(entities))

	// Delete context keys
	keys, _, err := store.ListContextKeys(ctx, namespace, nil, nil, "", 10000)
	if err != nil {
		return fmt.Errorf("failed to list context keys: %w", err)
	}
	for _, key := range keys {
		if err := store.DeleteContext(ctx, namespace, key, nil); err != nil {
			fmt.Printf("  Warning: failed to delete context key %s: %v\n", key, err)
		}
	}
	fmt.Printf("  Deleted %d context keys\n", len(keys))

	fmt.Printf("\nNamespace %s has been cleared.\n", namespace)
	return nil
}
