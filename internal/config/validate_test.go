package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDefaultConfigIsValid locks the shipped defaults against Validate —
// a fresh install must never fail startup validation.
func TestDefaultConfigIsValid(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("default config failed validation: %v", err)
	}
}

// mutate applies fn to a copy of the default config.
func mutate(t *testing.T, fn func(*Config)) *Config {
	t.Helper()
	cfg := DefaultConfig()
	fn(cfg)
	return cfg
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*Config)
		wantSubstr string // substring expected in the error message
	}{
		{
			name:       "pgvector without database_url",
			mutate:     func(c *Config) { c.Storage.Backend = "pgvector" },
			wantSubstr: `storage.database_url is required when storage.backend is "pgvector"`,
		},
		{
			name:       "unsupported backend",
			mutate:     func(c *Config) { c.Storage.Backend = "mysql" },
			wantSubstr: `storage.backend "mysql" is not supported`,
		},
		{
			name:       "empty backend",
			mutate:     func(c *Config) { c.Storage.Backend = "" },
			wantSubstr: "storage.backend is required",
		},
		{
			name:       "negative dimensions",
			mutate:     func(c *Config) { c.Embedding.Dimensions = -1536 },
			wantSubstr: "embedding.dimensions must be > 0 (got -1536)",
		},
		{
			name:       "zero dimensions",
			mutate:     func(c *Config) { c.Embedding.Dimensions = 0 },
			wantSubstr: "embedding.dimensions must be > 0",
		},
		{
			name:       "negative batch size",
			mutate:     func(c *Config) { c.Embedding.BatchSize = -1 },
			wantSubstr: "embedding.batch_size must be >= 0",
		},
		{
			name:       "negative embedding timeout",
			mutate:     func(c *Config) { c.Embedding.Timeout = -time.Second },
			wantSubstr: "embedding.timeout must be >= 0",
		},
		{
			name:       "unsupported chunk strategy",
			mutate:     func(c *Config) { c.Knowledge.DefaultChunkStrategy = "shred" },
			wantSubstr: `knowledge.default_chunk_strategy "shred" is not supported`,
		},
		{
			name:       "zero chunk max tokens",
			mutate:     func(c *Config) { c.Knowledge.DefaultChunkMaxTokens = 0 },
			wantSubstr: "knowledge.default_chunk_max_tokens must be > 0",
		},
		{
			name:       "zero ttl cleanup interval would panic ticker",
			mutate:     func(c *Config) { c.Context.TTLCleanupInterval = 0 },
			wantSubstr: "context.ttl_cleanup_interval must be > 0",
		},
		{
			name:       "negative gc interval would panic ticker",
			mutate:     func(c *Config) { c.Retention.GCInterval = -time.Hour },
			wantSubstr: "retention.gc_interval must be > 0",
		},
		{
			name:       "unsupported extraction mode",
			mutate:     func(c *Config) { c.Entity.ExtractionMode = "sometimes" },
			wantSubstr: `entity.extraction_mode "sometimes" is not supported`,
		},
		{
			name: "whitelist mode without keywords",
			mutate: func(c *Config) {
				c.Entity.ExtractionMode = "whitelist"
				c.Entity.WhitelistKeywords = nil
			},
			wantSubstr: `entity.whitelist_keywords must not be empty when entity.extraction_mode is "whitelist"`,
		},
		{
			name:       "sample rate above one",
			mutate:     func(c *Config) { c.Entity.SampleRate = 1.5 },
			wantSubstr: "entity.sample_rate must be within [0, 1] (got 1.5)",
		},
		{
			name:       "min confidence below zero",
			mutate:     func(c *Config) { c.Entity.MinConfidence = -0.1 },
			wantSubstr: "entity.min_confidence must be within [0, 1]",
		},
		{
			name:       "unsupported backoff",
			mutate:     func(c *Config) { c.Entity.ExtractionBackoff = "linear" },
			wantSubstr: `entity.extraction_backoff "linear" is not supported`,
		},
		{
			name:       "unsupported dead letter policy",
			mutate:     func(c *Config) { c.Entity.ExtractionDeadLetterPolicy = "explode" },
			wantSubstr: `entity.extraction_dead_letter_policy "explode" is not supported`,
		},
		{
			name:       "negative retention days",
			mutate:     func(c *Config) { c.Retention.ConversationRetentionDays = -7 },
			wantSubstr: "retention.conversation_retention_days must be >= 0",
		},
		{
			name:       "unsupported log level",
			mutate:     func(c *Config) { c.Server.LogLevel = "verbose" },
			wantSubstr: `server.log_level "verbose" is not supported`,
		},
		{
			name:       "metrics port out of range",
			mutate:     func(c *Config) { c.Server.MetricsPort = 99999 },
			wantSubstr: "server.metrics_port must be within [1, 65535]",
		},
		{
			name:       "metrics port ignored when metrics disabled",
			mutate:     func(c *Config) { c.Server.MetricsEnabled = false; c.Server.MetricsPort = 0 },
			wantSubstr: "", // valid: port unchecked when metrics are off
		},
		{
			name:       "negative shutdown timeout",
			mutate:     func(c *Config) { c.Server.ShutdownTimeout = -time.Second },
			wantSubstr: "server.shutdown_timeout must be >= 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := mutate(t, tc.mutate).Validate()
			if tc.wantSubstr == "" {
				if err != nil {
					t.Fatalf("expected valid, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("expected error containing %q, got:\n%v", tc.wantSubstr, err)
			}
		})
	}
}

