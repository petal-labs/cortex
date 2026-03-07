package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for Cortex.
type Config struct {
	Storage       StorageConfig       `mapstructure:"storage"`
	Embedding     EmbeddingConfig     `mapstructure:"embedding"`
	Summarization SummarizationConfig `mapstructure:"summarization"`
	Conversation  ConversationConfig  `mapstructure:"conversation"`
	Knowledge     KnowledgeConfig     `mapstructure:"knowledge"`
	Context       ContextConfig       `mapstructure:"context"`
	Entity        EntityConfig        `mapstructure:"entity"`
	Retention     RetentionConfig     `mapstructure:"retention"`
	Namespace     NamespaceConfig     `mapstructure:"namespace"`
	Server        ServerConfig        `mapstructure:"server"`
}

// StorageConfig configures the storage backend.
type StorageConfig struct {
	Backend     string `mapstructure:"backend"`      // "sqlite" or "pgvector"
	DataDir     string `mapstructure:"data_dir"`     // For SQLite
	DatabaseURL string `mapstructure:"database_url"` // For pgvector
}

// EmbeddingConfig configures embedding generation.
type EmbeddingConfig struct {
	Provider   string `mapstructure:"provider"`
	Model      string `mapstructure:"model"`
	Dimensions int    `mapstructure:"dimensions"`
	BatchSize  int    `mapstructure:"batch_size"`
	CacheSize  int    `mapstructure:"cache_size"`
}

// SummarizationConfig configures LLM summarization.
type SummarizationConfig struct {
	Provider  string `mapstructure:"provider"`
	Model     string `mapstructure:"model"`
	MaxTokens int    `mapstructure:"max_tokens"`
}

// ConversationConfig configures conversation memory.
type ConversationConfig struct {
	AutoSummarizeThreshold int  `mapstructure:"auto_summarize_threshold"`
	DefaultHistoryLimit    int  `mapstructure:"default_history_limit"`
	SemanticSearchEnabled  bool `mapstructure:"semantic_search_enabled"`
}

// KnowledgeConfig configures the knowledge store.
type KnowledgeConfig struct {
	DefaultChunkStrategy  string `mapstructure:"default_chunk_strategy"`
	DefaultChunkMaxTokens int    `mapstructure:"default_chunk_max_tokens"`
	DefaultChunkOverlap   int    `mapstructure:"default_chunk_overlap"`
	DefaultSearchTopK     int    `mapstructure:"default_search_top_k"`
}

// ContextConfig configures workflow context.
type ContextConfig struct {
	TTLCleanupInterval   time.Duration `mapstructure:"ttl_cleanup_interval"`
	HistoryRetentionDays int           `mapstructure:"history_retention_days"`
}

// EntityConfig configures entity memory.
type EntityConfig struct {
	ExtractionMode               string        `mapstructure:"extraction_mode"` // "off", "sampled", "whitelist", "full"
	ExtractionModel              string        `mapstructure:"extraction_model"`
	ExtractionBatchSize          int           `mapstructure:"extraction_batch_size"`
	ExtractionInterval           time.Duration `mapstructure:"extraction_interval"`
	SampleRate                   float64       `mapstructure:"sample_rate"`
	WhitelistKeywords            []string      `mapstructure:"whitelist_keywords"`
	SummaryRegenerationThreshold int           `mapstructure:"summary_regeneration_threshold"`
	MinConfidence                float64       `mapstructure:"min_confidence"`
	MinMentionsToKeep            int           `mapstructure:"min_mentions_to_keep"`
	AliasFuzzyThreshold          float64       `mapstructure:"alias_fuzzy_threshold"`
	ExtractionMaxAttempts        int           `mapstructure:"extraction_max_attempts"`
	ExtractionBackoff            string        `mapstructure:"extraction_backoff"`            // "fixed", "exponential"
	ExtractionDeadLetterPolicy   string        `mapstructure:"extraction_dead_letter_policy"` // "retain", "drop"
}

