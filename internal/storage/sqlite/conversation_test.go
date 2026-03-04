package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

func TestAppendMessage(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	msg := &types.Message{
		Namespace: "test-ns",
		ThreadID:  "thread-1",
		Role:      "user",
		Content:   "Hello, world!",
		Metadata:  map[string]string{"key": "value"},
	}

	// Append message (should create thread)
	if err := backend.AppendMessage(ctx, msg); err != nil {
		t.Fatalf("failed to append message: %v", err)
	}

	// Verify message was created
	if msg.ID == "" {
		t.Error("expected message ID to be generated")
	}

	// Verify thread was created
	thread, err := backend.GetThread(ctx, "test-ns", "thread-1")
	if err != nil {
		t.Fatalf("failed to get thread: %v", err)
	}
	if thread.ID != "thread-1" {
		t.Errorf("expected thread ID 'thread-1', got '%s'", thread.ID)
	}

	// Append another message to existing thread
	msg2 := &types.Message{
		Namespace: "test-ns",
		ThreadID:  "thread-1",
		Role:      "assistant",
		Content:   "Hello back!",
	}
	if err := backend.AppendMessage(ctx, msg2); err != nil {
		t.Fatalf("failed to append second message: %v", err)
	}

	// Get messages
	messages, _, err := backend.GetMessages(ctx, "test-ns", "thread-1", 10, "")
	if err != nil {
		t.Fatalf("failed to get messages: %v", err)
	}
	if len(messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(messages))
	}
}

func TestGetMessages(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create 5 messages with different timestamps
	for i := 0; i < 5; i++ {
		msg := &types.Message{
			Namespace: "test-ns",
			ThreadID:  "thread-1",
			Role:      "user",
			Content:   "Message " + string(rune('A'+i)),
			CreatedAt: time.Now().Add(time.Duration(i) * time.Second),
		}
		if err := backend.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("failed to append message %d: %v", i, err)
		}
		// Small delay to ensure different timestamps
		time.Sleep(10 * time.Millisecond)
	}

	// Get all messages
	messages, _, err := backend.GetMessages(ctx, "test-ns", "thread-1", 10, "")
	if err != nil {
		t.Fatalf("failed to get messages: %v", err)
	}
	if len(messages) != 5 {
		t.Errorf("expected 5 messages, got %d", len(messages))
	}

	// Verify messages are in chronological order (oldest first)
	for i := 0; i < len(messages)-1; i++ {
		if messages[i].CreatedAt.After(messages[i+1].CreatedAt) {
			t.Error("messages are not in chronological order")
		}
	}

	// Test pagination - get only 3
	messages, cursor, err := backend.GetMessages(ctx, "test-ns", "thread-1", 3, "")
	if err != nil {
		t.Fatalf("failed to get messages with limit: %v", err)
	}
	if len(messages) != 3 {
		t.Errorf("expected 3 messages with limit, got %d", len(messages))
	}
	if cursor == "" {
		t.Error("expected non-empty cursor for pagination")
	}
}

func TestListThreads(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create messages in different threads with explicit timestamps
	baseTime := time.Now().Unix()
	threads := []string{"thread-1", "thread-2", "thread-3"}
	for i, threadID := range threads {
		msg := &types.Message{
			Namespace: "test-ns",
			ThreadID:  threadID,
			Role:      "user",
			Content:   "Hello in " + threadID,
			CreatedAt: time.Unix(baseTime+int64(i), 0), // Ensure different timestamps
		}
		if err := backend.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("failed to append message: %v", err)
		}
	}

	// List threads
	result, _, err := backend.ListThreads(ctx, "test-ns", "", 10)
	if err != nil {
		t.Fatalf("failed to list threads: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("expected 3 threads, got %d", len(result))
	}

	// Threads should be ordered by updated_at DESC (most recent first)
	// thread-3 was created last, so it should be first
	if result[0].ID != "thread-3" {
		t.Errorf("expected first thread to be 'thread-3', got '%s'", result[0].ID)
	}
}

func TestUpdateThread(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create a thread by adding a message
	msg := &types.Message{
		Namespace: "test-ns",
		ThreadID:  "thread-1",
		Role:      "user",
		Content:   "Hello",
	}
	if err := backend.AppendMessage(ctx, msg); err != nil {
		t.Fatalf("failed to append message: %v", err)
	}

	// Get the thread
	thread, err := backend.GetThread(ctx, "test-ns", "thread-1")
	if err != nil {
		t.Fatalf("failed to get thread: %v", err)
	}

	// Update thread
	thread.Title = "My Conversation"
	thread.Summary = "A test conversation"
	thread.Metadata = map[string]string{"topic": "testing"}

	if err := backend.UpdateThread(ctx, thread); err != nil {
		t.Fatalf("failed to update thread: %v", err)
	}

	// Verify update
	updated, err := backend.GetThread(ctx, "test-ns", "thread-1")
	if err != nil {
		t.Fatalf("failed to get updated thread: %v", err)
	}
	if updated.Title != "My Conversation" {
		t.Errorf("expected title 'My Conversation', got '%s'", updated.Title)
	}
	if updated.Summary != "A test conversation" {
		t.Errorf("expected summary 'A test conversation', got '%s'", updated.Summary)
	}
	if updated.Metadata["topic"] != "testing" {
		t.Errorf("expected metadata topic 'testing', got '%s'", updated.Metadata["topic"])
	}
}

