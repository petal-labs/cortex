package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

func testConversation(ctx context.Context, t *testing.T, b storage.Backend, dims int) {
	const ns = "conf-conv"

	t.Run("AppendAndGetMessages", func(t *testing.T) {
		threadID := "thread-append"

		msgs := []*types.Message{
			{Namespace: ns, ThreadID: threadID, Role: "user", Content: "Hello, world!", Metadata: map[string]string{"key": "value"}},
			{Namespace: ns, ThreadID: threadID, Role: "assistant", Content: "Hi there!"},
		}
		for _, msg := range msgs {
			if err := b.AppendMessage(ctx, msg); err != nil {
				t.Fatalf("AppendMessage: %v", err)
			}
			if msg.ID == "" {
				t.Error("expected message ID to be generated")
			}
		}

		got, _, err := b.GetMessages(ctx, ns, threadID, 10, "")
		if err != nil {
			t.Fatalf("GetMessages: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(got))
		}
		if got[0].Metadata["key"] != "value" {
			t.Errorf("expected metadata key=value, got %v", got[0].Metadata)
		}
	})

	t.Run("GetThread", func(t *testing.T) {
		threadID := "thread-get"
		if err := b.AppendMessage(ctx, &types.Message{Namespace: ns, ThreadID: threadID, Role: "user", Content: "hi"}); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
		thread, err := b.GetThread(ctx, ns, threadID)
		if err != nil {
			t.Fatalf("GetThread: %v", err)
		}
		if thread.ID != threadID {
			t.Errorf("expected thread ID %s, got %s", threadID, thread.ID)
		}
	})

	t.Run("GetThreadNotFound", func(t *testing.T) {
		_, err := b.GetThread(ctx, ns, "nonexistent")
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("UpdateThread", func(t *testing.T) {
		threadID := "thread-update"
		if err := b.AppendMessage(ctx, &types.Message{Namespace: ns, ThreadID: threadID, Role: "user", Content: "hi"}); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
		thread, _ := b.GetThread(ctx, ns, threadID)
		thread.Title = "Updated Title"
		thread.Summary = "A summary"
		if err := b.UpdateThread(ctx, thread); err != nil {
			t.Fatalf("UpdateThread: %v", err)
		}
		got, err := b.GetThread(ctx, ns, threadID)
		if err != nil {
			t.Fatalf("GetThread: %v", err)
		}
		if got.Title != "Updated Title" {
			t.Errorf("expected title 'Updated Title', got %q", got.Title)
		}
		if got.Summary != "A summary" {
			t.Errorf("expected summary 'A summary', got %q", got.Summary)
		}
	})

	t.Run("ListThreads", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			if err := b.AppendMessage(ctx, &types.Message{
				Namespace: ns,
				ThreadID:  "list-thread-" + string(rune('A'+i)),
				Role:      "user",
				Content:   "msg",
			}); err != nil {
				t.Fatalf("AppendMessage: %v", err)
			}
		}
		threads, _, err := b.ListThreads(ctx, ns, "", 100)
		if err != nil {
			t.Fatalf("ListThreads: %v", err)
		}
		if len(threads) < 3 {
			t.Errorf("expected at least 3 threads, got %d", len(threads))
		}
	})

	t.Run("DeleteThread", func(t *testing.T) {
		threadID := "thread-delete"
		if err := b.AppendMessage(ctx, &types.Message{Namespace: ns, ThreadID: threadID, Role: "user", Content: "hi"}); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
		if err := b.DeleteThread(ctx, ns, threadID); err != nil {
			t.Fatalf("DeleteThread: %v", err)
		}
		_, err := b.GetThread(ctx, ns, threadID)
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound after delete, got %v", err)
		}
	})

	t.Run("StoreMessageEmbeddingAndSearch", func(t *testing.T) {
		threadID := "thread-search"
		msgs := []*types.Message{
			{ID: "smsg-1", Namespace: ns, ThreadID: threadID, Role: "user", Content: "Hello, how are you?"},
			{ID: "smsg-2", Namespace: ns, ThreadID: threadID, Role: "assistant", Content: "I am doing well, thank you!"},
			{ID: "smsg-3", Namespace: ns, ThreadID: threadID, Role: "user", Content: "Tell me about machine learning"},
		}
		embeddings := [][]float32{
			createTestEmbedding(0.1, dims),
			createTestEmbedding(0.15, dims),
			createTestEmbedding(0.9, dims),
		}
		for i, msg := range msgs {
			if err := b.AppendMessage(ctx, msg); err != nil {
				t.Fatalf("AppendMessage: %v", err)
			}
			if err := b.StoreMessageEmbedding(ctx, msg.ID, embeddings[i]); err != nil {
				t.Fatalf("StoreMessageEmbedding: %v", err)
			}
		}

		// Search with an embedding similar to the greeting messages.
		query := createTestEmbedding(0.12, dims)
		results, err := b.SearchMessages(ctx, ns, query, storage.MessageSearchOpts{TopK: 10})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}
		// The greeting messages (smsg-1, smsg-2) should rank higher than the ML message.
		top := results[0].Message.ID
		if top != "smsg-1" && top != "smsg-2" {
			t.Errorf("expected first result to be a greeting message, got %s", top)
		}
	})

	t.Run("SearchMessagesThreadFilter", func(t *testing.T) {
		threadID := "thread-filter"
		if err := b.AppendMessage(ctx, &types.Message{ID: "fmsg-1", Namespace: ns, ThreadID: threadID, Role: "user", Content: "filtered"}); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
		if err := b.StoreMessageEmbedding(ctx, "fmsg-1", createTestEmbedding(0.1, dims)); err != nil {
			t.Fatalf("StoreMessageEmbedding: %v", err)
		}
		// Also store an embedding in the search thread from the previous test.
		if err := b.StoreMessageEmbedding(ctx, "smsg-1", createTestEmbedding(0.1, dims)); err != nil {
			t.Fatalf("StoreMessageEmbedding: %v", err)
		}

		results, err := b.SearchMessages(ctx, ns, createTestEmbedding(0.12, dims), storage.MessageSearchOpts{
			TopK:     10,
			ThreadID: &threadID,
		})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		for _, r := range results {
			if r.Message.ThreadID != threadID {
				t.Errorf("expected results only from thread %s, got %s", threadID, r.Message.ThreadID)
			}
		}
	})

	t.Run("MarkMessagesSummarized", func(t *testing.T) {
		threadID := "thread-summarized"
		if err := b.AppendMessage(ctx, &types.Message{Namespace: ns, ThreadID: threadID, Role: "user", Content: "summarize me"}); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
		if err := b.MarkMessagesSummarized(ctx, ns, threadID, time.Now().Add(1*time.Hour).Unix()); err != nil {
			t.Fatalf("MarkMessagesSummarized: %v", err)
		}
		msgs, _, err := b.GetMessages(ctx, ns, threadID, 10, "")
		if err != nil {
			t.Fatalf("GetMessages: %v", err)
		}
		for _, msg := range msgs {
			if !msg.Summarized {
				t.Errorf("expected message %s to be summarized", msg.ID)
			}
		}
	})
}
