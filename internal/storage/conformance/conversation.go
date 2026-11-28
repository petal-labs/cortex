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

	t.Run("MarkMessagesSummarizedByIDsAndGetUnsummarized", func(t *testing.T) {
		threadID := "thread-unsummarized-ids"
		sameSec := time.Now().UTC().Truncate(time.Second)

		var ids []string
		for i := 0; i < 6; i++ {
			msg := &types.Message{
				ID:        "same-sec-msg-" + string(rune('a'+i)),
				Namespace: ns,
				ThreadID:  threadID,
				Role:      "user",
				Content:   "message " + string(rune('a'+i)),
				CreatedAt: sameSec,
			}
			if err := b.AppendMessage(ctx, msg); err != nil {
				t.Fatalf("AppendMessage %d: %v", i, err)
			}
			ids = append(ids, msg.ID)
		}

		// Mark first 4 IDs summarized; last 2 share the exact same second and must stay unsummarized.
		if err := b.MarkMessagesSummarizedByIDs(ctx, ns, threadID, ids[:4]); err != nil {
			t.Fatalf("MarkMessagesSummarizedByIDs: %v", err)
		}

		unsummarized, _, err := b.GetUnsummarizedMessages(ctx, ns, threadID, 0, "")
		if err != nil {
			t.Fatalf("GetUnsummarizedMessages: %v", err)
		}
		if len(unsummarized) != 2 {
			t.Fatalf("expected 2 unsummarized messages, got %d", len(unsummarized))
		}
		for _, msg := range unsummarized {
			if msg.Summarized {
				t.Errorf("expected message %s to be unsummarized", msg.ID)
			}
		}

		// Limit parameter should cap results while returning only unsummarized messages.
		limited, _, err := b.GetUnsummarizedMessages(ctx, ns, threadID, 1, "")
		if err != nil {
			t.Fatalf("GetUnsummarizedMessages(limit=1): %v", err)
		}
		if len(limited) != 1 {
			t.Fatalf("expected 1 unsummarized message with limit=1, got %d", len(limited))
		}

		// Empty slice should be a no-op.
		if err := b.MarkMessagesSummarizedByIDs(ctx, ns, threadID, nil); err != nil {
			t.Fatalf("MarkMessagesSummarizedByIDs(nil): %v", err)
		}
	})

	t.Run("SameSecondCursorPagination", func(t *testing.T) {
		threadID := "thread-same-sec-page"
		sameSec := time.Now().UTC().Truncate(time.Second)
		for i := 0; i < 5; i++ {
			msg := &types.Message{
				ID:        "page-msg-" + string(rune('a'+i)),
				Namespace: ns,
				ThreadID:  threadID,
				Role:      "user",
				Content:   "turn " + string(rune('a'+i)),
				CreatedAt: sameSec,
			}
			if err := b.AppendMessage(ctx, msg); err != nil {
				t.Fatalf("AppendMessage %d: %v", i, err)
			}
		}

		var collected []string
		cursor := ""
		for pages := 0; pages < 10; pages++ {
			msgs, next, err := b.GetMessages(ctx, ns, threadID, 2, cursor)
			if err != nil {
				t.Fatalf("GetMessages page %d: %v", pages, err)
			}
			for _, m := range msgs {
				collected = append(collected, m.ID)
			}
			if next == "" {
				break
			}
			cursor = next
		}
		if len(collected) != 5 {
			t.Fatalf("expected 5 paginated messages across same-second pages, got %d (%v)", len(collected), collected)
		}
	})

	t.Run("DeleteVoiceSessionNamespaceScoped", func(t *testing.T) {
		const nsA = "conf-voice-a"
		const nsB = "conf-voice-b"
		const sessID = "voice-sess-shared"

		for _, m := range []*types.Message{
			{
				Namespace: nsA, ThreadID: "t-voice-a", Role: "user", Content: "hello a",
				Metadata: map[string]string{types.VoiceMetaSessionID: sessID, types.VoiceMetaFinal: "true"},
			},
			{
				Namespace: nsA, ThreadID: "t-voice-a", Role: "assistant", Content: "hi a",
				Metadata: map[string]string{types.VoiceMetaSessionID: sessID, types.VoiceMetaFinal: "true"},
			},
			{
				Namespace: nsA, ThreadID: "t-mixed-a", Role: "user", Content: "text message",
			},
			{
				Namespace: nsA, ThreadID: "t-mixed-a", Role: "user", Content: "voice in mixed",
				Metadata: map[string]string{types.VoiceMetaSessionID: sessID, types.VoiceMetaFinal: "true"},
			},
			{
				Namespace: nsB, ThreadID: "t-voice-b", Role: "user", Content: "hello b",
				Metadata: map[string]string{types.VoiceMetaSessionID: sessID, types.VoiceMetaFinal: "true"},
			},
		} {
			if err := b.AppendMessage(ctx, m); err != nil {
				t.Fatalf("AppendMessage: %v", err)
			}
		}

		deleted, err := b.DeleteVoiceSession(ctx, nsA, sessID)
		if err != nil {
			t.Fatalf("DeleteVoiceSession: %v", err)
		}
		if deleted != 3 {
			t.Fatalf("expected 3 deleted voice messages in nsA, got %d", deleted)
		}

		// Empty voice-only thread should be removed.
		if _, err := b.GetThread(ctx, nsA, "t-voice-a"); err != storage.ErrNotFound {
			t.Errorf("expected empty voice thread to be deleted, got %v", err)
		}
		// Mixed thread should retain its non-voice message.
		mixedMsgs, _, err := b.GetMessages(ctx, nsA, "t-mixed-a", 10, "")
		if err != nil {
			t.Fatalf("GetMessages mixed: %v", err)
		}
		if len(mixedMsgs) != 1 || mixedMsgs[0].Content != "text message" {
			t.Errorf("expected non-voice message preserved in mixed thread, got %+v", mixedMsgs)
		}
		// Other namespace with identical session_id must remain untouched.
		nsBMsg, _, err := b.GetMessages(ctx, nsB, "t-voice-b", 10, "")
		if err != nil {
			t.Fatalf("GetMessages nsB: %v", err)
		}
		if len(nsBMsg) != 1 || nsBMsg[0].Content != "hello b" {
			t.Errorf("expected nsB voice message untouched, got %+v", nsBMsg)
		}
	})
}
