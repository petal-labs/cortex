package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petal-labs/cortex/internal/config"
	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/server"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
)

var (
	mcpMode          bool
	allowedNamespace string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Cortex server",
	Long: `Start the Cortex memory service.

By default, starts in MCP mode using stdio transport for integration
with AI agents and tools.

Examples:
  # Start MCP server (default)
  cortex serve

  # Start MCP server with namespace restriction
  cortex serve --namespace my-workflow

  # Explicitly specify MCP mode
  cortex serve --mcp`,
	RunE: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().BoolVar(&mcpMode, "mcp", true, "Run in MCP mode (default)")
	serveCmd.Flags().StringVar(&allowedNamespace, "namespace", "", "Restrict to a single namespace")
}

func runServe(cmd *cobra.Command, args []string) error {
	// Load configuration
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Create storage backend
	store, err := sqlite.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	// Create embedding provider if Iris is configured
	var emb embedding.Provider
	if cfg.Iris.Endpoint != "" {
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

	// Start MCP server
	if mcpMode {
		mcpCfg := &server.Config{
			Name:             "cortex",
			Version:          "1.0.0",
			AllowedNamespace: allowedNamespace,
		}

		srv := server.New(mcpCfg, convEngine, knowEngine, ctxEngine, entityEngine)
		return srv.ServeStdio()
	}

	return fmt.Errorf("only MCP mode is currently supported")
}

// loadConfig loads configuration from file or defaults.
func loadConfig(configPath string) (*config.Config, error) {
	if configPath != "" {
		return config.Load(configPath)
	}

	// Try default locations
	homeDir, err := os.UserHomeDir()
	if err == nil {
		defaultPath := homeDir + "/.cortex/config.yaml"
		if _, err := os.Stat(defaultPath); err == nil {
			return config.Load(defaultPath)
		}
	}

	// Use default config
	return config.DefaultConfig(), nil
}
