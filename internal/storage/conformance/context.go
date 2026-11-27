package conformance

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

	t.Run("CleanupRunContextEmptyRunIDGuard", func(t *testing.T) {
		persistentKey := "persistent-protected-key"
		if err := b.SetContext(ctx, &types.ContextEntry{
			Namespace: ns,
			Key:       persistentKey,
			Value:     "must-survive",
		}, nil); err != nil {
			t.Fatalf("SetContext persistent: %v", err)
		}

		// Calling CleanupRunContext with empty runID must fail and NOT delete persistent entries.
		err := b.CleanupRunContext(ctx, ns, "")
		if err == nil {
			t.Fatal("expected error from CleanupRunContext with empty runID, got nil")
		}
		if !errors.Is(err, storage.ErrInvalidInput) {
			t.Errorf("expected ErrInvalidInput, got %v", err)
		}

		got, err := b.GetContext(ctx, ns, persistentKey, nil)
		if err != nil {
			t.Fatalf("persistent context entry was wiped by CleanupRunContext(\"\"): %v", err)
		}
		if got.Value != "must-survive" {
			t.Errorf("expected 'must-survive', got %v", got.Value)
		}
	})

	t.Run("TTLReadExpiryBeforeGC", func(t *testing.T) {
		const ttlNS = "conf-ctx-ttl-read"
		past := time.Now().UTC().Add(-1 * time.Minute)
		future := time.Now().UTC().Add(1 * time.Hour)

		if err := b.SetContext(ctx, &types.ContextEntry{
			Namespace:    ttlNS,
			Key:          "expired-before-gc",
			Value:        "stale",
			TTLExpiresAt: &past,
		}, nil); err != nil {
			t.Fatalf("SetContext expired: %v", err)
		}
		if err := b.SetContext(ctx, &types.ContextEntry{
			Namespace:    ttlNS,
			Key:          "live-key",
			Value:        "fresh",
			TTLExpiresAt: &future,
		}, nil); err != nil {
			t.Fatalf("SetContext live: %v", err)
		}

		// Without calling CleanupExpiredContext, GetContext must immediately return ErrNotFound.
		if _, err := b.GetContext(ctx, ttlNS, "expired-before-gc", nil); err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound for expired entry before GC, got %v", err)
		}

		// ListContextKeys must exclude the expired entry before GC runs.
		keys, _, err := b.ListContextKeys(ctx, ttlNS, nil, nil, "", 100)
		if err != nil {
			t.Fatalf("ListContextKeys: %v", err)
		}
		if len(keys) != 1 || keys[0] != "live-key" {
			t.Errorf("expected only ['live-key'] from ListContextKeys, got %v", keys)
		}

		// SetContext treats the expired row as non-existent: expectedVersion=1 conflicts, expectedVersion=0 succeeds.
		v1 := int64(1)
		if err := b.SetContext(ctx, &types.ContextEntry{
			Namespace: ttlNS,
			Key:       "expired-before-gc",
			Value:     "should-fail",
		}, &v1); err != storage.ErrVersionConflict {
			t.Errorf("expected ErrVersionConflict when updating expired key with expectedVersion=1, got %v", err)
		}

		v0 := int64(0)
		recreated := &types.ContextEntry{
			Namespace: ttlNS,
			Key:       "expired-before-gc",
			Value:     "reborn",
		}
		if err := b.SetContext(ctx, recreated, &v0); err != nil {
			t.Fatalf("expected SetContext(expectedVersion=0) on expired key to succeed, got %v", err)
		}
		got, err := b.GetContext(ctx, ttlNS, "expired-before-gc", nil)
		if err != nil {
			t.Fatalf("GetContext recreated key: %v", err)
		}
		if got.Value != "reborn" || got.Version != 1 {
			t.Errorf("expected recreated value='reborn' version=1, got value=%v version=%d", got.Value, got.Version)
		}
	})

	t.Run("ConcurrentOptimisticConcurrency", func(t *testing.T) {
		const concNS = "conf-ctx-conc"
		const workers = 10

		// Phase 1: N goroutines race to create the same key with expectedVersion=0.
		// Exactly 1 must succeed and N-1 must return ErrVersionConflict.
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make(chan error, workers)

		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				<-start
				v0 := int64(0)
				err := b.SetContext(ctx, &types.ContextEntry{
					Namespace: concNS,
					Key:       "race-key",
					Value:     fmt.Sprintf("init-%d", id),
				}, &v0)
				results <- err
			}(i)
		}
		close(start)
		wg.Wait()
		close(results)

		successes, conflicts := 0, 0
		for err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, storage.ErrVersionConflict):
				conflicts++
			default:
				t.Errorf("unexpected error during concurrent create: %v", err)
			}
		}
		if successes != 1 || conflicts != workers-1 {
			t.Fatalf("concurrent create: expected 1 success and %d conflicts, got %d successes and %d conflicts",
				workers-1, successes, conflicts)
		}

		// Phase 2: N goroutines race to update version 1 -> 2.
		start2 := make(chan struct{})
		results2 := make(chan error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				<-start2
				v1 := int64(1)
				err := b.SetContext(ctx, &types.ContextEntry{
					Namespace: concNS,
					Key:       "race-key",
					Value:     fmt.Sprintf("update-%d", id),
				}, &v1)
				results2 <- err
			}(i)
		}
		close(start2)
		wg.Wait()
		close(results2)

		successes, conflicts = 0, 0
		for err := range results2 {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, storage.ErrVersionConflict):
				conflicts++
			default:
				t.Errorf("unexpected error during concurrent update: %v", err)
			}
		}
		if successes != 1 || conflicts != workers-1 {
			t.Fatalf("concurrent update: expected 1 success and %d conflicts, got %d successes and %d conflicts",
				workers-1, successes, conflicts)
		}

		// Verify context_history has strictly unique versions [2, 1] (descending).
		history, _, err := b.GetContextHistory(ctx, concNS, "race-key", nil, "", 100)
		if err != nil {
			t.Fatalf("GetContextHistory: %v", err)
		}
		if len(history) != 2 {
			t.Fatalf("expected exactly 2 history entries, got %d", len(history))
		}
		if history[0].Version != 2 || history[1].Version != 1 {
			t.Errorf("expected strictly unique versions [2, 1], got [%d, %d]", history[0].Version, history[1].Version)
		}
	})
}
