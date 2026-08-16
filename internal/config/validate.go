package config

import (
	"fmt"
	"strings"
	"time"
)

// Validate checks required fields and value ranges, returning an error that
// lists every violation with its config key and an actionable message. It
// is invoked after loading so bad values fail at startup with a clear
// message instead of late and opaquely (e.g. a time.NewTicker panic on a
// non-positive interval, or a pgvector backend without database_url).
func (c *Config) Validate() error {
	var problems []string

	// Storage
	switch c.Storage.Backend {
	case "sqlite":
	case "pgvector":
		if strings.TrimSpace(c.Storage.DatabaseURL) == "" {
			problems = append(problems,
				`storage.database_url is required when storage.backend is "pgvector"`)
		}
	case "":
		problems = append(problems,
			`storage.backend is required (supported: "sqlite", "pgvector")`)
	default:
		problems = append(problems, fmt.Sprintf(
			`storage.backend %q is not supported (supported: "sqlite", "pgvector")`, c.Storage.Backend))
	}

	// Embedding
	if c.Embedding.Dimensions <= 0 {
		problems = append(problems, fmt.Sprintf(
			"embedding.dimensions must be > 0 (got %d)", c.Embedding.Dimensions))
	}
	if c.Embedding.BatchSize < 0 {
		problems = append(problems, fmt.Sprintf(
			"embedding.batch_size must be >= 0, 0 means no limit (got %d)", c.Embedding.BatchSize))
	}
	if c.Embedding.CacheSize < 0 {
		problems = append(problems, fmt.Sprintf(
			"embedding.cache_size must be >= 0 (got %d)", c.Embedding.CacheSize))
	}
	if c.Embedding.Timeout < 0 {
		problems = append(problems, fmt.Sprintf(
			"embedding.timeout must be >= 0, 0 disables the timeout (got %s)", c.Embedding.Timeout))
	}

	// Summarization
	if c.Summarization.MaxTokens < 0 {
		problems = append(problems, fmt.Sprintf(
			"summarization.max_tokens must be >= 0 (got %d)", c.Summarization.MaxTokens))
	}
	if c.Summarization.Timeout < 0 {
		problems = append(problems, fmt.Sprintf(
			"summarization.timeout must be >= 0, 0 disables the timeout (got %s)", c.Summarization.Timeout))
	}

	// Conversation
	if c.Conversation.AutoSummarizeThreshold < 0 {
		problems = append(problems, fmt.Sprintf(
			"conversation.auto_summarize_threshold must be >= 0, 0 disables auto-summarization (got %d)",
			c.Conversation.AutoSummarizeThreshold))
	}
	if c.Conversation.DefaultHistoryLimit < 0 {
		problems = append(problems, fmt.Sprintf(
			"conversation.default_history_limit must be >= 0 (got %d)", c.Conversation.DefaultHistoryLimit))
	}

	// Knowledge
	switch c.Knowledge.DefaultChunkStrategy {
	case "fixed", "sentence", "paragraph", "semantic":
	case "":
		problems = append(problems, "knowledge.default_chunk_strategy is required "+
			`(supported: "fixed", "sentence", "paragraph", "semantic")`)
	default:
		problems = append(problems, fmt.Sprintf(
			`knowledge.default_chunk_strategy %q is not supported (supported: "fixed", "sentence", "paragraph", "semantic")`,
			c.Knowledge.DefaultChunkStrategy))
	}
	if c.Knowledge.DefaultChunkMaxTokens <= 0 {
		problems = append(problems, fmt.Sprintf(
			"knowledge.default_chunk_max_tokens must be > 0 (got %d)", c.Knowledge.DefaultChunkMaxTokens))
	}
	if c.Knowledge.DefaultChunkOverlap < 0 {
		problems = append(problems, fmt.Sprintf(
			"knowledge.default_chunk_overlap must be >= 0 (got %d)", c.Knowledge.DefaultChunkOverlap))
	}
	if c.Knowledge.DefaultSearchTopK <= 0 {
		problems = append(problems, fmt.Sprintf(
			"knowledge.default_search_top_k must be > 0 (got %d)", c.Knowledge.DefaultSearchTopK))
	}

	// Context — time.NewTicker panics on non-positive intervals.
	if c.Context.TTLCleanupInterval <= 0 {
		problems = append(problems, fmt.Sprintf(
			"context.ttl_cleanup_interval must be > 0 (got %s)", c.Context.TTLCleanupInterval))
	}
	if c.Context.HistoryRetentionDays < 0 {
		problems = append(problems, fmt.Sprintf(
			"context.history_retention_days must be >= 0, 0 retains forever (got %d)", c.Context.HistoryRetentionDays))
	}

	// Entity
	switch c.Entity.ExtractionMode {
	case "off", "sampled", "whitelist", "full":
	default:
		problems = append(problems, fmt.Sprintf(
			`entity.extraction_mode %q is not supported (supported: "off", "sampled", "whitelist", "full")`,
			c.Entity.ExtractionMode))
	}
	if c.Entity.ExtractionMode == "whitelist" && len(c.Entity.WhitelistKeywords) == 0 {
		problems = append(problems,
			"entity.whitelist_keywords must not be empty when entity.extraction_mode is \"whitelist\" "+
				"(nothing would ever be extracted)")
	}
	if c.Entity.SampleRate < 0 || c.Entity.SampleRate > 1 {
		problems = append(problems, fmt.Sprintf(
			"entity.sample_rate must be within [0, 1] (got %v)", c.Entity.SampleRate))
	}
	if c.Entity.MinConfidence < 0 || c.Entity.MinConfidence > 1 {
		problems = append(problems, fmt.Sprintf(
			"entity.min_confidence must be within [0, 1] (got %v)", c.Entity.MinConfidence))
	}
	if c.Entity.AliasFuzzyThreshold < 0 || c.Entity.AliasFuzzyThreshold > 1 {
		problems = append(problems, fmt.Sprintf(
			"entity.alias_fuzzy_threshold must be within [0, 1] (got %v)", c.Entity.AliasFuzzyThreshold))
	}
	if c.Entity.ExtractionBatchSize < 0 {
		problems = append(problems, fmt.Sprintf(
			"entity.extraction_batch_size must be >= 0 (got %d)", c.Entity.ExtractionBatchSize))
	}
	if c.Entity.ExtractionInterval < 0 {
		problems = append(problems, fmt.Sprintf(
			"entity.extraction_interval must be >= 0 (got %s)", c.Entity.ExtractionInterval))
	}
	if c.Entity.ExtractionMaxAttempts < 0 {
		problems = append(problems, fmt.Sprintf(
			"entity.extraction_max_attempts must be >= 0 (got %d)", c.Entity.ExtractionMaxAttempts))
	}
	switch c.Entity.ExtractionBackoff {
	case "fixed", "exponential":
	default:
		problems = append(problems, fmt.Sprintf(
			`entity.extraction_backoff %q is not supported (supported: "fixed", "exponential")`,
			c.Entity.ExtractionBackoff))
	}
	switch c.Entity.ExtractionDeadLetterPolicy {
	case "retain", "drop":
	default:
		problems = append(problems, fmt.Sprintf(
			`entity.extraction_dead_letter_policy %q is not supported (supported: "retain", "drop")`,
			c.Entity.ExtractionDeadLetterPolicy))
	}
	if c.Entity.ExtractionTimeout < 0 {
		problems = append(problems, fmt.Sprintf(
			"entity.extraction_timeout must be >= 0, 0 disables the timeout (got %s)", c.Entity.ExtractionTimeout))
	}

	// Retention — time.NewTicker panics on non-positive intervals.
	if c.Retention.ConversationRetentionDays < 0 {
		problems = append(problems, fmt.Sprintf(
			"retention.conversation_retention_days must be >= 0, 0 disables auto-delete (got %d)",
			c.Retention.ConversationRetentionDays))
	}
	if c.Retention.EntityStaleDays < 0 {
		problems = append(problems, fmt.Sprintf(
			"retention.entity_stale_days must be >= 0, 0 disables pruning (got %d)",
			c.Retention.EntityStaleDays))
	}
	if c.Retention.ContextRunRetentionDays < 0 {
		problems = append(problems, fmt.Sprintf(
			"retention.context_run_retention_days must be >= 0 (got %d)",
			c.Retention.ContextRunRetentionDays))
	}
	if c.Retention.GCInterval <= 0 {
		problems = append(problems, fmt.Sprintf(
			"retention.gc_interval must be > 0 (got %s)", c.Retention.GCInterval))
	}

	// Server
	switch c.Server.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		problems = append(problems, fmt.Sprintf(
			`server.log_level %q is not supported (supported: "debug", "info", "warn", "error")`,
			c.Server.LogLevel))
	}
	if c.Server.MetricsEnabled && (c.Server.MetricsPort < 1 || c.Server.MetricsPort > 65535) {
		problems = append(problems, fmt.Sprintf(
			"server.metrics_port must be within [1, 65535] when metrics are enabled (got %d)",
			c.Server.MetricsPort))
	}
	if c.Server.ShutdownTimeout < 0 {
		problems = append(problems, fmt.Sprintf(
			"server.shutdown_timeout must be >= 0, 0 uses the default %s (got %s)",
			30*time.Second, c.Server.ShutdownTimeout))
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("invalid config:\n  - %s", strings.Join(problems, "\n  - "))
}
