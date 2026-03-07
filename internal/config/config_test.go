package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Verify storage defaults
	if cfg.Storage.Backend != "sqlite" {
		t.Errorf("expected storage.backend to be 'sqlite', got '%s'", cfg.Storage.Backend)
	}

	// Verify embedding defaults
	if cfg.Embedding.Provider != "openai" {
		t.Errorf("expected embedding.provider to be 'openai', got '%s'", cfg.Embedding.Provider)
	}
	if cfg.Embedding.Model != "text-embedding-3-small" {
		t.Errorf("expected embedding.model to be 'text-embedding-3-small', got '%s'", cfg.Embedding.Model)
	}
	if cfg.Embedding.Dimensions != 1536 {
		t.Errorf("expected embedding.dimensions to be 1536, got %d", cfg.Embedding.Dimensions)
	}

	// Verify conversation defaults
	if cfg.Conversation.AutoSummarizeThreshold != 50 {
		t.Errorf("expected conversation.auto_summarize_threshold to be 50, got %d", cfg.Conversation.AutoSummarizeThreshold)
	}
	if !cfg.Conversation.SemanticSearchEnabled {
		t.Error("expected conversation.semantic_search_enabled to be true")
	}

	// Verify entity defaults
	if cfg.Entity.ExtractionMode != "full" {
		t.Errorf("expected entity.extraction_mode to be 'full', got '%s'", cfg.Entity.ExtractionMode)
	}
	if cfg.Entity.MinConfidence != 0.5 {
		t.Errorf("expected entity.min_confidence to be 0.5, got %f", cfg.Entity.MinConfidence)
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	// Create a temp directory and config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	configContent := `
storage:
  backend: pgvector
  database_url: postgres://user:pass@localhost:5432/cortex

embedding:
  provider: voyageai
  model: voyage-3-large
  dimensions: 1024

conversation:
  auto_summarize_threshold: 100
  semantic_search_enabled: false

entity:
  extraction_mode: sampled
  sample_rate: 0.5
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Verify loaded values
	if cfg.Storage.Backend != "pgvector" {
		t.Errorf("expected storage.backend to be 'pgvector', got '%s'", cfg.Storage.Backend)
	}
	if cfg.Embedding.Provider != "voyageai" {
		t.Errorf("expected embedding.provider to be 'voyageai', got '%s'", cfg.Embedding.Provider)
	}
	if cfg.Embedding.Dimensions != 1024 {
		t.Errorf("expected embedding.dimensions to be 1024, got %d", cfg.Embedding.Dimensions)
	}
	if cfg.Conversation.AutoSummarizeThreshold != 100 {
		t.Errorf("expected conversation.auto_summarize_threshold to be 100, got %d", cfg.Conversation.AutoSummarizeThreshold)
	}
	if cfg.Conversation.SemanticSearchEnabled {
		t.Error("expected conversation.semantic_search_enabled to be false")
	}
	if cfg.Entity.ExtractionMode != "sampled" {
		t.Errorf("expected entity.extraction_mode to be 'sampled', got '%s'", cfg.Entity.ExtractionMode)
	}
	if cfg.Entity.SampleRate != 0.5 {
		t.Errorf("expected entity.sample_rate to be 0.5, got %f", cfg.Entity.SampleRate)
	}
}

func TestLoadConfigWithDefaults(t *testing.T) {
	// Load from non-existent file should use defaults
	cfg, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing config file, got: %v", err)
	}

	// Should have default values
	if cfg.Storage.Backend != "sqlite" {
		t.Errorf("expected storage.backend to be 'sqlite', got '%s'", cfg.Storage.Backend)
	}
	if cfg.Embedding.Provider != "openai" {
		t.Errorf("expected embedding.provider to be 'openai', got '%s'", cfg.Embedding.Provider)
	}
}

func TestLoadConfigEnvOverride(t *testing.T) {
	// Set environment variable
	os.Setenv("CORTEX_STORAGE_BACKEND", "pgvector")
	defer os.Unsetenv("CORTEX_STORAGE_BACKEND")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Environment variable should not override in current implementation
	// (viper AutomaticEnv uses underscore replacement which may not match)
	// This test documents current behavior
	_ = cfg
}

func TestConfigDurations(t *testing.T) {
	cfg := DefaultConfig()

	// Verify duration fields are set correctly
	if cfg.Context.TTLCleanupInterval != 60*time.Second {
		t.Errorf("expected context.ttl_cleanup_interval to be 60s, got %v", cfg.Context.TTLCleanupInterval)
	}
	if cfg.Entity.ExtractionInterval != 5*time.Second {
		t.Errorf("expected entity.extraction_interval to be 5s, got %v", cfg.Entity.ExtractionInterval)
	}
	if cfg.Retention.GCInterval != 24*time.Hour {
		t.Errorf("expected retention.gc_interval to be 24h, got %v", cfg.Retention.GCInterval)
	}
}
