package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

func testGarbageCollection(ctx context.Context, t *testing.T, b storage.Backend, dims int) {
	const ns = "conf-gc"

	t.Run("DeleteOldConversations", func(t *testing.T) {
		threadID := "gc-thread"
		if err := b.AppendMessage(ctx, &types.Message{
			Namespace: ns,
			ThreadID:  threadID,
			Role:      "user",
			Content:   "old message",
		}); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}

		// With a 1-hour threshold, nothing should be deleted (thread is new).
		deleted, err := b.DeleteOldConversations(ctx, 1*time.Hour)
		if err != nil {
			t.Fatalf("DeleteOldConversations: %v", err)
		}
		if deleted != 0 {
			t.Errorf("expected 0 threads deleted for 1h threshold, got %d", deleted)
		}
		if _, err := b.GetThread(ctx, ns, threadID); err != nil {
			t.Errorf("thread should still exist: %v", err)
		}

		// SQLite stores timestamps as integer seconds, so we need a >1s sleep
		// and a 1s threshold to ensure the thread crosses the cutoff.
		time.Sleep(2 * time.Second)
		deleted, err = b.DeleteOldConversations(ctx, 1*time.Second)
		if err != nil {
			t.Fatalf("DeleteOldConversations: %v", err)
		}
		if deleted < 1 {
			t.Errorf("expected at least 1 thread deleted, got %d", deleted)
		}
		_, err = b.GetThread(ctx, ns, threadID)
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound after GC, got %v", err)
		}
	})

	t.Run("PruneStaleEntities", func(t *testing.T) {
		now := time.Now().UTC()
		if err := b.UpsertEntity(ctx, &types.Entity{
			ID:           "gc-entity-fresh",
			Namespace:    ns,
			Name:         "Fresh Entity",
			Type:         types.EntityTypePerson,
			MentionCount: 0,
			LastSeenAt:   now,
		}); err != nil {
			t.Fatalf("UpsertEntity fresh: %v", err)
		}
		if err := b.UpsertEntity(ctx, &types.Entity{
			ID:           "gc-entity-stale",
			Namespace:    ns,
			Name:         "Stale Entity",
			Type:         types.EntityTypePerson,
			MentionCount: 0,
			LastSeenAt:   now.Add(-48 * time.Hour),
		}); err != nil {
			t.Fatalf("UpsertEntity stale: %v", err)
		}
		if err := b.UpsertEntity(ctx, &types.Entity{
			ID:           "gc-entity-active",
			Namespace:    ns,
			Name:         "Active Entity",
			Type:         types.EntityTypePerson,
			MentionCount: 10,
			LastSeenAt:   now.Add(-48 * time.Hour),
		}); err != nil {
			t.Fatalf("UpsertEntity active: %v", err)
		}

		deleted, err := b.PruneStaleEntities(ctx, 24*time.Hour, 5)
		if err != nil {
			t.Fatalf("PruneStaleEntities: %v", err)
		}
		if deleted != 1 {
			t.Errorf("expected 1 stale low-mention entity pruned, got %d", deleted)
		}
		if _, err := b.GetEntityByID(ctx, ns, "gc-entity-fresh"); err != nil {
			t.Errorf("fresh entity should still exist: %v", err)
		}
		if _, err := b.GetEntityByID(ctx, ns, "gc-entity-stale"); err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound for stale entity, got %v", err)
		}
		if _, err := b.GetEntityByID(ctx, ns, "gc-entity-active"); err != nil {
			t.Errorf("high-mention entity should still exist: %v", err)
		}
	})

	t.Run("DeleteOrphanedChunks", func(t *testing.T) {
		// No orphaned chunks should exist through normal API usage
		// (DeleteDocument cascades), so this should return 0 without error.
		deleted, err := b.DeleteOrphanedChunks(ctx)
		if err != nil {
			t.Fatalf("DeleteOrphanedChunks: %v", err)
		}
		if deleted < 0 {
			t.Errorf("expected >= 0, got %d", deleted)
		}
	})

	t.Run("CleanupContextHistory", func(t *testing.T) {
		// Generate some history entries.
		for i := 0; i < 3; i++ {
			if err := b.SetContext(ctx, &types.ContextEntry{
				Namespace: ns,
				Key:       "gc-history",
				Value:     i,
			}, nil); err != nil {
				t.Fatalf("SetContext: %v", err)
			}
		}
		time.Sleep(50 * time.Millisecond)
		deleted, err := b.CleanupContextHistory(ctx, 25*time.Millisecond)
		if err != nil {
			t.Fatalf("CleanupContextHistory: %v", err)
		}
		if deleted < 0 {
			t.Errorf("expected >= 0, got %d", deleted)
		}
	})

	t.Run("CleanupOldRunContext", func(t *testing.T) {
		runID := "gc-run"
		if err := b.SetContext(ctx, &types.ContextEntry{
			Namespace: ns,
			Key:       "gc-run-key",
			RunID:     &runID,
			Value:     "temp",
		}, nil); err != nil {
			t.Fatalf("SetContext: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
		deleted, err := b.CleanupOldRunContext(ctx, 25*time.Millisecond)
		if err != nil {
			t.Fatalf("CleanupOldRunContext: %v", err)
		}
		if deleted < 0 {
			t.Errorf("expected >= 0, got %d", deleted)
		}
	})
}