// TestValidateReportsAllProblems verifies multiple violations are all
// listed, not just the first.
func TestValidateReportsAllProblems(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Embedding.Dimensions = -1
	cfg.Storage.Backend = "pgvector"
	cfg.Server.LogLevel = "loud"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		"storage.database_url",
		"embedding.dimensions",
		`server.log_level "loud"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in error, got:\n%v", want, err)
		}
	}
	if !strings.HasPrefix(err.Error(), "invalid config:") {
		t.Errorf("expected 'invalid config:' prefix, got: %v", err)
	}
}

// TestLoadRejectsInvalidConfigFile verifies a bad config file fails Load at
// startup with the actionable message.
func TestLoadRejectsInvalidConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
storage:
  backend: pgvector
embedding:
  dimensions: -5
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected Load to reject invalid config")
	}
	for _, want := range []string{"storage.database_url", "embedding.dimensions"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected %q in error, got: %v", want, err)
		}
	}
}

// TestLoadAcceptsValidOverrides verifies legit overrides still load.
func TestLoadAcceptsValidOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
storage:
  backend: pgvector
  database_url: postgres://localhost/cortex
embedding:
  dimensions: 768
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Embedding.Dimensions != 768 {
		t.Errorf("expected dimensions override 768, got %d", cfg.Embedding.Dimensions)
	}
	if cfg.Storage.Backend != "pgvector" {
		t.Errorf("expected pgvector backend, got %s", cfg.Storage.Backend)
	}
}

// TestChatTimeoutDefaults verifies both chat-path timeouts default to an
// explicit 120s — matching what the SDK would otherwise apply implicitly,
// so behavior is unchanged but now visible and configurable.
func TestChatTimeoutDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Summarization.Timeout != 120*time.Second {
		t.Errorf("expected summarization.timeout default 120s, got %s", cfg.Summarization.Timeout)
	}
	if cfg.Entity.ExtractionTimeout != 120*time.Second {
		t.Errorf("expected entity.extraction_timeout default 120s, got %s", cfg.Entity.ExtractionTimeout)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}

// TestValidateChatTimeouts rejects negative timeouts on both paths.
func TestValidateChatTimeouts(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*Config)
		wantSubstr string
	}{
		{
			name:       "negative summarization timeout",
			mutate:     func(c *Config) { c.Summarization.Timeout = -time.Second },
			wantSubstr: "summarization.timeout must be >= 0",
		},
		{
			name:       "negative extraction timeout",
			mutate:     func(c *Config) { c.Entity.ExtractionTimeout = -time.Second },
			wantSubstr: "entity.extraction_timeout must be >= 0",
		},
		{
			name: "zero disables and is valid",
			mutate: func(c *Config) {
				c.Summarization.Timeout = 0
				c.Entity.ExtractionTimeout = 0
			},
			wantSubstr: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := mutate(t, tc.mutate).Validate()
			if tc.wantSubstr == "" {
				if err != nil {
					t.Fatalf("expected valid, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantSubstr, err)
			}
		})
	}
}

// TestLoadChatTimeoutOverrides verifies the knobs load from a config file.
func TestLoadChatTimeoutOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
summarization:
  timeout: 45s
entity:
  extraction_timeout: 0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Summarization.Timeout != 45*time.Second {
		t.Errorf("expected summarization.timeout 45s, got %s", cfg.Summarization.Timeout)
	}
	if cfg.Entity.ExtractionTimeout != 0 {
		t.Errorf("expected entity.extraction_timeout 0 (disabled), got %s", cfg.Entity.ExtractionTimeout)
	}
}