// RetentionConfig configures data lifecycle.
type RetentionConfig struct {
	ConversationRetentionDays int           `mapstructure:"conversation_retention_days"`
	EntityStaleDays           int           `mapstructure:"entity_stale_days"`
	ContextRunRetentionDays   int           `mapstructure:"context_run_retention_days"`
	GCInterval                time.Duration `mapstructure:"gc_interval"`
}

// NamespaceConfig configures namespace isolation.
type NamespaceConfig struct {
	AllowedNamespaces []string `mapstructure:"allowed_namespaces"`
}

// ServerConfig configures the server.
type ServerConfig struct {
	LogLevel          string `mapstructure:"log_level"`
	MetricsEnabled    bool   `mapstructure:"metrics_enabled"`
	MetricsPort       int    `mapstructure:"metrics_port"`
	StructuredLogging bool   `mapstructure:"structured_logging"`
	RequestIDHeader   string `mapstructure:"request_id_header"`
}

// DefaultConfig returns a Config with default values.
func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	return &Config{
		Storage: StorageConfig{
			Backend: "sqlite",
			DataDir: filepath.Join(homeDir, ".cortex", "data"),
		},
		Embedding: EmbeddingConfig{
			Provider:   "openai",
			Model:      "text-embedding-3-small",
			Dimensions: 1536,
			BatchSize:  100,
			CacheSize:  1000,
		},
		Summarization: SummarizationConfig{
			Provider:  "anthropic",
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 1024,
		},
		Conversation: ConversationConfig{
			AutoSummarizeThreshold: 50,
			DefaultHistoryLimit:    20,
			SemanticSearchEnabled:  true,
		},
		Knowledge: KnowledgeConfig{
			DefaultChunkStrategy:  "sentence",
			DefaultChunkMaxTokens: 512,
			DefaultChunkOverlap:   50,
			DefaultSearchTopK:     5,
		},
		Context: ContextConfig{
			TTLCleanupInterval:   60 * time.Second,
			HistoryRetentionDays: 30,
		},
		Entity: EntityConfig{
			ExtractionMode:               "full",
			ExtractionModel:              "claude-haiku-4-5-20251001",
			ExtractionBatchSize:          10,
			ExtractionInterval:           5 * time.Second,
			SampleRate:                   1.0,
			WhitelistKeywords:            []string{},
			SummaryRegenerationThreshold: 5,
			MinConfidence:                0.5,
			MinMentionsToKeep:            1,
			AliasFuzzyThreshold:          0.85,
			ExtractionMaxAttempts:        5,
			ExtractionBackoff:            "exponential",
			ExtractionDeadLetterPolicy:   "retain",
		},
		Retention: RetentionConfig{
			ConversationRetentionDays: 0, // 0 = no auto-delete
			EntityStaleDays:           0, // 0 = no auto-prune
			ContextRunRetentionDays:   30,
			GCInterval:                24 * time.Hour,
		},
		Namespace: NamespaceConfig{
			AllowedNamespaces: []string{}, // Empty = allow all
		},
		Server: ServerConfig{
			LogLevel:          "info",
			MetricsEnabled:    true,
			MetricsPort:       9811,
			StructuredLogging: true,
			RequestIDHeader:   "X-PetalFlow-Request-ID",
		},
	}
}

