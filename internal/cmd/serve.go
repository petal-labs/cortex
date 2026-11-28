package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/petal-labs/cortex/v2/internal/config"
	ctxengine "github.com/petal-labs/cortex/v2/internal/context"
	"github.com/petal-labs/cortex/v2/internal/conversation"
	"github.com/petal-labs/cortex/v2/internal/embedding"
	"github.com/petal-labs/cortex/v2/internal/entity"
	"github.com/petal-labs/cortex/v2/internal/gc"
	"github.com/petal-labs/cortex/v2/internal/knowledge"
	"github.com/petal-labs/cortex/v2/internal/observability"
	"github.com/petal-labs/cortex/v2/internal/server"
	"github.com/petal-labs/cortex/v2/internal/storage"
	"github.com/petal-labs/cortex/v2/internal/summarization"
)

var (
	mcpMode          bool
	allowedNamespace string
	transport        string
	serveHost        string
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
  sse   - Server-Sent Events over HTTP (binds to 127.0.0.1 by default)

Examples:
  # Start MCP server with stdio transport (default)
  cortex serve

  # Start MCP server with SSE transport on 127.0.0.1:9810
  cortex serve --transport sse --port 9810

  # Start MCP server with namespace restriction (overrides namespace.allowed_namespaces)
  cortex serve --namespace my-workflow

  # SSE server bound to a specific host with namespace restriction
  cortex serve --transport sse --host 127.0.0.1 --port 9810 --namespace my-workflow`,
	RunE: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().BoolVar(&mcpMode, "mcp", true, "Run in MCP mode (default)")
	serveCmd.Flags().StringVar(&allowedNamespace, "namespace", "", "Restrict to a single namespace (overrides namespace.allowed_namespaces)")
	serveCmd.Flags().StringVar(&transport, "transport", "stdio", "Transport mode: stdio or sse")
	serveCmd.Flags().StringVar(&serveHost, "host", "127.0.0.1", "Host interface to bind for SSE transport (default 127.0.0.1)")
	serveCmd.Flags().IntVar(&port, "port", 9810, "Port for SSE transport (default 9810)")
}

// resolveServeBindHost resolves the SSE bind host from CLI flag and config,
// defaulting to 127.0.0.1.
func resolveServeBindHost(flagHost string, flagChanged bool, cfgHost string) string {
	if flagChanged && strings.TrimSpace(flagHost) != "" {
		return strings.TrimSpace(flagHost)
	}
	if strings.TrimSpace(cfgHost) != "" {
		return strings.TrimSpace(cfgHost)
	}
	if strings.TrimSpace(flagHost) != "" {
		return strings.TrimSpace(flagHost)
	}
	return "127.0.0.1"
}

func runServe(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

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

		metricsHost := strings.TrimSpace(cfg.Server.MetricsHost)
		if metricsHost == "" {
			metricsHost = observability.DefaultMetricsHost
		}
		if !server.IsLoopbackHost(metricsHost) {
			observability.Warn(ctx, "metrics server binding to non-loopback address without authentication",
				observability.Field("metrics_host", metricsHost),
				observability.Field("metrics_port", cfg.Server.MetricsPort),
			)
		}

		// Start metrics server; /ready probes the storage backend so
		// orchestrators can hold traffic until it is reachable.
		metricsServer = observability.NewMetricsServerWithHost(metricsHost, cfg.Server.MetricsPort, store.Health)
		metricsServer.Start()
	}

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
	convEngine.SetVoiceConfig(&cfg.Voice)
	convEngine.SetAllowedNamespaces(cfg.Namespace.AllowedNamespaces)

	// Set up summarization if provider is configured
	if cfg.Summarization.Provider != "" && cfg.Summarization.Model != "" {
		summClient, err := summarization.NewClient(cfg)
		if err != nil {
			return fmt.Errorf("failed to create summarization client: %w", err)
		}
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

	// Set up entity extraction pipeline if provider is configured and extraction is enabled
	var queueProcessor *entity.QueueProcessor
	if cfg.Summarization.Provider != "" && cfg.Entity.ExtractionMode != "off" {
		// Create extractor (uses iris SDK)
		extractor, err := entity.NewExtractor(cfg)
		if err != nil {
			return fmt.Errorf("failed to create entity extractor: %w", err)
		}

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

	// Start queue processor in background if configured
	if queueProcessor != nil {
		queueProcessor.Start(ctx)
	}

	// Start MCP server
	if mcpMode {
		mcpCfg := &server.Config{
			Name:              "cortex",
			Version:           "1.0.0",
			AllowedNamespace:  allowedNamespace,
			AllowedNamespaces: cfg.Namespace.AllowedNamespaces,
		}

		srv := server.New(mcpCfg, convEngine, knowEngine, ctxEngine, entityEngine)

		// Run server in a goroutine so we can coordinate shutdown
		errCh := make(chan error, 1)
		// Select transport mode
		switch transport {
		case "stdio", "":
			observability.Info(ctx, "starting MCP server with stdio transport")
			go func() { errCh <- srv.ServeStdio() }()

		case "sse":
			bindHost := resolveServeBindHost(serveHost, cmd.Flags().Changed("host"), cfg.Server.Host)
			addr := net.JoinHostPort(bindHost, strconv.Itoa(port))
			if !server.IsLoopbackHost(bindHost) {
				observability.Warn(ctx, "MCP SSE server binding to non-loopback address without authentication; place behind a TLS/auth reverse proxy",
					observability.Field("addr", addr),
				)
			}
			observability.Info(ctx, "starting MCP server with SSE transport",
				observability.Field("addr", addr),
				observability.Field("sse_endpoint", "/sse"),
				observability.Field("message_endpoint", "/message"),
			)
			if !cfg.Server.StructuredLogging {
				fmt.Printf("Starting Cortex MCP server with SSE transport on %s\n", addr)
				fmt.Printf("  SSE endpoint: http://%s/sse\n", addr)
				fmt.Printf("  Message endpoint: http://%s/message\n", addr)
				if cfg.Server.MetricsEnabled && metricsServer != nil {
					fmt.Printf("  Metrics endpoint: http://%s/metrics\n", metricsServer.Addr())
				}
			}
			go func() { errCh <- srv.ServeSSE(addr) }()

		default:
			return fmt.Errorf("unknown transport: %q (supported: stdio, sse)", transport)
		}

		// Wait for shutdown signal or server error
		select {
		case <-ctx.Done():
			observability.Info(context.Background(), "shutdown signal received, coordinating graceful shutdown")
		case err := <-errCh:
			return err
		}

		// Bounded shutdown of background workers
		shutdownTimeout := cfg.Server.ShutdownTimeout
		if shutdownTimeout <= 0 {
			shutdownTimeout = 30 * time.Second
		}

		if queueProcessor != nil {
			if err := queueProcessor.Shutdown(shutdownTimeout); err != nil {
				observability.Error(context.Background(), "queue processor shutdown timeout", zap.Error(err))
			}
		}
		if err := gcCollector.Shutdown(shutdownTimeout); err != nil {
			observability.Error(context.Background(), "garbage collector shutdown timeout", zap.Error(err))
		}

		return nil
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
	cfg := config.DefaultConfig()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
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
