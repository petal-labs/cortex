package conversation

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
	"github.com/petal-labs/cortex/pkg/types"
)

// MockEmbeddingProvider implements embedding.Provider for testing.
type MockEmbeddingProvider struct {
	dimensions int
	callCount  int
}

func NewMockEmbeddingProvider(dimensions int) *MockEmbeddingProvider {
	return &MockEmbeddingProvider{dimensions: dimensions}
}

func (m *MockEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	m.callCount++
	// Return deterministic embedding based on text length
	embedding := make([]float32, m.dimensions)
	for i := 0; i < m.dimensions; i++ {
		embedding[i] = float32(len(text)%100) / 100.0
	}
	return embedding, nil
}

func (m *MockEmbeddingProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, _ := m.Embed(ctx, text)
		results[i] = emb
	}
	return results, nil
}

func (m *MockEmbeddingProvider) Dimensions() int {
	return m.dimensions
}

func (m *MockEmbeddingProvider) Close() error {
	return nil
}

func (m *MockEmbeddingProvider) CallCount() int {
	return m.callCount
}

// Helper to create test engine with in-memory SQLite
func setupTestEngine(t *testing.T, semanticSearchEnabled bool) (*Engine, *sqlite.Backend) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	backend := sqlite.NewWithDB(db)
	if err := backend.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	cfg := &config.ConversationConfig{
		AutoSummarizeThreshold: 50,
		DefaultHistoryLimit:    20,
		SemanticSearchEnabled:  semanticSearchEnabled,
	}

	var emb *MockEmbeddingProvider
	if semanticSearchEnabled {
		emb = NewMockEmbeddingProvider(128)
	}

	engine, err := NewEngine(backend, emb, cfg)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	return engine, backend
}

