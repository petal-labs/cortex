package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

func TestSetAndGetContext(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create context entry
	entry := &types.ContextEntry{
		Namespace: "test-ns",
		Key:       "config.theme",
		Value:     map[string]any{"color": "dark", "size": 14},
		UpdatedBy: "test-agent",
	}

	// Set context
	if err := backend.SetContext(ctx, entry, nil); err != nil {
		t.Fatalf("failed to set context: %v", err)
	}

	// Verify version was set
	if entry.Version != 1 {
		t.Errorf("expected version 1, got %d", entry.Version)
	}

	// Get context
	retrieved, err := backend.GetContext(ctx, "test-ns", "config.theme", nil)
	if err != nil {
		t.Fatalf("failed to get context: %v", err)
	}

	if retrieved.Key != "config.theme" {
		t.Errorf("expected key 'config.theme', got '%s'", retrieved.Key)
	}
	if retrieved.Version != 1 {
		t.Errorf("expected version 1, got %d", retrieved.Version)
	}
	if retrieved.UpdatedBy != "test-agent" {
		t.Errorf("expected updated_by 'test-agent', got '%s'", retrieved.UpdatedBy)
	}

	// Check value (it's a map[string]any after JSON roundtrip)
	valueMap, ok := retrieved.Value.(map[string]any)
	if !ok {
		t.Fatalf("expected value to be map[string]any, got %T", retrieved.Value)
	}
	if valueMap["color"] != "dark" {
		t.Errorf("expected color 'dark', got '%v'", valueMap["color"])
	}
}

func TestSetContextVersioning(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	entry := &types.ContextEntry{
		Namespace: "test-ns",
		Key:       "counter",
		Value:     1,
	}

	// Set initial
	if err := backend.SetContext(ctx, entry, nil); err != nil {
		t.Fatalf("failed to set context: %v", err)
	}
	if entry.Version != 1 {
		t.Errorf("expected version 1, got %d", entry.Version)
	}

	// Update
	entry.Value = 2
	if err := backend.SetContext(ctx, entry, nil); err != nil {
		t.Fatalf("failed to update context: %v", err)
	}
	if entry.Version != 2 {
		t.Errorf("expected version 2, got %d", entry.Version)
	}

	// Update again
	entry.Value = 3
	if err := backend.SetContext(ctx, entry, nil); err != nil {
		t.Fatalf("failed to update context again: %v", err)
	}
	if entry.Version != 3 {
		t.Errorf("expected version 3, got %d", entry.Version)
	}

	// Verify final value
	retrieved, err := backend.GetContext(ctx, "test-ns", "counter", nil)
	if err != nil {
		t.Fatalf("failed to get context: %v", err)
	}
	if retrieved.Version != 3 {
		t.Errorf("expected version 3, got %d", retrieved.Version)
	}
}

func TestSetContextOptimisticConcurrency(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	entry := &types.ContextEntry{
		Namespace: "test-ns",
		Key:       "resource",
		Value:     "initial",
	}

	// Set initial
	if err := backend.SetContext(ctx, entry, nil); err != nil {
		t.Fatalf("failed to set context: %v", err)
	}

	// Try to update with correct expected version
	entry.Value = "updated"
	expectedV := int64(1)
	if err := backend.SetContext(ctx, entry, &expectedV); err != nil {
		t.Fatalf("failed to update with correct version: %v", err)
	}

	// Try to update with wrong expected version
	entry.Value = "updated again"
	wrongV := int64(1) // Should be 2 now
	err := backend.SetContext(ctx, entry, &wrongV)
	if err != storage.ErrVersionConflict {
		t.Errorf("expected ErrVersionConflict, got %v", err)
	}
}

