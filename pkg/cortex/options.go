// Package cortex provides the public, SemVer-stable Go SDK for embedding and
// interacting with Cortex programmatically without importing internal packages.
package cortex

import (
	"context"
	"errors"
	"time"

	"github.com/petal-labs/cortex/pkg/types"
)

// Common sentinel errors returned by the Cortex SDK.
var (
	ErrNotFound              = errors.New("not found")
	ErrAlreadyExists         = errors.New("already exists")
	ErrVersionConflict       = errors.New("version conflict")
	ErrInvalidInput          = errors.New("invalid input")
	ErrVoiceStorageDisabled  = errors.New("voice transcript storage is disabled")
	ErrUncommittedTranscript = errors.New("uncommitted partial transcript cannot be persisted")
	ErrRawAudioDisallowed    = errors.New("raw audio persistence is disabled")
	ErrNamespaceNotAllowed   = errors.New("namespace not allowed")
)

// SearchMode defines the retrieval strategy for search operations.
type SearchMode string

const (
	// SearchModeVector uses pure vector similarity search.
	SearchModeVector SearchMode = "vector"
	// SearchModeHybrid combines vector and full-text search using Reciprocal Rank Fusion.
	SearchModeHybrid SearchMode = "hybrid"
	// SearchModeText uses pure full-text search (BM25 / tsvector).
	SearchModeText SearchMode = "text"
	// SearchModeGraph uses zero-LLM Vector-Graph RAG (Knowledge search only).
	SearchModeGraph SearchMode = "graph"
)

// EmbeddingProvider generates dense vector embeddings for text.
// Callers may supply a custom implementation via WithEmbeddingProvider or
// configure a built-in Iris provider via WithIrisEmbedding.
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
	Close() error
}

// Summarizer generates summaries from conversation messages.
// Callers may supply a custom implementation via WithSummarizer or
// configure a built-in Iris provider via WithIrisSummarizer.
type Summarizer interface {
	SummarizeMessages(ctx context.Context, messages []*types.Message) (string, error)
}

type openConfig struct {
	configFile string

	backend     string
	dataDir     string
	databaseURL string
	poolMin     int32
	poolMax     int32

	embeddingProvider    EmbeddingProvider
	irisEmbProvider      string
	irisEmbModel         string
	irisEmbDims          int
	embeddingCacheSize   *int
	semanticSearchSet    bool
	semanticSearchEnable bool

	summarizer        Summarizer
	irisSummProvider  string
	irisSummModel     string
	irisSummMaxTokens int

	extractionEnabled  bool
	extractionProvider string
	extractionModel    string
	extractionMode     string

	autoSummarizeThreshold *int
	defaultHistoryLimit    *int
	defaultChunkConfig     *types.ChunkConfig
	shutdownTimeout        time.Duration

	voiceTranscriptStorageSet     bool
	voiceTranscriptStorageEnabled bool
	voiceTranscriptRetentionDays  *int
	allowedNamespacesSet          bool
	allowedNamespaces             []string
}

// Option configures a Cortex Client created via Open or New.
type Option func(*openConfig)

// WithConfigFile loads base settings from a YAML config file before applying
// explicit programmatic options.
func WithConfigFile(path string) Option {
	return func(c *openConfig) {
		c.configFile = path
	}
}

// WithSQLite configures Cortex to use the local SQLite + sqlite-vec backend
// storing data in dataDir/cortex.db.
func WithSQLite(dataDir string) Option {
	return func(c *openConfig) {
		c.backend = "sqlite"
		c.dataDir = dataDir
	}
}

// WithPGVector configures Cortex to use PostgreSQL + pgvector at databaseURL.
func WithPGVector(databaseURL string) Option {
	return func(c *openConfig) {
		c.backend = "pgvector"
		c.databaseURL = databaseURL
	}
}

// WithPGVectorPool configures connection pool limits for the pgvector backend.
func WithPGVectorPool(minConns, maxConns int32) Option {
	return func(c *openConfig) {
		c.poolMin = minConns
		c.poolMax = maxConns
	}
}