func TestNewEngine(t *testing.T) {
	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	backend := sqlite.NewWithDB(db)

	t.Run("creates engine with valid deps", func(t *testing.T) {
		engine, err := NewEngine(backend, nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if engine == nil {
			t.Error("expected engine to be created")
		}
	})

	t.Run("fails without storage", func(t *testing.T) {
		_, err := NewEngine(nil, nil, nil)
		if err == nil {
			t.Error("expected error without storage")
		}
	})

	t.Run("uses default config when nil", func(t *testing.T) {
		engine, _ := NewEngine(backend, nil, nil)
		if engine.cfg.DefaultHistoryLimit != 20 {
			t.Errorf("expected default history limit 20, got %d", engine.cfg.DefaultHistoryLimit)
		}
	})
}

func TestAppend(t *testing.T) {
	engine, _ := setupTestEngine(t, true)
	ctx := context.Background()
	namespace := "test-ns"

	t.Run("appends message with new thread", func(t *testing.T) {
		msg, err := engine.Append(ctx, namespace, "thread-1", "user", "Hello, world!", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if msg.ID == "" {
			t.Error("expected message ID to be set")
		}
		if msg.ThreadID != "thread-1" {
			t.Errorf("expected thread-1, got %s", msg.ThreadID)
		}
		if msg.Role != "user" {
			t.Errorf("expected user role, got %s", msg.Role)
		}
		if msg.Content != "Hello, world!" {
			t.Errorf("unexpected content: %s", msg.Content)
		}
	})

	t.Run("generates thread ID if not provided", func(t *testing.T) {
		msg, err := engine.Append(ctx, namespace, "", "user", "Test", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if msg.ThreadID == "" {
			t.Error("expected thread ID to be generated")
		}
	})

	t.Run("truncates long content", func(t *testing.T) {
		longContent := make([]byte, 1000)
		for i := range longContent {
			longContent[i] = 'x'
		}

		opts := &AppendOpts{MaxContentLength: 100}
		msg, err := engine.Append(ctx, namespace, "thread-truncate", "user", string(longContent), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(msg.Content) > 120 { // 100 + "[truncated]" + newline
			t.Errorf("content not truncated: length %d", len(msg.Content))
		}
		if msg.Content[len(msg.Content)-11:] != "[truncated]" {
			t.Error("expected truncation marker")
		}
	})

	t.Run("rejects empty content", func(t *testing.T) {
		_, err := engine.Append(ctx, namespace, "thread-empty", "user", "", nil)
		if err != ErrEmptyContent {
			t.Errorf("expected ErrEmptyContent, got %v", err)
		}
	})

	t.Run("rejects invalid role", func(t *testing.T) {
		_, err := engine.Append(ctx, namespace, "thread-role", "invalid", "test", nil)
		if err == nil {
			t.Error("expected error for invalid role")
		}
	})

	t.Run("accepts valid roles", func(t *testing.T) {
		for role := range ValidRoles {
			_, err := engine.Append(ctx, namespace, "thread-roles", role, "test "+role, nil)
			if err != nil {
				t.Errorf("unexpected error for role %s: %v", role, err)
			}
		}
	})

	t.Run("stores metadata", func(t *testing.T) {
		opts := &AppendOpts{
			Metadata: map[string]string{"key": "value"},
		}
		msg, err := engine.Append(ctx, namespace, "thread-meta", "user", "test", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if msg.Metadata["key"] != "value" {
			t.Error("metadata not stored")
		}
	})

	t.Run("skips embedding when requested", func(t *testing.T) {
		embProvider := engine.embedding.(*MockEmbeddingProvider)
		initialCount := embProvider.CallCount()

		opts := &AppendOpts{SkipEmbedding: true}
		_, err := engine.Append(ctx, namespace, "thread-noemb", "user", "test", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if embProvider.CallCount() != initialCount {
			t.Error("embedding should have been skipped")
		}
	})
}

func TestHistory(t *testing.T) {
	engine, _ := setupTestEngine(t, false)
	ctx := context.Background()
	namespace := "test-ns"
	threadID := "history-thread"

	// Add some messages
	for i := 0; i < 10; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		_, err := engine.Append(ctx, namespace, threadID, role, "Message "+string(rune('A'+i)), nil)
		if err != nil {
			t.Fatalf("failed to append message: %v", err)
		}
		time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	}

	t.Run("retrieves messages in order", func(t *testing.T) {
		result, err := engine.History(ctx, namespace, threadID, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Messages) == 0 {
			t.Error("expected messages")
		}

		// Messages should be in chronological order (oldest first)
		for i := 1; i < len(result.Messages); i++ {
			if result.Messages[i].CreatedAt.Before(result.Messages[i-1].CreatedAt) {
				t.Error("messages not in chronological order")
			}
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		opts := &HistoryOpts{LastN: 5}
		result, err := engine.History(ctx, namespace, threadID, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Messages) > 5 {
			t.Errorf("expected at most 5 messages, got %d", len(result.Messages))
		}
	})

	t.Run("includes thread ID in result", func(t *testing.T) {
		result, err := engine.History(ctx, namespace, threadID, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.ThreadID != threadID {
			t.Errorf("expected thread ID %s, got %s", threadID, result.ThreadID)
		}
	})

	t.Run("returns empty for non-existent thread", func(t *testing.T) {
		result, err := engine.History(ctx, namespace, "non-existent", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Messages) != 0 {
			t.Errorf("expected 0 messages for non-existent thread, got %d", len(result.Messages))
		}
	})
}

func TestSearch(t *testing.T) {
	engine, _ := setupTestEngine(t, true)
	ctx := context.Background()
	namespace := "test-ns"
	threadID := "search-thread"

	// Add messages with embeddings
	messages := []string{
		"Hello, how can I help you today?",
		"I need information about machine learning",
		"Machine learning is a subset of AI",
		"The weather is nice today",
	}

	for _, content := range messages {
		_, err := engine.Append(ctx, namespace, threadID, "user", content, nil)
		if err != nil {
			t.Fatalf("failed to append: %v", err)
		}
	}

	t.Run("searches messages", func(t *testing.T) {
		result, err := engine.Search(ctx, namespace, "artificial intelligence", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Query != "artificial intelligence" {
			t.Error("query not included in result")
		}

		// Results should exist (even if ordering isn't perfect with mock embeddings)
		if len(result.Results) == 0 {
			t.Error("expected search results")
		}
	})

	t.Run("respects topK", func(t *testing.T) {
		opts := &SearchOpts{TopK: 2}
		result, err := engine.Search(ctx, namespace, "test query", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Results) > 2 {
			t.Errorf("expected at most 2 results, got %d", len(result.Results))
		}
	})

	t.Run("filters by thread", func(t *testing.T) {
		// Add message to different thread
		_, _ = engine.Append(ctx, namespace, "other-thread", "user", "Different thread message", nil)

		opts := &SearchOpts{ThreadID: &threadID}
		result, err := engine.Search(ctx, namespace, "message", opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		for _, r := range result.Results {
			if r.ThreadID != threadID {
				t.Errorf("expected results from %s, got %s", threadID, r.ThreadID)
			}
		}
	})

	t.Run("rejects empty query", func(t *testing.T) {
		_, err := engine.Search(ctx, namespace, "", nil)
		if err == nil {
			t.Error("expected error for empty query")
		}
	})
}

func TestSearchDisabled(t *testing.T) {
	engine, _ := setupTestEngine(t, false) // Semantic search disabled
	ctx := context.Background()

	_, err := engine.Search(ctx, "test-ns", "query", nil)
	if err == nil {
		t.Error("expected error when semantic search disabled")
	}
}

func TestClear(t *testing.T) {
	engine, backend := setupTestEngine(t, false)
	ctx := context.Background()
	namespace := "test-ns"
	threadID := "clear-thread"

	// Add messages
	for i := 0; i < 5; i++ {
		_, err := engine.Append(ctx, namespace, threadID, "user", "Message", nil)
		if err != nil {
			t.Fatalf("failed to append: %v", err)
		}
	}

	// Verify messages exist
	result, _ := engine.History(ctx, namespace, threadID, nil)
	if len(result.Messages) == 0 {
		t.Fatal("expected messages before clear")
	}

	// Clear thread
	err := engine.Clear(ctx, namespace, threadID)
	if err != nil {
		t.Fatalf("failed to clear: %v", err)
	}

	// Verify thread is gone
	_, err = backend.GetThread(ctx, namespace, threadID)
	if err == nil {
		t.Error("expected thread to be deleted")
	}
}

func TestClearNotFound(t *testing.T) {
	engine, _ := setupTestEngine(t, false)
	ctx := context.Background()

	err := engine.Clear(ctx, "test-ns", "non-existent")
	if err != ErrThreadNotFound {
		t.Errorf("expected ErrThreadNotFound, got %v", err)
	}
}

func TestListThreads(t *testing.T) {
	engine, _ := setupTestEngine(t, false)
	ctx := context.Background()
	namespace := "test-ns"

	// Create multiple threads
	for i := 0; i < 5; i++ {
		threadID := "list-thread-" + string(rune('A'+i))
		_, err := engine.Append(ctx, namespace, threadID, "user", "Hello", nil)
		if err != nil {
			t.Fatalf("failed to append: %v", err)
		}
		time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	}

	t.Run("lists all threads", func(t *testing.T) {
		threads, _, err := engine.ListThreads(ctx, namespace, "", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(threads) < 5 {
			t.Errorf("expected at least 5 threads, got %d", len(threads))
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		threads, _, err := engine.ListThreads(ctx, namespace, "", 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(threads) > 3 {
			t.Errorf("expected at most 3 threads, got %d", len(threads))
		}
	})

	t.Run("returns empty for unknown namespace", func(t *testing.T) {
		threads, _, err := engine.ListThreads(ctx, "unknown-ns", "", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(threads) != 0 {
			t.Errorf("expected 0 threads, got %d", len(threads))
		}
	})
}

func TestGetThread(t *testing.T) {
	engine, _ := setupTestEngine(t, false)
	ctx := context.Background()
	namespace := "test-ns"
	threadID := "get-thread"

	// Create thread by appending message
	_, err := engine.Append(ctx, namespace, threadID, "user", "Hello", nil)
	if err != nil {
		t.Fatalf("failed to append: %v", err)
	}

	t.Run("retrieves existing thread", func(t *testing.T) {
		thread, err := engine.GetThread(ctx, namespace, threadID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if thread.ID != threadID {
			t.Errorf("expected thread ID %s, got %s", threadID, thread.ID)
		}
	})

	t.Run("returns error for non-existent thread", func(t *testing.T) {
		_, err := engine.GetThread(ctx, namespace, "non-existent")
		if err != ErrThreadNotFound {
			t.Errorf("expected ErrThreadNotFound, got %v", err)
		}
	})
}

func TestUpdateThread(t *testing.T) {
	engine, _ := setupTestEngine(t, false)
	ctx := context.Background()
	namespace := "test-ns"
	threadID := "update-thread"

	// Create thread
	_, err := engine.Append(ctx, namespace, threadID, "user", "Hello", nil)
	if err != nil {
		t.Fatalf("failed to append: %v", err)
	}

	// Update thread
	thread := &types.Thread{
		ID:        threadID,
		Namespace: namespace,
		Title:     "Updated Title",
		Summary:   "This is a summary",
	}

	err = engine.UpdateThread(ctx, thread)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify update
	updated, err := engine.GetThread(ctx, namespace, threadID)
	if err != nil {
		t.Fatalf("failed to get thread: %v", err)
	}

	if updated.Title != "Updated Title" {
		t.Errorf("expected title 'Updated Title', got '%s'", updated.Title)
	}
	if updated.Summary != "This is a summary" {
		t.Errorf("expected summary, got '%s'", updated.Summary)
	}
}

func TestMarkSummarized(t *testing.T) {
	engine, backend := setupTestEngine(t, false)
	ctx := context.Background()
	namespace := "test-ns"
	threadID := "summarize-thread"

	// Add messages
	for i := 0; i < 5; i++ {
		_, err := engine.Append(ctx, namespace, threadID, "user", "Message", nil)
		if err != nil {
			t.Fatalf("failed to append: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Mark messages before a time in the future to ensure all are included
	// (beforeTime is exclusive, so we add a buffer)
	err := engine.MarkSummarized(ctx, namespace, threadID, time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify by checking messages directly
	messages, _, err := backend.GetMessages(ctx, namespace, threadID, 100, "")
	if err != nil {
		t.Fatalf("failed to get messages: %v", err)
	}

	for _, msg := range messages {
		if !msg.Summarized {
			t.Error("expected all messages to be marked as summarized")
		}
	}
}