func TestSetContextWithRunID(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	runID := "run-123"

	entry := &types.ContextEntry{
		Namespace: "test-ns",
		RunID:     &runID,
		Key:       "step.status",
		Value:     "running",
	}

	if err := backend.SetContext(ctx, entry, nil); err != nil {
		t.Fatalf("failed to set context: %v", err)
	}

	// Get with runID
	retrieved, err := backend.GetContext(ctx, "test-ns", "step.status", &runID)
	if err != nil {
		t.Fatalf("failed to get context: %v", err)
	}
	if retrieved.RunID == nil {
		t.Fatal("expected runID to be set")
	}
	if *retrieved.RunID != runID {
		t.Errorf("expected runID '%s', got '%s'", runID, *retrieved.RunID)
	}

	// Should not find without runID (persistent context)
	_, err = backend.GetContext(ctx, "test-ns", "step.status", nil)
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSetContextWithTTL(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	ttl := time.Now().Add(time.Hour).UTC()
	entry := &types.ContextEntry{
		Namespace:    "test-ns",
		Key:          "temp.data",
		Value:        "temporary",
		TTLExpiresAt: &ttl,
	}

	if err := backend.SetContext(ctx, entry, nil); err != nil {
		t.Fatalf("failed to set context: %v", err)
	}

	retrieved, err := backend.GetContext(ctx, "test-ns", "temp.data", nil)
	if err != nil {
		t.Fatalf("failed to get context: %v", err)
	}
	if retrieved.TTLExpiresAt == nil {
		t.Fatal("expected TTLExpiresAt to be set")
	}
	if retrieved.TTLExpiresAt.Unix() != ttl.Unix() {
		t.Errorf("expected TTL %v, got %v", ttl, *retrieved.TTLExpiresAt)
	}
}

func TestGetContextNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	_, err := backend.GetContext(ctx, "test-ns", "nonexistent", nil)
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListContextKeys(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create multiple keys
	keys := []string{"config.theme", "config.lang", "user.name", "user.email", "system.status"}
	for _, key := range keys {
		entry := &types.ContextEntry{
			Namespace: "test-ns",
			Key:       key,
			Value:     "value",
		}
		if err := backend.SetContext(ctx, entry, nil); err != nil {
			t.Fatalf("failed to set context: %v", err)
		}
	}

	// List all keys
	result, _, err := backend.ListContextKeys(ctx, "test-ns", nil, nil, "", 10)
	if err != nil {
		t.Fatalf("failed to list keys: %v", err)
	}
	if len(result) != 5 {
		t.Errorf("expected 5 keys, got %d", len(result))
	}

	// List with prefix
	prefix := "config"
	result, _, err = backend.ListContextKeys(ctx, "test-ns", &prefix, nil, "", 10)
	if err != nil {
		t.Fatalf("failed to list keys with prefix: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 keys with 'config' prefix, got %d", len(result))
	}

	// Test pagination
	result, cursor, err := backend.ListContextKeys(ctx, "test-ns", nil, nil, "", 3)
	if err != nil {
		t.Fatalf("failed to list keys with limit: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("expected 3 keys with limit, got %d", len(result))
	}
	if cursor == "" {
		t.Error("expected non-empty cursor for pagination")
	}
}

func TestDeleteContext(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create entry
	entry := &types.ContextEntry{
		Namespace: "test-ns",
		Key:       "to.delete",
		Value:     "temporary",
	}
	if err := backend.SetContext(ctx, entry, nil); err != nil {
		t.Fatalf("failed to set context: %v", err)
	}

	// Delete entry
	if err := backend.DeleteContext(ctx, "test-ns", "to.delete", nil); err != nil {
		t.Fatalf("failed to delete context: %v", err)
	}

	// Verify it's gone
	_, err := backend.GetContext(ctx, "test-ns", "to.delete", nil)
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteContextNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	err := backend.DeleteContext(ctx, "test-ns", "nonexistent", nil)
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetContextHistory(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create entry with multiple updates
	entry := &types.ContextEntry{
		Namespace: "test-ns",
		Key:       "history.test",
		UpdatedBy: "agent-1",
	}

	for i := 1; i <= 5; i++ {
		entry.Value = i
		if err := backend.SetContext(ctx, entry, nil); err != nil {
			t.Fatalf("failed to set context: %v", err)
		}
	}

	// Get history
	history, _, err := backend.GetContextHistory(ctx, "test-ns", "history.test", nil, "", 10)
	if err != nil {
		t.Fatalf("failed to get history: %v", err)
	}
	if len(history) != 5 {
		t.Errorf("expected 5 history entries, got %d", len(history))
	}

	// History should be in descending order (newest first)
	if history[0].Version != 5 {
		t.Errorf("expected first history entry to be version 5, got %d", history[0].Version)
	}
	if history[4].Version != 1 {
		t.Errorf("expected last history entry to be version 1, got %d", history[4].Version)
	}

	// All entries should have operation "set"
	for _, h := range history {
		if h.Operation != "set" {
			t.Errorf("expected operation 'set', got '%s'", h.Operation)
		}
	}
}

func TestGetContextHistoryAfterDelete(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create and update entry
	entry := &types.ContextEntry{
		Namespace: "test-ns",
		Key:       "delete.history",
		Value:     "initial",
	}
	if err := backend.SetContext(ctx, entry, nil); err != nil {
		t.Fatalf("failed to set context: %v", err)
	}

	entry.Value = "updated"
	if err := backend.SetContext(ctx, entry, nil); err != nil {
		t.Fatalf("failed to update context: %v", err)
	}

	// Delete entry
	if err := backend.DeleteContext(ctx, "test-ns", "delete.history", nil); err != nil {
		t.Fatalf("failed to delete context: %v", err)
	}

	// History should include delete operation
	history, _, err := backend.GetContextHistory(ctx, "test-ns", "delete.history", nil, "", 10)
	if err != nil {
		t.Fatalf("failed to get history: %v", err)
	}
	if len(history) != 3 {
		t.Errorf("expected 3 history entries (2 sets + 1 delete), got %d", len(history))
	}

	// Most recent should be delete
	if history[0].Operation != "delete" {
		t.Errorf("expected first history entry to be 'delete', got '%s'", history[0].Operation)
	}
}

func TestCleanupExpiredContext(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create entries with past TTL
	pastTTL := time.Now().Add(-time.Hour).UTC()
	for i := 0; i < 3; i++ {
		entry := &types.ContextEntry{
			Namespace:    "test-ns",
			Key:          "expired." + string(rune('A'+i)),
			Value:        "expired",
			TTLExpiresAt: &pastTTL,
		}
		if err := backend.SetContext(ctx, entry, nil); err != nil {
			t.Fatalf("failed to set context: %v", err)
		}
	}

	// Create entries with future TTL
	futureTTL := time.Now().Add(time.Hour).UTC()
	for i := 0; i < 2; i++ {
		entry := &types.ContextEntry{
			Namespace:    "test-ns",
			Key:          "valid." + string(rune('A'+i)),
			Value:        "valid",
			TTLExpiresAt: &futureTTL,
		}
		if err := backend.SetContext(ctx, entry, nil); err != nil {
			t.Fatalf("failed to set context: %v", err)
		}
	}

	// Create entries without TTL
	for i := 0; i < 2; i++ {
		entry := &types.ContextEntry{
			Namespace: "test-ns",
			Key:       "permanent." + string(rune('A'+i)),
			Value:     "permanent",
		}
		if err := backend.SetContext(ctx, entry, nil); err != nil {
			t.Fatalf("failed to set context: %v", err)
		}
	}

	// Cleanup expired
	deleted, err := backend.CleanupExpiredContext(ctx)
	if err != nil {
		t.Fatalf("failed to cleanup: %v", err)
	}
	if deleted != 3 {
		t.Errorf("expected 3 deleted, got %d", deleted)
	}

	// Verify remaining keys
	keys, _, err := backend.ListContextKeys(ctx, "test-ns", nil, nil, "", 10)
	if err != nil {
		t.Fatalf("failed to list keys: %v", err)
	}
	if len(keys) != 4 {
		t.Errorf("expected 4 remaining keys, got %d", len(keys))
	}
}

func TestCleanupRunContext(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	runID := "run-to-cleanup"

	// Create run-scoped entries
	for i := 0; i < 5; i++ {
		entry := &types.ContextEntry{
			Namespace: "test-ns",
			RunID:     &runID,
			Key:       "run.key." + string(rune('A'+i)),
			Value:     "run data",
		}
		if err := backend.SetContext(ctx, entry, nil); err != nil {
			t.Fatalf("failed to set context: %v", err)
		}
	}

	// Create persistent entry
	entry := &types.ContextEntry{
		Namespace: "test-ns",
		Key:       "persistent.key",
		Value:     "persistent",
	}
	if err := backend.SetContext(ctx, entry, nil); err != nil {
		t.Fatalf("failed to set context: %v", err)
	}

	// Cleanup run context
	if err := backend.CleanupRunContext(ctx, "test-ns", runID); err != nil {
		t.Fatalf("failed to cleanup run context: %v", err)
	}

	// Verify run-scoped entries are gone
	keys, _, err := backend.ListContextKeys(ctx, "test-ns", nil, &runID, "", 10)
	if err != nil {
		t.Fatalf("failed to list keys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected 0 run-scoped keys, got %d", len(keys))
	}

	// Verify persistent entry still exists
	_, err = backend.GetContext(ctx, "test-ns", "persistent.key", nil)
	if err != nil {
		t.Errorf("persistent entry should still exist, got error: %v", err)
	}
}

func TestContextValueTypes(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	testCases := []struct {
		name  string
		key   string
		value any
	}{
		{"string", "type.string", "hello"},
		{"int", "type.int", float64(42)}, // JSON numbers become float64
		{"float", "type.float", 3.14},
		{"bool", "type.bool", true},
		{"array", "type.array", []any{"a", "b", "c"}},
		{"object", "type.object", map[string]any{"nested": "value"}},
		{"null", "type.null", nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			entry := &types.ContextEntry{
				Namespace: "test-ns",
				Key:       tc.key,
				Value:     tc.value,
			}
			if err := backend.SetContext(ctx, entry, nil); err != nil {
				t.Fatalf("failed to set context: %v", err)
			}

			retrieved, err := backend.GetContext(ctx, "test-ns", tc.key, nil)
			if err != nil {
				t.Fatalf("failed to get context: %v", err)
			}

			// For nil, we need special handling
			if tc.value == nil {
				if retrieved.Value != nil {
					t.Errorf("expected nil, got %v", retrieved.Value)
				}
				return
			}

			// For other types, compare after JSON roundtrip normalization
			switch expected := tc.value.(type) {
			case []any:
				actual, ok := retrieved.Value.([]any)
				if !ok {
					t.Errorf("expected []any, got %T", retrieved.Value)
					return
				}
				if len(actual) != len(expected) {
					t.Errorf("expected len %d, got %d", len(expected), len(actual))
				}
			case map[string]any:
				actual, ok := retrieved.Value.(map[string]any)
				if !ok {
					t.Errorf("expected map[string]any, got %T", retrieved.Value)
					return
				}
				if len(actual) != len(expected) {
					t.Errorf("expected len %d, got %d", len(expected), len(actual))
				}
			default:
				if retrieved.Value != tc.value {
					t.Errorf("expected %v, got %v", tc.value, retrieved.Value)
				}
			}
		})
	}
}