// WithEmbeddingProvider injects a caller-supplied EmbeddingProvider.
func WithEmbeddingProvider(p EmbeddingProvider) Option {
	return func(c *openConfig) {
		c.embeddingProvider = p
	}
}

// WithIrisEmbedding configures a built-in Iris embedding provider
// (e.g., provider "openai", model "text-embedding-3-small", dimensions 1536).
func WithIrisEmbedding(provider, model string, dimensions int) Option {
	return func(c *openConfig) {
		c.irisEmbProvider = provider
		c.irisEmbModel = model
		c.irisEmbDims = dimensions
	}
}

// WithEmbeddingCacheSize sets the LRU cache size for embeddings (0 disables caching).
func WithEmbeddingCacheSize(size int) Option {
	return func(c *openConfig) {
		c.embeddingCacheSize = &size
	}
}

// WithSemanticSearch enables or disables conversation semantic search.
func WithSemanticSearch(enabled bool) Option {
	return func(c *openConfig) {
		c.semanticSearchSet = true
		c.semanticSearchEnable = enabled
	}
}

// WithSummarizer injects a caller-supplied conversation Summarizer.
func WithSummarizer(s Summarizer) Option {
	return func(c *openConfig) {
		c.summarizer = s
	}
}

// WithIrisSummarizer configures a built-in Iris summarization provider
// (e.g., provider "anthropic", model "claude-sonnet-4-6").
func WithIrisSummarizer(provider, model string) Option {
	return func(c *openConfig) {
		c.irisSummProvider = provider
		c.irisSummModel = model
	}
}

// WithEntityExtraction enables the background entity extraction pipeline using
// the specified Iris chat provider, extraction model, and mode ("full", "sampled", "whitelist").
func WithEntityExtraction(provider, model, mode string) Option {
	return func(c *openConfig) {
		c.extractionEnabled = true
		c.extractionProvider = provider
		c.extractionModel = model
		c.extractionMode = mode
	}
}

// WithAutoSummarizeThreshold sets the unsummarized message threshold that triggers
// automatic conversation summarization (0 disables auto-summarization).
func WithAutoSummarizeThreshold(threshold int) Option {
	return func(c *openConfig) {
		c.autoSummarizeThreshold = &threshold
	}
}

// WithDefaultHistoryLimit sets the default number of messages returned by Conversation.History.
func WithDefaultHistoryLimit(limit int) Option {
	return func(c *openConfig) {
		c.defaultHistoryLimit = &limit
	}
}

// WithDefaultChunkConfig sets default chunking parameters for Knowledge collections.
func WithDefaultChunkConfig(cfg types.ChunkConfig) Option {
	return func(c *openConfig) {
		cp := cfg
		c.defaultChunkConfig = &cp
	}
}

// WithShutdownTimeout sets the maximum duration Close waits for background workers to drain.
func WithShutdownTimeout(d time.Duration) Option {
	return func(c *openConfig) {
		c.shutdownTimeout = d
	}
}

// WithVoiceTranscriptStorage explicitly enables or disables opt-in persistence
// of committed voice transcripts. Voice transcript storage is disabled by
// default for privacy.
func WithVoiceTranscriptStorage(enabled bool) Option {
	return func(c *openConfig) {
		c.voiceTranscriptStorageSet = true
		c.voiceTranscriptStorageEnabled = enabled
	}
}

// WithVoiceTranscriptRetention sets the voice-specific transcript retention
// window in days (0 disables voice-specific automatic deletion).
func WithVoiceTranscriptRetention(days int) Option {
	return func(c *openConfig) {
		c.voiceTranscriptRetentionDays = &days
	}
}

// WithAllowedNamespaces restricts voice memory operations to the provided
// namespace allowlist. Passing no namespaces allows all non-empty namespaces.
func WithAllowedNamespaces(namespaces ...string) Option {
	return func(c *openConfig) {
		c.allowedNamespacesSet = true
		c.allowedNamespaces = append([]string(nil), namespaces...)
	}
}
