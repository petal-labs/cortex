package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

func testContext(ctx context.Context, t *testing.T, b storage.Backend) {
	const ns = "conf-ctx"

	t.Run("SetAndGet", func(t *testing.T) {
		entry := &types.ContextEntry{
			Namespace: ns,
			Key:       "config.theme",
			Value:     map[string]any{"color": "dark", "size": 14},
			UpdatedBy: "test-agent",
		}
		if err := b.SetContext(ctx, entry, nil); err != nil {
			t.Fatalf("SetContext: %v", err)
		}
		if entry.Version != 1 {
			t.Errorf("expected version 1, got %d", entry.Version)
		}

		got, err := b.GetContext(ctx, ns, "config.theme", nil)
		if err != nil {
			t.Fatalf("GetContext: %v", err)
		}
		if got.Key != "config.theme" {
			t.Errorf("expected key 'config.theme', got %q", got.Key)
		}
		if got.Version != 1 {
			t.Errorf("expected version 1, got %d", got.Version)
		}
		if got.UpdatedBy != "test-agent" {
			t.Errorf("expected updated_by 'test-agent', got %q", got.UpdatedBy)
		}
		val, ok := got.Value.(map[string]any)
		if !ok {
			t.Fatalf("expected value to be map[string]any, got %T", got.Value)
		}
		if val["color"] != "dark" {
			t.Errorf("expected color 'dark', got %v", val["color"])
		}
	})

	t.Run("Versioning", func(t *testing.T) {
		entry := &types.ContextEntry{
			Namespace: ns,
			Key:       "counter",
			Value:     1,
		}
		if err := b.SetContext(ctx, entry, nil); err != nil {
			t.Fatalf("SetContext: %v", err)
		}
		if entry.Version != 1 {
			t.Fatalf("expected version 1, got %d", entry.Version)
		}
		entry.Value = 2
		if err := b.SetContext(ctx, entry, nil); err != nil {
			t.Fatalf("SetContext update: %v", err)
		}
		if entry.Version != 2 {
			t.Errorf("expected version 2, got %d", entry.Version)
		}
	})

	t.Run("OptimisticConcurrency", func(t *testing.T) {
		entry := &types.ContextEntry{
			Namespace: ns,
			Key:       "guarded",
			Value:     "initial",
		}
		if err := b.SetContext(ctx, entry, nil); err != nil {
			t.Fatalf("SetContext: %v", err)
		}

		// Wrong expected version should fail.
		wrong := int64(99)
		err := b.SetContext(ctx, entry, &wrong)
		if err != storage.ErrVersionConflict {
			t.Errorf("expected ErrVersionConflict, got %v", err)
		}

		// Correct expected version should succeed.
		correct := int64(1)
		entry.Value = "updated"
		if err := b.SetContext(ctx, entry, &correct); err != nil {
			t.Fatalf("SetContext with correct version: %v", err)
		}
	})

	t.Run("RunScoped", func(t *testing.T) {
		runID := "run-123"
		entry := &types.ContextEntry{
			Namespace: ns,
			Key:       "run-state",
			RunID:     &runID,
			Value:     "running",
		}
		if err := b.SetContext(ctx, entry, nil); err != nil {
			t.Fatalf("SetContext: %v", err)
		}

		// Get with the same runID.
		got, err := b.GetContext(ctx, ns, "run-state", &runID)
		if err != nil {
			t.Fatalf("GetContext: %v", err)
		}
		if got.Value != "running" {
			t.Errorf("expected 'running', got %v", got.Value)
		}

		// Get without runID should not find the run-scoped entry.
		_, err = b.GetContext(ctx, ns, "run-state", nil)
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound for non-run-scoped lookup, got %v", err)
		}
	})

	t.Run("TTLAndCleanup", func(t *testing.T) {
		past := time.Now().Add(-1 * time.Hour)
		entry := &types.ContextEntry{
			Namespace:    ns,
			Key:          "expired-key",
			Value:        "stale",
			TTLExpiresAt: &past,
		}
		if err := b.SetContext(ctx, entry, nil); err != nil {
			t.Fatalf("SetContext: %v", err)
		}

		deleted, err := b.CleanupExpiredContext(ctx)
		if err != nil {
			t.Fatalf("CleanupExpiredContext: %v", err)
		}
		if deleted < 1 {
			t.Errorf("expected at least 1 expired entry deleted, got %d", deleted)
		}

		_, err = b.GetContext(ctx, ns, "expired-key", nil)
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound after TTL cleanup, got %v", err)
		}
	})

	t.Run("ListKeys", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			if err := b.SetContext(ctx, &types.ContextEntry{
				Namespace: ns,
				Key:       "list-key-" + string(rune('A'+i)),
				Value:     i,
			}, nil); err != nil {
				t.Fatalf("SetContext: %v", err)
			}
		}
		keys, _, err := b.ListContextKeys(ctx, ns, nil, nil, "", 100)
		if err != nil {
			t.Fatalf("ListContextKeys: %v", err)
		}
		if len(keys) < 3 {
			t.Errorf("expected at least 3 keys, got %d", len(keys))
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := b.SetContext(ctx, &types.ContextEntry{
			Namespace: ns,
			Key:       "delete-me",
			Value:     "bye",
		}, nil); err != nil {
			t.Fatalf("SetContext: %v", err)
		}
		if err := b.DeleteContext(ctx, ns, "delete-me", nil); err != nil {
			t.Fatalf("DeleteContext: %v", err)
		}
		_, err := b.GetContext(ctx, ns, "delete-me", nil)
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound after delete, got %v", err)
		}
	})

	t.Run("History", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			if err := b.SetContext(ctx, &types.ContextEntry{
				Namespace: ns,
				Key:       "history-key",
				Value:     i,
			}, nil); err != nil {
				t.Fatalf("SetContext %d: %v", i, err)
			}
		}
		history, _, err := b.GetContextHistory(ctx, ns, "history-key", nil, "", 100)
		if err != nil {
			t.Fatalf("GetContextHistory: %v", err)
		}
		if len(history) < 3 {
			t.Errorf("expected at least 3 history entries, got %d", len(history))
		}
	})

	t.Run("CleanupRunContext", func(t *testing.T) {
		runID := "run-cleanup"
		if err := b.SetContext(ctx, &types.ContextEntry{
			Namespace: ns,
			Key:       "run-cleanup-key",
			RunID:     &runID,
			Value:     "temp",
		}, nil); err != nil {
			t.Fatalf("SetContext: %v", err)
		}
		if err := b.CleanupRunContext(ctx, ns, runID); err != nil {
			t.Fatalf("CleanupRunContext: %v", err)
		}
		_, err := b.GetContext(ctx, ns, "run-cleanup-key", &runID)
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound after run cleanup, got %v", err)
		}
	})
}
