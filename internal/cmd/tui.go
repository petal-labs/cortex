package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/tui"
)

func init() {
	rootCmd.AddCommand(tuiCmd)
}

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the terminal user interface",
	Long: `Launch an interactive terminal UI for browsing Cortex data.

The TUI provides a visual interface for exploring:
  - Dashboard overview with stats
  - Knowledge collections and documents
  - Conversation threads and messages
  - Entity explorer with relationships

Navigation:
  1-5: Switch sections
  ↑/↓ or j/k: Navigate lists
  Enter: Select/view details
  Esc: Go back
  r: Refresh data
  q: Quit`,
	RunE: runTUI,
}

func runTUI(cmd *cobra.Command, args []string) error {
	// Load configuration
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	if namespace == "" {
		namespace = "default"
	}

	// Create storage backend
	store, err := createStorage(cfg)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}
	defer store.Close()

	// Create embedding provider if configured
	var emb embedding.Provider
	if cfg.Embedding.Provider != "" {
		emb, err = embedding.NewIrisClient(cfg)
		if err != nil {
			return fmt.Errorf("failed to create embedding client: %w", err)
		}

		// Wrap with cache if configured
		if cfg.Embedding.CacheSize > 0 {
			emb, err = embedding.NewCachedProvider(emb, cfg.Embedding.CacheSize)
			if err != nil {
				return fmt.Errorf("failed to create embedding cache: %w", err)
			}
		}
	}

	// Create engines
	convEngine, err := conversation.NewEngine(store, emb, &cfg.Conversation)
	if err != nil {
		return fmt.Errorf("failed to create conversation engine: %w", err)
	}

	knowEngine, err := knowledge.NewEngine(store, emb, &cfg.Knowledge)
	if err != nil {
		return fmt.Errorf("failed to create knowledge engine: %w", err)
	}

	ctxEngine, err := ctxengine.NewEngine(store, &cfg.Context)
	if err != nil {
		return fmt.Errorf("failed to create context engine: %w", err)
	}

	entityEngine, err := entity.NewEngine(store, emb, &cfg.Entity)
	if err != nil {
		return fmt.Errorf("failed to create entity engine: %w", err)
	}

	// Set up context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Run TUI
	return tui.Run(ctx, store, knowEngine, convEngine, ctxEngine, entityEngine, &tui.Config{
		Namespace: namespace,
	})
}