// Load loads configuration from file and environment variables.
func Load(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	v := viper.New()
	v.SetConfigType("yaml")

	// Set defaults from default config
	setViperDefaults(v, cfg)

	// If config path provided, use it (but only if it exists)
	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			v.SetConfigFile(configPath)
		}
		// If file doesn't exist, we'll just use defaults
	} else {
		// Look in default locations
		homeDir, _ := os.UserHomeDir()
		v.AddConfigPath(filepath.Join(homeDir, ".cortex"))
		v.AddConfigPath(".")
		v.SetConfigName("config")
	}

	// Environment variable overrides
	v.SetEnvPrefix("CORTEX")
	v.AutomaticEnv()

	// Read config file (ignore if not found)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// Only return error for actual read errors, not missing files
			if !os.IsNotExist(err) {
				return nil, err
			}
		}
	}

	// Unmarshal into config struct
	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func setViperDefaults(v *viper.Viper, cfg *Config) {
	v.SetDefault("storage.backend", cfg.Storage.Backend)
	v.SetDefault("storage.data_dir", cfg.Storage.DataDir)
	v.SetDefault("embedding.provider", cfg.Embedding.Provider)
	v.SetDefault("embedding.model", cfg.Embedding.Model)
	v.SetDefault("embedding.dimensions", cfg.Embedding.Dimensions)
	v.SetDefault("embedding.batch_size", cfg.Embedding.BatchSize)
	v.SetDefault("embedding.cache_size", cfg.Embedding.CacheSize)
	v.SetDefault("summarization.provider", cfg.Summarization.Provider)
	v.SetDefault("summarization.model", cfg.Summarization.Model)
	v.SetDefault("summarization.max_tokens", cfg.Summarization.MaxTokens)
	v.SetDefault("conversation.auto_summarize_threshold", cfg.Conversation.AutoSummarizeThreshold)
	v.SetDefault("conversation.default_history_limit", cfg.Conversation.DefaultHistoryLimit)
	v.SetDefault("conversation.semantic_search_enabled", cfg.Conversation.SemanticSearchEnabled)
	v.SetDefault("knowledge.default_chunk_strategy", cfg.Knowledge.DefaultChunkStrategy)
	v.SetDefault("knowledge.default_chunk_max_tokens", cfg.Knowledge.DefaultChunkMaxTokens)
	v.SetDefault("knowledge.default_chunk_overlap", cfg.Knowledge.DefaultChunkOverlap)
	v.SetDefault("knowledge.default_search_top_k", cfg.Knowledge.DefaultSearchTopK)
	v.SetDefault("context.ttl_cleanup_interval", cfg.Context.TTLCleanupInterval)
	v.SetDefault("context.history_retention_days", cfg.Context.HistoryRetentionDays)
	v.SetDefault("entity.extraction_mode", cfg.Entity.ExtractionMode)
	v.SetDefault("entity.extraction_model", cfg.Entity.ExtractionModel)
	v.SetDefault("entity.extraction_batch_size", cfg.Entity.ExtractionBatchSize)
	v.SetDefault("entity.extraction_interval", cfg.Entity.ExtractionInterval)
	v.SetDefault("entity.sample_rate", cfg.Entity.SampleRate)
	v.SetDefault("entity.summary_regeneration_threshold", cfg.Entity.SummaryRegenerationThreshold)
	v.SetDefault("entity.min_confidence", cfg.Entity.MinConfidence)
	v.SetDefault("entity.min_mentions_to_keep", cfg.Entity.MinMentionsToKeep)
	v.SetDefault("entity.alias_fuzzy_threshold", cfg.Entity.AliasFuzzyThreshold)
	v.SetDefault("entity.extraction_max_attempts", cfg.Entity.ExtractionMaxAttempts)
	v.SetDefault("entity.extraction_backoff", cfg.Entity.ExtractionBackoff)
	v.SetDefault("entity.extraction_dead_letter_policy", cfg.Entity.ExtractionDeadLetterPolicy)
	v.SetDefault("retention.conversation_retention_days", cfg.Retention.ConversationRetentionDays)
	v.SetDefault("retention.entity_stale_days", cfg.Retention.EntityStaleDays)
	v.SetDefault("retention.context_run_retention_days", cfg.Retention.ContextRunRetentionDays)
	v.SetDefault("retention.gc_interval", cfg.Retention.GCInterval)
	v.SetDefault("server.log_level", cfg.Server.LogLevel)
	v.SetDefault("server.metrics_enabled", cfg.Server.MetricsEnabled)
	v.SetDefault("server.metrics_port", cfg.Server.MetricsPort)
	v.SetDefault("server.structured_logging", cfg.Server.StructuredLogging)
	v.SetDefault("server.request_id_header", cfg.Server.RequestIDHeader)
}
