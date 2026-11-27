package cortex

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/internal/storage/pgvector"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
	"github.com/petal-labs/cortex/internal/summarization"
)

// Client is the top-level programmatic facade for Cortex.
// It owns the storage backend, optional embedding/summarization providers,
// and background workers, and exposes typed sub-clients for all four memory primitives.
type Client struct {
	store           storage.Backend
	emb             embedding.Provider
	queueProcessor  *entity.QueueProcessor
	shutdownTimeout time.Duration

	conversation *ConversationClient
	knowledge    *KnowledgeClient
	context      *ContextClient
	entity       *EntityClient

	closeOnce sync.Once
	closeErr  error
}

// Open initializes a Cortex Client using the supplied options, runs storage
// migrations, and starts any configured background workers.
// Call Close when finished to drain background workers and release resources.
func Open(ctx context.Context, opts ...Option) (*Client, error) {
	oc := &openConfig{
		shutdownTimeout: 30 * time.Second,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(oc)
		}
	}

	var cfg *config.Config
	if oc.configFile != "" {
		var err error
		cfg, err = config.Load(oc.configFile)
		if err != nil {
			return nil, fmt.Errorf("load config: %w", err)
		}
	} else {
		cfg = config.DefaultConfig()
	}

	if oc.backend != "" {
		cfg.Storage.Backend = oc.backend
	}
	if oc.dataDir != "" {
		cfg.Storage.DataDir = oc.dataDir
	}
	if oc.databaseURL != "" {
		cfg.Storage.DatabaseURL = oc.databaseURL
	}
	if oc.poolMin > 0 {
		cfg.Storage.PoolMinConns = oc.poolMin
	}
	if oc.poolMax > 0 {
		cfg.Storage.PoolMaxConns = oc.poolMax
	}
	if oc.autoSummarizeThreshold != nil {
		cfg.Conversation.AutoSummarizeThreshold = *oc.autoSummarizeThreshold
	}
	if oc.defaultHistoryLimit != nil {
		cfg.Conversation.DefaultHistoryLimit = *oc.defaultHistoryLimit
	}
	if oc.semanticSearchSet {
		cfg.Conversation.SemanticSearchEnabled = oc.semanticSearchEnable
	}
	if oc.defaultChunkConfig != nil {
		if oc.defaultChunkConfig.Strategy != "" {
			cfg.Knowledge.DefaultChunkStrategy = oc.defaultChunkConfig.Strategy
		}
		if oc.defaultChunkConfig.MaxTokens > 0 {
			cfg.Knowledge.DefaultChunkMaxTokens = oc.defaultChunkConfig.MaxTokens
		}
		if oc.defaultChunkConfig.Overlap >= 0 {
			cfg.Knowledge.DefaultChunkOverlap = oc.defaultChunkConfig.Overlap
		}
	}
	if oc.irisEmbProvider != "" {
		cfg.Embedding.Provider = oc.irisEmbProvider
	}
	if oc.irisEmbModel != "" {
		cfg.Embedding.Model = oc.irisEmbModel
	}
	if oc.irisEmbDims > 0 {
		cfg.Embedding.Dimensions = oc.irisEmbDims
	}
	if oc.embeddingCacheSize != nil {
		cfg.Embedding.CacheSize = *oc.embeddingCacheSize
	}
	if oc.irisSummProvider != "" {
		cfg.Summarization.Provider = oc.irisSummProvider
	}
	if oc.irisSummModel != "" {
		cfg.Summarization.Model = oc.irisSummModel
	}
	if oc.irisSummMaxTokens > 0 {
		cfg.Summarization.MaxTokens = oc.irisSummMaxTokens
	}
	if oc.extractionEnabled {
		if oc.extractionProvider != "" {
			cfg.Summarization.Provider = oc.extractionProvider
		}
		if oc.extractionModel != "" {
			cfg.Entity.ExtractionModel = oc.extractionModel
		}
		if oc.extractionMode != "" {
			cfg.Entity.ExtractionMode = oc.extractionMode
		}
	}

	// Initialize storage backend
	var store storage.Backend
	var err error
	switch cfg.Storage.Backend {
	case "", "sqlite":
		store, err = sqlite.New(cfg)
		if err != nil {
			return nil, fmt.Errorf("open sqlite storage: %w", err)
		}
	case "pgvector", "postgres", "postgresql":
		if cfg.Storage.DatabaseURL == "" {
			return nil, fmt.Errorf("pgvector database URL is required: %w", ErrInvalidInput)
		}
		store, err = pgvector.New(cfg)
		if err != nil {
			return nil, fmt.Errorf("open pgvector storage: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported storage backend %q: %w", cfg.Storage.Backend, ErrInvalidInput)
	}

	cleanupOnErr := func() {
		_ = store.Close()
	}

	// Ensure migrations are applied
	if err := store.Migrate(ctx); err != nil {
		cleanupOnErr()
		return nil, fmt.Errorf("migrate storage: %w", err)
	}

	// Configure optional embedding provider
	var emb embedding.Provider
	switch {
	case oc.embeddingProvider != nil:
		emb = oc.embeddingProvider
	case oc.irisEmbProvider != "":
		emb, err = embedding.NewIrisClient(cfg)
		if err != nil {
			cleanupOnErr()
			return nil, fmt.Errorf("create embedding provider: %w", err)
		}
	}
	if emb != nil && cfg.Embedding.CacheSize > 0 {
		cached, err := embedding.NewCachedProvider(emb, cfg.Embedding.CacheSize)
		if err != nil {
			_ = emb.Close()
			cleanupOnErr()
			return nil, fmt.Errorf("create embedding cache: %w", err)
		}
		emb = cached
	}

	// If caller provided no embedding provider and did not explicitly toggle semantic search,
	// keep conversation Append working cleanly without trying to embed.
	if emb == nil && !oc.semanticSearchSet {
		cfg.Conversation.SemanticSearchEnabled = false
	}

	// Construct internal engines
	convEng, err := conversation.NewEngine(store, emb, &cfg.Conversation)
	if err != nil {
		if emb != nil {
			_ = emb.Close()
		}
		cleanupOnErr()
		return nil, fmt.Errorf("create conversation engine: %w", err)
	}

	switch {
	case oc.summarizer != nil:
		convEng.SetSummarizer(oc.summarizer)
	case oc.irisSummProvider != "" && cfg.Summarization.Model != "":
		summClient, err := summarization.NewClient(cfg)
		if err != nil {
			if emb != nil {
				_ = emb.Close()
			}
			cleanupOnErr()
			return nil, fmt.Errorf("create summarizer: %w", err)
		}
		convEng.SetSummarizer(summClient)
	}

	knowEng, err := knowledge.NewEngine(store, emb, &cfg.Knowledge)
	if err != nil {
		if emb != nil {
			_ = emb.Close()
		}
		cleanupOnErr()
		return nil, fmt.Errorf("create knowledge engine: %w", err)
	}

	ctxEng, err := ctxengine.NewEngine(store, &cfg.Context)
	if err != nil {
		if emb != nil {
			_ = emb.Close()
		}
		cleanupOnErr()
		return nil, fmt.Errorf("create context engine: %w", err)
	}

	entEng, err := entity.NewEngine(store, emb, &cfg.Entity)
	if err != nil {
		if emb != nil {
			_ = emb.Close()
		}
		cleanupOnErr()
		return nil, fmt.Errorf("create entity engine: %w", err)
	}

	// Optional background entity extraction worker
	var queueProcessor *entity.QueueProcessor
	if oc.extractionEnabled && cfg.Entity.ExtractionMode != "off" {
		extractor, err := entity.NewExtractor(cfg)
		if err != nil {
			if emb != nil {
				_ = emb.Close()
			}
			cleanupOnErr()
			return nil, fmt.Errorf("create entity extractor: %w", err)
		}
		resolver := entity.NewResolver(store, cfg.Entity.AliasFuzzyThreshold)
		queueProcessor = entity.NewQueueProcessor(store, extractor, resolver, entEng, &cfg.Entity)
		enqueuer := entity.NewExtractionEnqueuerAdapter(store)
		convEng.SetExtractionEnqueuer(enqueuer)
		knowEng.SetExtractionEnqueuer(enqueuer)
		queueProcessor.Start(ctx)
	}

	return &Client{
		store:           store,
		emb:             emb,
		queueProcessor:  queueProcessor,
		shutdownTimeout: oc.shutdownTimeout,
		conversation:    &ConversationClient{engine: convEng},
		knowledge:       &KnowledgeClient{engine: knowEng},
		context:         &ContextClient{engine: ctxEng},
		entity:          &EntityClient{engine: entEng},
	}, nil
}

