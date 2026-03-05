package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/gc"
	"github.com/petal-labs/cortex/internal/observability"
)

var (
	gcAll              bool
	gcExpiredTTL       bool
	gcOrphanedChunks   bool
	gcOldConversations bool
	gcStaleEntities    bool
	gcContextHistory   bool
	gcRunContext       bool
	gcDryRun           bool
)

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Run garbage collection",
	Long: `Run garbage collection to clean up old data.

By default, runs all GC tasks based on the configured retention policies.
Use flags to run specific GC tasks.

Examples:
  # Run all GC tasks
  cortex gc --all

  # Clean up expired TTL context entries only
  cortex gc --expired-ttl

  # Clean up orphaned chunks only
  cortex gc --orphaned-chunks

  # Multiple specific tasks
  cortex gc --expired-ttl --orphaned-chunks

  # Dry run to see what would be deleted
  cortex gc --all --dry-run`,
	RunE: runGC,
}

func init() {
	rootCmd.AddCommand(gcCmd)

	gcCmd.Flags().BoolVar(&gcAll, "all", false, "Run all GC tasks")
	gcCmd.Flags().BoolVar(&gcExpiredTTL, "expired-ttl", false, "Clean up expired TTL context entries")
	gcCmd.Flags().BoolVar(&gcOrphanedChunks, "orphaned-chunks", false, "Delete orphaned chunks")
	gcCmd.Flags().BoolVar(&gcOldConversations, "old-conversations", false, "Delete old conversations")
	gcCmd.Flags().BoolVar(&gcStaleEntities, "stale-entities", false, "Prune stale entities")
	gcCmd.Flags().BoolVar(&gcContextHistory, "context-history", false, "Clean up old context history")
	gcCmd.Flags().BoolVar(&gcRunContext, "run-context", false, "Clean up old run-scoped context")
	gcCmd.Flags().BoolVar(&gcDryRun, "dry-run", false, "Show what would be deleted without actually deleting")
}

func runGC(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize logging for CLI
	_ = observability.InitLogger(cfg.Server.LogLevel, false)
	defer observability.DefaultLogger.Sync()

	store, err := createStorage(cfg)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}
	defer store.Close()

	// Run migrations
	ctx := context.Background()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	collector := gc.NewCollector(store, cfg)

	// If no specific flags, default to --all
	noSpecificFlags := !gcExpiredTTL && !gcOrphanedChunks && !gcOldConversations &&
		!gcStaleEntities && !gcContextHistory && !gcRunContext
	if gcAll || noSpecificFlags {
		return runAllGC(ctx, collector, cfg)
	}

	// Run specific tasks
	return runSpecificGC(ctx, collector, cfg)
}

func runAllGC(ctx context.Context, collector *gc.Collector, cfg *config.Config) error {
	fmt.Println("Running all garbage collection tasks...")

	if gcDryRun {
		fmt.Println("(dry-run mode - no changes will be made)")
		printGCPolicies(cfg)
		return nil
	}

	start := time.Now()
	results := collector.RunAll(ctx)

	fmt.Println("\nGarbage collection results:")
	fmt.Printf("  Expired TTL entries:      %d deleted\n", results.ExpiredTTL)
	fmt.Printf("  Old conversations:        %d deleted\n", results.OldConversations)
	fmt.Printf("  Stale entities:           %d pruned\n", results.StaleEntities)
	fmt.Printf("  Orphaned chunks:          %d deleted\n", results.OrphanedChunks)
	fmt.Printf("  Old context history:      %d deleted\n", results.OldContextHistory)
	fmt.Printf("  Old run-scoped context:   %d deleted\n", results.OldRunContext)
	fmt.Printf("\nCompleted in %v\n", time.Since(start).Round(time.Millisecond))

	total := results.ExpiredTTL + results.OldConversations + results.StaleEntities +
		results.OrphanedChunks + results.OldContextHistory + results.OldRunContext
	if total == 0 {
		fmt.Println("No data needed cleanup.")
	}

	return nil
}

func runSpecificGC(ctx context.Context, collector *gc.Collector, cfg *config.Config) error {
	if gcDryRun {
		fmt.Println("(dry-run mode - no changes will be made)")
		printGCPolicies(cfg)
		return nil
	}

	start := time.Now()
	var totalDeleted int64

	if gcExpiredTTL {
		fmt.Print("Cleaning up expired TTL entries... ")
		deleted := collector.RunTTLCleanup(ctx)
		fmt.Printf("%d deleted\n", deleted)
		totalDeleted += deleted
	}

	if gcOldConversations {
		if cfg.Retention.ConversationRetentionDays <= 0 {
			fmt.Println("Skipping old conversations (retention disabled)")
		} else {
			fmt.Printf("Deleting conversations older than %d days... ", cfg.Retention.ConversationRetentionDays)
			deleted := collector.RunConversationCleanup(ctx)
			fmt.Printf("%d deleted\n", deleted)
			totalDeleted += deleted
		}
	}

	if gcStaleEntities {
		if cfg.Retention.EntityStaleDays <= 0 {
			fmt.Println("Skipping stale entities (pruning disabled)")
		} else {
			fmt.Printf("Pruning entities stale for %d days with < %d mentions... ",
				cfg.Retention.EntityStaleDays, cfg.Entity.MinMentionsToKeep)
			deleted := collector.RunEntityPruning(ctx)
			fmt.Printf("%d pruned\n", deleted)
			totalDeleted += deleted
		}
	}

	if gcOrphanedChunks {
		fmt.Print("Deleting orphaned chunks... ")
		deleted := collector.RunOrphanedChunkCleanup(ctx)
		fmt.Printf("%d deleted\n", deleted)
		totalDeleted += deleted
	}

	if gcContextHistory {
		if cfg.Context.HistoryRetentionDays <= 0 {
			fmt.Println("Skipping context history (retention disabled)")
		} else {
			fmt.Printf("Cleaning context history older than %d days... ", cfg.Context.HistoryRetentionDays)
			deleted := collector.RunContextHistoryCleanup(ctx)
			fmt.Printf("%d deleted\n", deleted)
			totalDeleted += deleted
		}
	}

	if gcRunContext {
		if cfg.Retention.ContextRunRetentionDays <= 0 {
			fmt.Println("Skipping run-scoped context (retention disabled)")
		} else {
			fmt.Printf("Cleaning run-scoped context older than %d days... ", cfg.Retention.ContextRunRetentionDays)
			deleted := collector.RunRunContextCleanup(ctx)
			fmt.Printf("%d deleted\n", deleted)
			totalDeleted += deleted
		}
	}

	fmt.Printf("\nCompleted in %v, %d total items cleaned up\n",
		time.Since(start).Round(time.Millisecond), totalDeleted)

	return nil
}

func printGCPolicies(cfg *config.Config) {
	fmt.Println("\nConfigured retention policies:")
	fmt.Printf("  Conversation retention:     %d days (0 = disabled)\n", cfg.Retention.ConversationRetentionDays)
	fmt.Printf("  Entity stale threshold:     %d days (0 = disabled)\n", cfg.Retention.EntityStaleDays)
	fmt.Printf("  Context run retention:      %d days\n", cfg.Retention.ContextRunRetentionDays)
	fmt.Printf("  Context history retention:  %d days\n", cfg.Context.HistoryRetentionDays)
	fmt.Printf("  TTL cleanup interval:       %v\n", cfg.Context.TTLCleanupInterval)
	fmt.Printf("  GC interval:                %v\n", cfg.Retention.GCInterval)
}
