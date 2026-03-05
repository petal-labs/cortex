package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/petal-labs/cortex/internal/config"
	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/gc"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/observability"
	"github.com/petal-labs/cortex/internal/server"
	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/internal/summarization"
)

var (
	mcpMode          bool
	allowedNamespace string
	transport        string
	port             int
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Cortex server",
	Long: `Start the Cortex memory service.

By default, starts in MCP mode using stdio transport for integration
with AI agents and tools.

Transport modes:
  stdio - Standard input/output (default, for process-based MCP clients)
  sse   - Server-Sent Events over HTTP (for web-based MCP clients)

Examples:
  # Start MCP server with stdio transport (default)
  cortex serve

  # Start MCP server with SSE transport on port 9810
  cortex serve --transport sse --port 9810

  # Start MCP server with namespace restriction
  cortex serve --namespace my-workflow

  # SSE server with namespace restriction
  cortex serve --transport sse --port 9810 --namespace my-workflow`,
	RunE: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().BoolVar(&mcpMode, "mcp", true, "Run in MCP mode (default)")
	serveCmd.Flags().StringVar(&allowedNamespace, "namespace", "", "Restrict to a single namespace")
	serveCmd.Flags().StringVar(&transport, "transport", "stdio", "Transport mode: stdio or sse")
	serveCmd.Flags().IntVar(&port, "port", 9810, "Port for SSE transport (default 9810)")
}

func runServe(cmd *cobra.Command, args []string) error {
	// Load configuration
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize structured logging
	if err := observability.InitLogger(cfg.Server.LogLevel, cfg.Server.StructuredLogging); err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	defer observability.DefaultLogger.Sync()

	// Create storage backend based on configuration
	store, err := createStorage(cfg)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	// Initialize metrics if enabled
	var metricsServer *observability.MetricsServer
	if cfg.Server.MetricsEnabled {
		// Create queue stats provider
		queueStats := &queueStatsAdapter{store: store}
		observability.InitMetrics(queueStats)

		// Start metrics server
		metricsServer = observability.NewMetricsServer(cfg.Server.MetricsPort)
		metricsServer.Start()
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

	// Set up summarization if Iris is configured
	if cfg.Iris.Endpoint != "" && cfg.Summarization.Model != "" {
		summClient := summarization.NewClient(cfg)
		convEngine.SetSummarizer(summClient)
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

	// Set up entity extraction pipeline if Iris is configured and extraction is enabled
	var queueProcessor *entity.QueueProcessor
	if cfg.Iris.Endpoint != "" && cfg.Entity.ExtractionMode != "off" {
		// Create extractor (uses Iris completion API)
		extractor := entity.NewExtractor(cfg)

		// Create name resolver with configurable fuzzy threshold
		resolver := entity.NewResolver(store, cfg.Entity.AliasFuzzyThreshold)

		// Create queue processor
		queueProcessor = entity.NewQueueProcessor(store, extractor, resolver, entityEngine, &cfg.Entity)

		// Create enqueuer adapter and set on engines
		enqueuer := entity.NewExtractionEnqueuerAdapter(store)
		convEngine.SetExtractionEnqueuer(enqueuer)
		knowEngine.SetExtractionEnqueuer(enqueuer)
	}

	// Start garbage collector in background
	gcCollector := gc.NewCollector(store, cfg)
	gcCollector.Start()
	defer gcCollector.Stop()

	// Start MCP server
	if mcpMode {
		mcpCfg := &server.Config{
			Name:             "cortex",
			Version:          "1.0.0",
			AllowedNamespace: allowedNamespace,
		}

		srv := server.New(mcpCfg, convEngine, knowEngine, ctxEngine, entityEngine)

		// Start queue processor in background if configured
		if queueProcessor != nil {
			ctx := cmd.Context()
			queueProcessor.Start(ctx)
		}

		// Select transport mode
		switch transport {
		case "stdio", "":
			observability.Info(context.Background(), "starting MCP server with stdio transport")
			return srv.ServeStdio()

		case "sse":
			addr := fmt.Sprintf(":%d", port)
			observability.Info(context.Background(), "starting MCP server with SSE transport",
				observability.Field("addr", addr),
				observability.Field("sse_endpoint", fmt.Sprintf("/sse")),
				observability.Field("message_endpoint", fmt.Sprintf("/message")),
			)
			// Also print to stdout for non-structured logging
			if !cfg.Server.StructuredLogging {
				fmt.Printf("Starting Cortex MCP server with SSE transport on %s\n", addr)
				fmt.Printf("  SSE endpoint: http://localhost:%d/sse\n", port)
				fmt.Printf("  Message endpoint: http://localhost:%d/message\n", port)
				if cfg.Server.MetricsEnabled {
					fmt.Printf("  Metrics endpoint: http://localhost:%d/metrics\n", cfg.Server.MetricsPort)
				}
			}
			return srv.ServeSSE(addr)

		default:
			return fmt.Errorf("unknown transport: %q (supported: stdio, sse)", transport)
		}
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

// queueStatsAdapter adapts the storage backend to provide queue statistics.
type queueStatsAdapter struct {
	store storage.Backend
}

func (a *queueStatsAdapter) GetQueueSize() int64 {
	stats, err := a.store.GetExtractionQueueStats(context.Background())
	if err != nil {
		return 0
	}
	return stats.PendingCount + stats.ProcessingCount
}

func (a *queueStatsAdapter) GetDeadLetterCount() int64 {
	stats, err := a.store.GetExtractionQueueStats(context.Background())
	if err != nil {
		return 0
	}
	return stats.DeadLetterCount
}
