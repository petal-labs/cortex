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

// MockSummarizer implements Summarizer for testing.
type MockSummarizer struct {
	summary   string
	callCount int
	messages  []*types.Message // Last messages passed to Summarize
}

func NewMockSummarizer(summary string) *MockSummarizer {
	return &MockSummarizer{summary: summary}
}

func (m *MockSummarizer) SummarizeMessages(ctx context.Context, messages []*types.Message) (string, error) {
	m.callCount++
	m.messages = messages
	return m.summary, nil
}

func TestSummarize(t *testing.T) {
	t.Run("summarizes messages and updates thread", func(t *testing.T) {
		engine, backend := setupTestEngine(t, false)
		ctx := context.Background()
		namespace := "test-ns"
		threadID := "thread-1"

		// Create messages with explicit timestamps to ensure they're distinct
		// This avoids relying on wall-clock time in tests
		baseTime := time.Now().Unix()
		for i := 0; i < 15; i++ {
			msg := &types.Message{
				ID:        "msg-" + string(rune('a'+i)),
				Namespace: namespace,
				ThreadID:  threadID,
				Role:      "user",
				Content:   "Message " + string(rune('a'+i)),
				CreatedAt: time.Unix(baseTime+int64(i), 0), // Each message 1 second apart
			}
			if err := backend.AppendMessage(ctx, msg); err != nil {
				t.Fatalf("failed to append: %v", err)
			}
		}

		// Set up summarizer
		summarizer := NewMockSummarizer("This is the conversation summary.")
		engine.SetSummarizer(summarizer)

		// Summarize with 5 messages kept
		result, err := engine.Summarize(ctx, namespace, threadID, &SummarizeOpts{KeepRecent: 5})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify result
		if result.Summary != "This is the conversation summary." {
			t.Errorf("expected summary 'This is the conversation summary.', got %q", result.Summary)
		}
		if result.MessagesSummarized != 10 {
			t.Errorf("expected 10 messages summarized, got %d", result.MessagesSummarized)
		}
		if result.MessagesKept != 5 {
			t.Errorf("expected 5 messages kept, got %d", result.MessagesKept)
		}

		// Verify thread was updated
		thread, err := backend.GetThread(ctx, namespace, threadID)
		if err != nil {
			t.Fatalf("failed to get thread: %v", err)
		}
		if thread.Summary != "This is the conversation summary." {
			t.Errorf("expected thread summary to be updated, got %q", thread.Summary)
		}

		// Verify messages were marked as summarized
		messages, _, err := backend.GetMessages(ctx, namespace, threadID, 100, "")
		if err != nil {
			t.Fatalf("failed to get messages: %v", err)
		}

		summarizedCount := 0
		for _, msg := range messages {
			if msg.Summarized {
				summarizedCount++
			}
		}
		if summarizedCount != 10 {
			t.Errorf("expected 10 summarized messages, got %d", summarizedCount)
		}
	})

	t.Run("returns error when no summarizer set", func(t *testing.T) {
		engine, _ := setupTestEngine(t, false)
		ctx := context.Background()

		_, err := engine.Summarize(ctx, "ns", "thread", nil)
		if err != ErrSummarizerNotSet {
			t.Errorf("expected ErrSummarizerNotSet, got %v", err)
		}
	})

	t.Run("returns error when not enough messages", func(t *testing.T) {
		engine, _ := setupTestEngine(t, false)
		ctx := context.Background()
		namespace := "test-ns"
		threadID := "thread-1"

		// Add only 5 messages
		for i := 0; i < 5; i++ {
			engine.Append(ctx, namespace, threadID, "user", "Message", nil)
		}

		engine.SetSummarizer(NewMockSummarizer("summary"))

		// Try to summarize with keepRecent=10 (more than available)
		_, err := engine.Summarize(ctx, namespace, threadID, &SummarizeOpts{KeepRecent: 10})
		if err != ErrNothingToSummarize {
			t.Errorf("expected ErrNothingToSummarize, got %v", err)
		}
	})

	t.Run("uses default keepRecent when not specified", func(t *testing.T) {
		engine, _ := setupTestEngine(t, false)
		ctx := context.Background()
		namespace := "test-ns"
		threadID := "thread-1"

		// Add 20 messages
		for i := 0; i < 20; i++ {
			engine.Append(ctx, namespace, threadID, "user", "Message", nil)
			time.Sleep(5 * time.Millisecond)
		}

		summarizer := NewMockSummarizer("summary")
		engine.SetSummarizer(summarizer)

		result, err := engine.Summarize(ctx, namespace, threadID, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Default keepRecent is 10, so 10 should be summarized
		if result.MessagesSummarized != 10 {
			t.Errorf("expected 10 messages summarized (default keepRecent=10), got %d", result.MessagesSummarized)
		}
	})

	t.Run("includes previous summary in context", func(t *testing.T) {
		engine, backend := setupTestEngine(t, false)
		ctx := context.Background()
		namespace := "test-ns"
		threadID := "thread-1"

		// Add messages
		for i := 0; i < 20; i++ {
			engine.Append(ctx, namespace, threadID, "user", "Message", nil)
			time.Sleep(5 * time.Millisecond)
		}

		// Set an existing summary on the thread
		thread, _ := backend.GetThread(ctx, namespace, threadID)
		thread.Summary = "Previous conversation summary"
		backend.UpdateThread(ctx, thread)

		summarizer := NewMockSummarizer("New combined summary")
		engine.SetSummarizer(summarizer)

		_, err := engine.Summarize(ctx, namespace, threadID, &SummarizeOpts{KeepRecent: 10})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify the previous summary was included in the messages to summarize
		if len(summarizer.messages) == 0 {
			t.Fatal("expected messages to be passed to summarizer")
		}

		// First message should be the previous summary
		firstMsg := summarizer.messages[0]
		if firstMsg.Role != "system" {
			t.Errorf("expected first message role to be 'system', got %q", firstMsg.Role)
		}
		if firstMsg.Content != "[Previous summary]: Previous conversation summary" {
			t.Errorf("expected first message to contain previous summary, got %q", firstMsg.Content)
		}
	})
}

func TestAutoSummarization(t *testing.T) {
	t.Run("auto-triggers summarization on history call", func(t *testing.T) {
		engine, backend := setupTestEngine(t, false)
		engine.cfg.AutoSummarizeThreshold = 10 // Low threshold for testing

		ctx := context.Background()
		namespace := "test-ns"
		threadID := "thread-1"

		// Create messages with explicit timestamps (over threshold)
		baseTime := time.Now().Unix()
		for i := 0; i < 15; i++ {
			msg := &types.Message{
				ID:        "msg-auto-" + string(rune('a'+i)),
				Namespace: namespace,
				ThreadID:  threadID,
				Role:      "user",
				Content:   "Message " + string(rune('a'+i)),
				CreatedAt: time.Unix(baseTime+int64(i), 0),
			}
			if err := backend.AppendMessage(ctx, msg); err != nil {
				t.Fatalf("failed to append: %v", err)
			}
		}

		// Set up summarizer
		summarizer := NewMockSummarizer("Auto-generated summary")
		engine.SetSummarizer(summarizer)

		// Call History - should auto-trigger summarization
		result, err := engine.History(ctx, namespace, threadID, &HistoryOpts{
			IncludeSummary: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify summarization was triggered
		if summarizer.callCount != 1 {
			t.Errorf("expected summarizer to be called once, got %d", summarizer.callCount)
		}

		// Verify thread has summary
		thread, err := backend.GetThread(ctx, namespace, threadID)
		if err != nil {
			t.Fatalf("failed to get thread: %v", err)
		}
		if thread.Summary != "Auto-generated summary" {
			t.Errorf("expected thread to have auto-generated summary, got %q", thread.Summary)
		}

		// Verify result includes summary
		if result.Summary != "Auto-generated summary" {
			t.Errorf("expected result to include summary, got %q", result.Summary)
		}
	})

	t.Run("skips auto-summarization when no summarizer", func(t *testing.T) {
		engine, backend := setupTestEngine(t, false)
		engine.cfg.AutoSummarizeThreshold = 5

		ctx := context.Background()
		namespace := "test-ns"
		threadID := "thread-1"

		// Create messages over threshold
		for i := 0; i < 10; i++ {
			engine.Append(ctx, namespace, threadID, "user", "Message", nil)
		}

		// No summarizer set - should not panic or error
		result, err := engine.History(ctx, namespace, threadID, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should return messages without summary
		if len(result.Messages) == 0 {
			t.Error("expected messages to be returned")
		}

		// Thread should have no summary
		thread, _ := backend.GetThread(ctx, namespace, threadID)
		if thread.Summary != "" {
			t.Errorf("expected no summary without summarizer, got %q", thread.Summary)
		}
	})

	t.Run("respects SkipAutoSummarize option", func(t *testing.T) {
		engine, _ := setupTestEngine(t, false)
		engine.cfg.AutoSummarizeThreshold = 5

		ctx := context.Background()
		namespace := "test-ns"
		threadID := "thread-1"

		// Create messages over threshold
		for i := 0; i < 10; i++ {
			engine.Append(ctx, namespace, threadID, "user", "Message", nil)
		}

		// Set up summarizer
		summarizer := NewMockSummarizer("Summary")
		engine.SetSummarizer(summarizer)

		// Call History with SkipAutoSummarize
		_, err := engine.History(ctx, namespace, threadID, &HistoryOpts{
			SkipAutoSummarize: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Summarizer should NOT have been called
		if summarizer.callCount != 0 {
			t.Errorf("expected summarizer to not be called when SkipAutoSummarize=true, got %d calls", summarizer.callCount)
		}
	})
}

func TestShouldSummarize(t *testing.T) {
	t.Run("returns true when over threshold", func(t *testing.T) {
		engine, _ := setupTestEngine(t, false)
		engine.cfg.AutoSummarizeThreshold = 10

		ctx := context.Background()
		namespace := "test-ns"
		threadID := "thread-1"

		// Add messages over threshold
		for i := 0; i < 15; i++ {
			engine.Append(ctx, namespace, threadID, "user", "Message", nil)
		}

		should, err := engine.ShouldSummarize(ctx, namespace, threadID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !should {
			t.Error("expected ShouldSummarize to return true")
		}
	})

	t.Run("returns false when under threshold", func(t *testing.T) {
		engine, _ := setupTestEngine(t, false)
		engine.cfg.AutoSummarizeThreshold = 20

		ctx := context.Background()
		namespace := "test-ns"
		threadID := "thread-1"

		// Add fewer messages than threshold
		for i := 0; i < 10; i++ {
			engine.Append(ctx, namespace, threadID, "user", "Message", nil)
		}

		should, err := engine.ShouldSummarize(ctx, namespace, threadID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if should {
			t.Error("expected ShouldSummarize to return false")
		}
	})

	t.Run("returns false when threshold is 0", func(t *testing.T) {
		engine, _ := setupTestEngine(t, false)
		engine.cfg.AutoSummarizeThreshold = 0

		ctx := context.Background()
		namespace := "test-ns"
		threadID := "thread-1"

		for i := 0; i < 100; i++ {
			engine.Append(ctx, namespace, threadID, "user", "Message", nil)
		}

		should, err := engine.ShouldSummarize(ctx, namespace, threadID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if should {
			t.Error("expected ShouldSummarize to return false when threshold is 0 (disabled)")
		}
	})
}

// MockExtractionEnqueuer implements ExtractionEnqueuer for testing.
type MockExtractionEnqueuer struct {
	callCount int
	items     []extractionItem
}

type extractionItem struct {
	namespace  string
	sourceType string
	sourceID   string
	content    string
}

func NewMockExtractionEnqueuer() *MockExtractionEnqueuer {
	return &MockExtractionEnqueuer{}
}

func (m *MockExtractionEnqueuer) EnqueueForExtraction(ctx context.Context, namespace, sourceType, sourceID, content string) error {
	m.callCount++
	m.items = append(m.items, extractionItem{
		namespace:  namespace,
		sourceType: sourceType,
		sourceID:   sourceID,
		content:    content,
	})
	return nil
}

func (m *MockExtractionEnqueuer) CallCount() int {
	return m.callCount
}

func TestExtractionEnqueuer(t *testing.T) {
	t.Run("enqueues message for extraction on append", func(t *testing.T) {
		engine, _ := setupTestEngine(t, false)
		ctx := context.Background()
		namespace := "test-ns"
		threadID := "thread-1"

		// Set up extraction enqueuer
		enqueuer := NewMockExtractionEnqueuer()
		engine.SetExtractionEnqueuer(enqueuer)

		// Append a message
		msg, err := engine.Append(ctx, namespace, threadID, "user", "Hello, this is a test message", nil)
		if err != nil {
			t.Fatalf("failed to append: %v", err)
		}

		// Verify extraction was enqueued
		if enqueuer.callCount != 1 {
			t.Errorf("expected 1 extraction call, got %d", enqueuer.callCount)
		}

		// Verify extraction item details
		if len(enqueuer.items) != 1 {
			t.Fatalf("expected 1 extraction item, got %d", len(enqueuer.items))
		}

		item := enqueuer.items[0]
		if item.namespace != namespace {
			t.Errorf("expected namespace %q, got %q", namespace, item.namespace)
		}
		if item.sourceType != "conversation" {
			t.Errorf("expected sourceType 'conversation', got %q", item.sourceType)
		}
		if item.sourceID != msg.ID {
			t.Errorf("expected sourceID %q, got %q", msg.ID, item.sourceID)
		}
		if item.content != "Hello, this is a test message" {
			t.Errorf("expected content to match message, got %q", item.content)
		}
	})

	t.Run("does not enqueue when no enqueuer set", func(t *testing.T) {
		engine, _ := setupTestEngine(t, false)
		ctx := context.Background()

		// No enqueuer set - should not panic
		_, err := engine.Append(ctx, "test-ns", "thread-1", "user", "Hello", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("enqueues for each message", func(t *testing.T) {
		engine, _ := setupTestEngine(t, false)
		ctx := context.Background()
		namespace := "test-ns"

		enqueuer := NewMockExtractionEnqueuer()
		engine.SetExtractionEnqueuer(enqueuer)

		// Append multiple messages
		for i := 0; i < 5; i++ {
			_, err := engine.Append(ctx, namespace, "thread-1", "user", "Message", nil)
			if err != nil {
				t.Fatalf("failed to append: %v", err)
			}
		}

		if enqueuer.callCount != 5 {
			t.Errorf("expected 5 extraction calls, got %d", enqueuer.callCount)
		}
	})
}