func TestDeleteThread(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create thread with messages
	for i := 0; i < 3; i++ {
		msg := &types.Message{
			Namespace: "test-ns",
			ThreadID:  "thread-1",
			Role:      "user",
			Content:   "Message",
		}
		if err := backend.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("failed to append message: %v", err)
		}
	}

	// Delete thread
	if err := backend.DeleteThread(ctx, "test-ns", "thread-1"); err != nil {
		t.Fatalf("failed to delete thread: %v", err)
	}

	// Verify thread is gone
	_, err := backend.GetThread(ctx, "test-ns", "thread-1")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	// Verify messages are also deleted (cascade)
	messages, _, err := backend.GetMessages(ctx, "test-ns", "thread-1", 10, "")
	if err != nil {
		t.Fatalf("failed to get messages: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("expected 0 messages after delete, got %d", len(messages))
	}
}

func TestDeleteThreadNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	err := backend.DeleteThread(ctx, "test-ns", "nonexistent")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreMessageEmbedding(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create a message
	msg := &types.Message{
		ID:        "msg-1",
		Namespace: "test-ns",
		ThreadID:  "thread-1",
		Role:      "user",
		Content:   "Hello",
	}
	if err := backend.AppendMessage(ctx, msg); err != nil {
		t.Fatalf("failed to append message: %v", err)
	}

	// Store embedding
	embedding := make([]float32, 1536)
	for i := range embedding {
		embedding[i] = float32(i) * 0.001
	}

	if err := backend.StoreMessageEmbedding(ctx, "msg-1", embedding); err != nil {
		t.Fatalf("failed to store embedding: %v", err)
	}

	// Verify embedding was stored (query directly)
	var count int
	err := backend.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM message_embeddings WHERE message_id = ?", "msg-1").Scan(&count)
	if err != nil {
		t.Fatalf("failed to query embeddings: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 embedding, got %d", count)
	}
}

func TestMarkMessagesSummarized(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create 5 messages
	baseTime := time.Now().Unix()
	for i := 0; i < 5; i++ {
		msg := &types.Message{
			Namespace: "test-ns",
			ThreadID:  "thread-1",
			Role:      "user",
			Content:   "Message",
			CreatedAt: time.Unix(baseTime+int64(i), 0),
		}
		if err := backend.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("failed to append message: %v", err)
		}
	}

	// Mark messages before timestamp 3 as summarized
	if err := backend.MarkMessagesSummarized(ctx, "test-ns", "thread-1", baseTime+3); err != nil {
		t.Fatalf("failed to mark messages summarized: %v", err)
	}

	// Count summarized messages
	var summarizedCount int
	err := backend.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM messages WHERE namespace = ? AND thread_id = ? AND summarized = 1",
		"test-ns", "thread-1",
	).Scan(&summarizedCount)
	if err != nil {
		t.Fatalf("failed to count summarized: %v", err)
	}

	// Messages 0, 1, 2 should be summarized (created_at < baseTime+3)
	if summarizedCount != 3 {
		t.Errorf("expected 3 summarized messages, got %d", summarizedCount)
	}
}

func TestMessageMetadata(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create message with metadata
	msg := &types.Message{
		Namespace: "test-ns",
		ThreadID:  "thread-1",
		Role:      "user",
		Content:   "Hello",
		Metadata: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	}
	if err := backend.AppendMessage(ctx, msg); err != nil {
		t.Fatalf("failed to append message: %v", err)
	}

	// Retrieve message
	messages, _, err := backend.GetMessages(ctx, "test-ns", "thread-1", 10, "")
	if err != nil {
		t.Fatalf("failed to get messages: %v", err)
	}

	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}

	retrieved := messages[0]
	if retrieved.Metadata == nil {
		t.Fatal("expected metadata to be present")
	}
	if retrieved.Metadata["key1"] != "value1" {
		t.Errorf("expected metadata key1='value1', got '%s'", retrieved.Metadata["key1"])
	}
	if retrieved.Metadata["key2"] != "value2" {
		t.Errorf("expected metadata key2='value2', got '%s'", retrieved.Metadata["key2"])
	}
}
