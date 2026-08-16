package entity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/petal-labs/iris/core"

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

// PanickingExtractor is a mock that panics during Extract, simulating a
// crash in the extraction path.
type PanickingExtractor struct{}

func (p *PanickingExtractor) Extract(ctx context.Context, text string) (*ExtractionResult, error) {
	panic("simulated extraction crash")
}

func TestQueueProcessorPanicRecovery(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	backend := sqlite.NewWithDB(db)
	if err := backend.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Entity.ExtractionMode = "full"
	cfg.Entity.ExtractionInterval = 50 * time.Millisecond

	engine, err := NewEngine(backend, nil, &cfg.Entity)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	resolver := NewResolver(backend, 0.8)

	processor := NewQueueProcessor(backend, &PanickingExtractor{}, resolver, engine, &cfg.Entity)

	// Enqueue an item so processBatch has something to process.
	if err := backend.EnqueueExtraction(context.Background(), &types.ExtractionQueueItem{
		Namespace:  "test-ns",
		SourceType: "conversation",
		SourceID:   "msg-1",
		Content:    "Some content that will trigger a panic during extraction",
	}); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	processor.Start(ctx)

	// Wait long enough for the ticker to fire and processBatch to run.
	// If panic recovery is not working, the goroutine will crash and
	// IsRunning will become false (or the process will panic).
	time.Sleep(200 * time.Millisecond)

	if !processor.IsRunning() {
		t.Fatal("processor stopped unexpectedly — panic was not recovered")
	}

	processor.Stop()
}

func TestQueueProcessorShutdown(t *testing.T) {
	processor, _, backend := setupTestQueueProcessor(t)
	defer backend.Close()

	ctx := context.Background()
	processor.Start(ctx)

	if !processor.IsRunning() {
		t.Fatal("expected processor to be running")
	}

	// Shutdown with a generous timeout should return nil.
	if err := processor.Shutdown(5 * time.Second); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	if processor.IsRunning() {
		t.Error("expected processor to not be running after shutdown")
	}
}

// enqueueTestItem enqueues and dequeues a single item so it is in the
// 'processing' state with one attempt counted, mirroring the real flow.
func enqueueTestItem(t *testing.T, backend *sqlite.Backend, sourceID string) *types.ExtractionQueueItem {
	t.Helper()
	ctx := context.Background()
	if err := backend.EnqueueExtraction(ctx, &types.ExtractionQueueItem{
		Namespace:  "test-ns",
		SourceType: "conversation",
		SourceID:   sourceID,
		Content:    "content",
	}); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}
	items, err := backend.DequeueExtraction(ctx, 1)
	if err != nil {
		t.Fatalf("failed to dequeue: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 dequeued item, got %d", len(items))
	}
	return items[0]
}

// queueAttempts reads the attempts column directly for assertions.
func queueAttempts(t *testing.T, backend *sqlite.Backend, itemID int64) int {
	t.Helper()
	var attempts int
	if err := backend.DB().QueryRowContext(context.Background(),
		"SELECT attempts FROM entity_extraction_queue WHERE id = ?", itemID,
	).Scan(&attempts); err != nil {
		t.Fatalf("failed to read attempts: %v", err)
	}
	return attempts
}

func TestQueueProcessorBackoffRequeues(t *testing.T) {
	processor, _, backend := setupTestQueueProcessor(t)
	defer backend.Close()
	ctx := context.Background()

	item := enqueueTestItem(t, backend, "msg-backoff")

	processor.handleFailure(ctx, item, errors.New("transient provider blip"))

	// The item must be back in pending with a future next_retry_at...
	stats, err := backend.GetExtractionQueueStats(ctx)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.PendingCount != 1 || stats.ProcessingCount != 0 {
		t.Fatalf("expected item requeued to pending, got pending=%d processing=%d", stats.PendingCount, stats.ProcessingCount)
	}

	// ...so an immediate dequeue does NOT return it.
	items, err := backend.DequeueExtraction(ctx, 10)
	if err != nil {
		t.Fatalf("failed to dequeue: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no eligible items during backoff, got %d", len(items))
	}

	// The failed attempt is counted.
	if got := queueAttempts(t, backend, item.ID); got != 1 {
		t.Errorf("expected 1 recorded attempt, got %d", got)
	}

	// Once the backoff elapses, the item is eligible again.
	if _, err := backend.DB().ExecContext(ctx,
		"UPDATE entity_extraction_queue SET next_retry_at = ? WHERE id = ?",
		time.Now().Add(-time.Second).Unix(), item.ID,
	); err != nil {
		t.Fatalf("failed to age out backoff: %v", err)
	}
	items, err = backend.DequeueExtraction(ctx, 10)
	if err != nil {
		t.Fatalf("failed to dequeue: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 eligible item after backoff, got %d", len(items))
	}
}

func TestQueueProcessorNonRetryableDeadLetters(t *testing.T) {
	processor, _, backend := setupTestQueueProcessor(t)
	defer backend.Close()
	ctx := context.Background()

	item := enqueueTestItem(t, backend, "msg-badkey")

	// A bad API key is permanent — wrapped the way processItem wraps it.
	permErr := fmt.Errorf("extraction failed: %w", core.ErrUnauthorized)
	processor.handleFailure(ctx, item, permErr)

	stats, err := backend.GetExtractionQueueStats(ctx)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.DeadLetterCount != 1 {
		t.Fatalf("expected non-retryable failure to dead-letter immediately, got dead_letter=%d", stats.DeadLetterCount)
	}
	if stats.PendingCount != 0 {
		t.Errorf("expected no requeue for permanent error, got pending=%d", stats.PendingCount)
	}
}

func TestQueueProcessorCancelRequeuesWithoutAttempt(t *testing.T) {
	processor, _, backend := setupTestQueueProcessor(t)
	defer backend.Close()

	item := enqueueTestItem(t, backend, "msg-shutdown")

	// Simulate the processor's context being canceled mid-extraction.
	procCtx, cancel := context.WithCancel(context.Background())
	cancel()
	processor.handleFailure(procCtx, item, context.Canceled)

	stats, err := backend.GetExtractionQueueStats(context.Background())
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.PendingCount != 1 || stats.ProcessingCount != 0 {
		t.Fatalf("expected item requeued to pending on shutdown, got pending=%d processing=%d", stats.PendingCount, stats.ProcessingCount)
	}

	// The shutdown must not count as a failed attempt.
	if got := queueAttempts(t, backend, item.ID); got != 0 {
		t.Errorf("expected 0 attempts after shutdown requeue, got %d", got)
	}

	// And the item is immediately eligible.
	items, err := backend.DequeueExtraction(context.Background(), 10)
	if err != nil {
		t.Fatalf("failed to dequeue: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 immediately eligible item, got %d", len(items))
	}
}
