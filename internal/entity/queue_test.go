package entity

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
	"github.com/petal-labs/cortex/pkg/types"

	_ "github.com/mattn/go-sqlite3"
)

// MockExtractor provides a test implementation of entity extraction.
type MockExtractor struct {
	extractFunc func(ctx context.Context, text string) (*ExtractionResult, error)
}

func NewMockExtractor(entities []ExtractedEntity) *MockExtractor {
	return &MockExtractor{
		extractFunc: func(ctx context.Context, text string) (*ExtractionResult, error) {
			return &ExtractionResult{
				Entities:   entities,
				SourceText: text,
			}, nil
		},
	}
}

func (m *MockExtractor) Extract(ctx context.Context, text string) (*ExtractionResult, error) {
	return m.extractFunc(ctx, text)
}

func setupTestQueueProcessor(t *testing.T) (*QueueProcessor, *Engine, *sqlite.Backend) {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	backend := sqlite.NewWithDB(db)
	if err := backend.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	cfg := config.DefaultConfig()
	engine, err := NewEngine(backend, nil, &cfg.Entity)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	resolver := NewResolver(backend, 0.8)

	// Create mock extractor that returns empty results
	mockExtractor := NewMockExtractor([]ExtractedEntity{})

	processor := NewQueueProcessor(backend, mockExtractor, resolver, engine, &cfg.Entity)

	return processor, engine, backend
}

func setupTestQueueProcessorWithEntities(t *testing.T, entities []ExtractedEntity) (*QueueProcessor, *Engine, *sqlite.Backend) {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	backend := sqlite.NewWithDB(db)
	if err := backend.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Entity.ExtractionMode = "full"
	cfg.Entity.MinConfidence = 0.5

	engine, err := NewEngine(backend, nil, &cfg.Entity)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	resolver := NewResolver(backend, 0.8)
	mockExtractor := NewMockExtractor(entities)

	processor := NewQueueProcessor(backend, mockExtractor, resolver, engine, &cfg.Entity)

	return processor, engine, backend
}

func TestQueueProcessorStartStop(t *testing.T) {
	processor, _, backend := setupTestQueueProcessor(t)
	defer backend.Close()

	// Initially not running
	if processor.IsRunning() {
		t.Error("processor should not be running initially")
	}

	// Start
	ctx, cancel := context.WithCancel(context.Background())
	processor.Start(ctx)

	// Wait a bit for the goroutine to start
	time.Sleep(50 * time.Millisecond)

	if !processor.IsRunning() {
		t.Error("processor should be running after Start")
	}

	// Stop via context
	cancel()
	time.Sleep(100 * time.Millisecond)

	if processor.IsRunning() {
		t.Error("processor should stop after context cancel")
	}
}

func TestQueueProcessorManualStop(t *testing.T) {
	processor, _, backend := setupTestQueueProcessor(t)
	defer backend.Close()

	ctx := context.Background()
	processor.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	if !processor.IsRunning() {
		t.Error("processor should be running")
	}

	processor.Stop()
	time.Sleep(100 * time.Millisecond)

	if processor.IsRunning() {
		t.Error("processor should stop after Stop")
	}
}

func TestQueueProcessorShouldProcess(t *testing.T) {
	processor, _, backend := setupTestQueueProcessor(t)
	defer backend.Close()

	item := &types.ExtractionQueueItem{
		SourceID: "test-source",
		Content:  "test content with partnership mention",
	}

	t.Run("full mode processes all", func(t *testing.T) {
		processor.cfg.ExtractionMode = "full"
		if !processor.shouldProcess(item) {
			t.Error("full mode should process all items")
		}
	})

	t.Run("off mode processes none", func(t *testing.T) {
		processor.cfg.ExtractionMode = "off"
		if processor.shouldProcess(item) {
			t.Error("off mode should not process any items")
		}
	})

	t.Run("whitelist mode with matching keyword", func(t *testing.T) {
		processor.cfg.ExtractionMode = "whitelist"
		processor.cfg.WhitelistKeywords = []string{"partnership"}
		if !processor.shouldProcess(item) {
			t.Error("whitelist mode should process items with matching keywords")
		}
	})

	t.Run("whitelist mode without matching keyword", func(t *testing.T) {
		processor.cfg.ExtractionMode = "whitelist"
		processor.cfg.WhitelistKeywords = []string{"acquisition", "merger"}
		item.Content = "unrelated content"
		if processor.shouldProcess(item) {
			t.Error("whitelist mode should not process items without matching keywords")
		}
	})
}

func TestQueueProcessorCalculateBackoff(t *testing.T) {
	processor, _, backend := setupTestQueueProcessor(t)
	defer backend.Close()

	t.Run("fixed backoff", func(t *testing.T) {
		processor.cfg.ExtractionBackoff = "fixed"

		delay := processor.calculateBackoff(1)
		if delay != 5*time.Second {
			t.Errorf("expected 5s fixed delay, got %v", delay)
		}

		delay = processor.calculateBackoff(5)
		if delay != 5*time.Second {
			t.Errorf("expected 5s fixed delay, got %v", delay)
		}
	})

	t.Run("exponential backoff", func(t *testing.T) {
		processor.cfg.ExtractionBackoff = "exponential"

		delay1 := processor.calculateBackoff(1)
		delay2 := processor.calculateBackoff(2)
		delay3 := processor.calculateBackoff(3)

		// Each should be roughly double the previous
		if delay2 <= delay1 {
			t.Errorf("delay2 (%v) should be > delay1 (%v)", delay2, delay1)
		}
		if delay3 <= delay2 {
			t.Errorf("delay3 (%v) should be > delay2 (%v)", delay3, delay2)
		}
	})

	t.Run("exponential caps at 5 minutes", func(t *testing.T) {
		processor.cfg.ExtractionBackoff = "exponential"

		delay := processor.calculateBackoff(100) // Very high attempt count
		if delay > 5*time.Minute {
			t.Errorf("expected max 5m delay, got %v", delay)
		}
	})
}

func TestQueueProcessorProcessSingle(t *testing.T) {
	entities := []ExtractedEntity{
		{Name: "Acme Corp", Type: "organization", Confidence: 0.9},
	}

	processor, _, backend := setupTestQueueProcessorWithEntities(t, entities)
	defer backend.Close()

	ctx := context.Background()

	result, err := processor.ProcessSingle(ctx, "test-ns", "Acme Corp announced new products today.", "conversation", "msg-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Entities) != 1 {
		t.Errorf("expected 1 entity, got %d", len(result.Entities))
	}

	if result.Entities[0].Name != "Acme Corp" {
		t.Errorf("expected entity name 'Acme Corp', got '%s'", result.Entities[0].Name)
	}
}

// Integration test - requires real LLM provider
func TestQueueProcessorProcessSingle_Integration(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY or OPENAI_API_KEY not set, skipping integration test")
	}

	t.Skip("Integration test requires real LLM provider - run manually with API key")
}