// New is an alias for Open.
func New(ctx context.Context, opts ...Option) (*Client, error) {
	return Open(ctx, opts...)
}

// Conversation returns the Conversation Memory sub-client.
func (c *Client) Conversation() *ConversationClient {
	return c.conversation
}

// Knowledge returns the Knowledge Store sub-client.
func (c *Client) Knowledge() *KnowledgeClient {
	return c.knowledge
}

// Context returns the Workflow Context sub-client.
func (c *Client) Context() *ContextClient {
	return c.context
}

// Entity returns the Entity Memory sub-client.
func (c *Client) Entity() *EntityClient {
	return c.entity
}

// Health verifies connectivity to the underlying storage backend.
func (c *Client) Health(ctx context.Context) error {
	return c.store.Health(ctx)
}

// Close drains any background workers, closes the embedding provider, and closes
// the underlying storage backend. Calling Close more than once is safe.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		var errs []error
		if c.queueProcessor != nil {
			timeout := c.shutdownTimeout
			if timeout <= 0 {
				timeout = 30 * time.Second
			}
			if err := c.queueProcessor.Shutdown(timeout); err != nil {
				errs = append(errs, fmt.Errorf("shutdown entity queue processor: %w", err))
			}
		}
		if c.emb != nil {
			if err := c.emb.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close embedding provider: %w", err))
			}
		}
		if c.store != nil {
			if err := c.store.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close storage: %w", err))
			}
		}
		c.closeErr = errors.Join(errs...)
	})
	return c.closeErr
}
