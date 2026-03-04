package context

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
	"github.com/petal-labs/cortex/pkg/types"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestEngine(t *testing.T) (*Engine, *sqlite.Backend) {
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
	engine, err := NewEngine(backend, &cfg.Context)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	return engine, backend
}

func TestNewEngine(t *testing.T) {
	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	backend := sqlite.NewWithDB(db)

	// Test with valid backend
	engine, err := NewEngine(backend, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}

	// Test with nil backend
	_, err = NewEngine(nil, nil)
	if err == nil {
		t.Error("expected error for nil backend")
	}
}

func TestGetSet(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	// Set a value
	result, err := engine.Set(ctx, namespace, "my-key", "my-value", nil)
	if err != nil {
		t.Fatalf("failed to set: %v", err)
	}

	if result.Key != "my-key" {
		t.Errorf("expected key 'my-key', got %s", result.Key)
	}
	if result.Version != 1 {
		t.Errorf("expected version 1, got %d", result.Version)
	}

	// Get the value
	getResult, err := engine.Get(ctx, namespace, "my-key", nil)
	if err != nil {
		t.Fatalf("failed to get: %v", err)
	}

	if !getResult.Exists {
		t.Error("expected key to exist")
	}
	if getResult.Value != "my-value" {
		t.Errorf("expected value 'my-value', got %v", getResult.Value)
	}
	if getResult.Version != 1 {
		t.Errorf("expected version 1, got %d", getResult.Version)
	}
}

func TestGetNonExistent(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	result, err := engine.Get(context.Background(), "ns", "nonexistent", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Exists {
		t.Error("expected key to not exist")
	}
}

func TestSetEmptyKey(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.Set(context.Background(), "ns", "", "value", nil)
	if err != ErrEmptyKey {
		t.Errorf("expected ErrEmptyKey, got %v", err)
	}
}

func TestGetEmptyKey(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.Get(context.Background(), "ns", "", nil)
	if err != ErrEmptyKey {
		t.Errorf("expected ErrEmptyKey, got %v", err)
	}
}

func TestSetWithVersion(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	// Initial set
	engine.Set(ctx, namespace, "key", "v1", nil)

	// Update with expected version
	result, err := engine.Set(ctx, namespace, "key", "v2", &SetOpts{
		ExpectedVersion: ptr(int64(1)),
	})
	if err != nil {
		t.Fatalf("failed to set with expected version: %v", err)
	}
	if result.Version != 2 {
		t.Errorf("expected version 2, got %d", result.Version)
	}

	// Update with wrong version should fail
	_, err = engine.Set(ctx, namespace, "key", "v3", &SetOpts{
		ExpectedVersion: ptr(int64(1)), // Wrong version
	})
	if err != ErrVersionConflict {
		t.Errorf("expected ErrVersionConflict, got %v", err)
	}
}

func TestSetWithTTL(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	_, err := engine.Set(ctx, namespace, "ttl-key", "value", &SetOpts{
		TTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("failed to set with TTL: %v", err)
	}

	// Verify value exists
	result, _ := engine.Get(ctx, namespace, "ttl-key", nil)
	if !result.Exists {
		t.Error("expected key to exist")
	}
}

func TestSetWithRunID(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	runID := "run-123"

	// Set with run ID
	_, err := engine.Set(ctx, namespace, "run-key", "run-value", &SetOpts{
		RunID: &runID,
	})
	if err != nil {
		t.Fatalf("failed to set with run ID: %v", err)
	}

	// Get with run ID
	result, _ := engine.Get(ctx, namespace, "run-key", &GetOpts{
		RunID: &runID,
	})
	if !result.Exists {
		t.Error("expected key to exist with run ID")
	}

	// Get without run ID should not find it
	result2, _ := engine.Get(ctx, namespace, "run-key", nil)
	if result2.Exists {
		t.Error("expected key to not exist without run ID")
	}
}

func TestDelete(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	engine.Set(ctx, namespace, "to-delete", "value", nil)

	err := engine.Delete(ctx, namespace, "to-delete", nil)
	if err != nil {
		t.Fatalf("failed to delete: %v", err)
	}

	// Verify it's gone
	result, _ := engine.Get(ctx, namespace, "to-delete", nil)
	if result.Exists {
		t.Error("expected key to not exist after delete")
	}
}

func TestDeleteNonExistent(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	err := engine.Delete(context.Background(), "ns", "nonexistent", nil)
	if err != ErrKeyNotFound {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestList(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	// Create multiple keys
	engine.Set(ctx, namespace, "user:1", "a", nil)
	engine.Set(ctx, namespace, "user:2", "b", nil)
	engine.Set(ctx, namespace, "config:app", "c", nil)

	// List all
	result, err := engine.List(ctx, namespace, nil)
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if result.Count != 3 {
		t.Errorf("expected 3 keys, got %d", result.Count)
	}

	// List with prefix
	prefix := "user:"
	result2, err := engine.List(ctx, namespace, &ListOpts{
		Prefix: &prefix,
	})
	if err != nil {
		t.Fatalf("failed to list with prefix: %v", err)
	}
	if result2.Count != 2 {
		t.Errorf("expected 2 keys with prefix 'user:', got %d", result2.Count)
	}
}

func TestListWithRunID(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	runID := "run-456"

	// Create keys in different contexts
	engine.Set(ctx, namespace, "persistent", "a", nil)
	engine.Set(ctx, namespace, "run-scoped", "b", &SetOpts{RunID: &runID})

	// List persistent only
	result1, _ := engine.List(ctx, namespace, nil)
	if result1.Count != 1 {
		t.Errorf("expected 1 persistent key, got %d", result1.Count)
	}

	// List run-scoped
	result2, _ := engine.List(ctx, namespace, &ListOpts{RunID: &runID})
	if result2.Count != 1 {
		t.Errorf("expected 1 run-scoped key, got %d", result2.Count)
	}
}

func TestHistory(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	// Make multiple updates
	engine.Set(ctx, namespace, "versioned", "v1", nil)
	engine.Set(ctx, namespace, "versioned", "v2", nil)
	engine.Set(ctx, namespace, "versioned", "v3", nil)

	result, err := engine.History(ctx, namespace, "versioned", nil)
	if err != nil {
		t.Fatalf("failed to get history: %v", err)
	}

	if len(result.History) != 3 {
		t.Errorf("expected 3 history entries, got %d", len(result.History))
	}
}

func TestCleanupRun(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	runID := "run-to-clean"

	// Create run-scoped keys
	engine.Set(ctx, namespace, "key1", "a", &SetOpts{RunID: &runID})
	engine.Set(ctx, namespace, "key2", "b", &SetOpts{RunID: &runID})

	// Cleanup the run
	err := engine.CleanupRun(ctx, namespace, runID)
	if err != nil {
		t.Fatalf("failed to cleanup run: %v", err)
	}

	// Verify keys are gone
	result, _ := engine.List(ctx, namespace, &ListOpts{RunID: &runID})
	if result.Count != 0 {
		t.Errorf("expected 0 keys after cleanup, got %d", result.Count)
	}
}

func TestCleanupRunEmptyID(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	err := engine.CleanupRun(context.Background(), "ns", "")
	if err == nil {
		t.Error("expected error for empty run ID")
	}
}

// Merge strategy tests

func TestMergeReplace(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	engine.Set(ctx, namespace, "key", "old", nil)

	result, err := engine.Merge(ctx, namespace, "key", "new", &MergeOpts{
		Strategy: types.MergeStrategyReplace,
	})
	if err != nil {
		t.Fatalf("failed to merge: %v", err)
	}

	if result.MergedValue != "new" {
		t.Errorf("expected 'new', got %v", result.MergedValue)
	}
}

func TestMergeAppend(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	engine.Set(ctx, namespace, "key", []any{"a", "b"}, nil)

	result, err := engine.Merge(ctx, namespace, "key", []any{"c"}, &MergeOpts{
		Strategy: types.MergeStrategyAppend,
	})
	if err != nil {
		t.Fatalf("failed to merge: %v", err)
	}

	merged, ok := result.MergedValue.([]any)
	if !ok {
		t.Fatalf("expected slice, got %T", result.MergedValue)
	}
	if len(merged) != 3 {
		t.Errorf("expected 3 elements, got %d", len(merged))
	}
}

func TestMergeMax(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	engine.Set(ctx, namespace, "key", 10, nil)

	result, err := engine.Merge(ctx, namespace, "key", 15, &MergeOpts{
		Strategy: types.MergeStrategyMax,
	})
	if err != nil {
		t.Fatalf("failed to merge: %v", err)
	}

	if result.MergedValue != 15 {
		t.Errorf("expected 15, got %v", result.MergedValue)
	}

	// Merge with smaller value
	result2, _ := engine.Merge(ctx, namespace, "key", 5, &MergeOpts{
		Strategy: types.MergeStrategyMax,
	})

	// Get the actual stored value
	getResult, _ := engine.Get(ctx, namespace, "key", nil)
	if getResult.Value.(float64) != 15 {
		t.Errorf("expected 15 to be preserved, got %v", result2.MergedValue)
	}
}

func TestMergeMin(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	engine.Set(ctx, namespace, "key", 10, nil)

	result, err := engine.Merge(ctx, namespace, "key", 5, &MergeOpts{
		Strategy: types.MergeStrategyMin,
	})
	if err != nil {
		t.Fatalf("failed to merge: %v", err)
	}

	if result.MergedValue != 5 {
		t.Errorf("expected 5, got %v", result.MergedValue)
	}
}

func TestMergeSum(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	engine.Set(ctx, namespace, "counter", 10, nil)

	result, err := engine.Merge(ctx, namespace, "counter", 5, &MergeOpts{
		Strategy: types.MergeStrategySum,
	})
	if err != nil {
		t.Fatalf("failed to merge: %v", err)
	}

	if result.MergedValue != float64(15) {
		t.Errorf("expected 15, got %v", result.MergedValue)
	}
}

func TestMergeDeepMerge(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	existing := map[string]any{
		"name": "test",
		"config": map[string]any{
			"a": 1,
			"b": 2,
		},
	}
	engine.Set(ctx, namespace, "key", existing, nil)

	incoming := map[string]any{
		"config": map[string]any{
			"b": 3, // Override
			"c": 4, // New key
		},
		"new_field": "added",
	}

	result, err := engine.Merge(ctx, namespace, "key", incoming, &MergeOpts{
		Strategy: types.MergeStrategyDeepMerge,
	})
	if err != nil {
		t.Fatalf("failed to merge: %v", err)
	}

	merged, ok := result.MergedValue.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result.MergedValue)
	}

	// Check that name is preserved
	if merged["name"] != "test" {
		t.Error("name field should be preserved")
	}

	// Check that new_field was added
	if merged["new_field"] != "added" {
		t.Error("new_field should be added")
	}

	// Check nested merge
	config, ok := merged["config"].(map[string]any)
	if !ok {
		t.Fatal("config should be a map")
	}

	// Use helper to compare numeric values regardless of type
	if !numEquals(config["a"], 1) {
		t.Errorf("config.a should be 1, got %v", config["a"])
	}
	if !numEquals(config["b"], 3) {
		t.Errorf("config.b should be 3 (overridden), got %v", config["b"])
	}
	if !numEquals(config["c"], 4) {
		t.Errorf("config.c should be 4 (new), got %v", config["c"])
	}
}

// numEquals compares two numeric values regardless of their concrete types.
func numEquals(a, b any) bool {
	af, aok := toFloat64(a)
	bf, bok := toFloat64(b)
	if !aok || !bok {
		return false
	}
	return af == bf
}

func TestMergeNonExistentKey(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	// Merge to non-existent key should just set the value
	result, err := engine.Merge(ctx, namespace, "new-key", "value", nil)
	if err != nil {
		t.Fatalf("failed to merge: %v", err)
	}

	if result.MergedValue != "value" {
		t.Errorf("expected 'value', got %v", result.MergedValue)
	}
}

func TestMergeInvalidTypes(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	engine.Set(ctx, namespace, "key", "not a number", nil)

	_, err := engine.Merge(ctx, namespace, "key", 5, &MergeOpts{
		Strategy: types.MergeStrategySum,
	})
	if err != ErrInvalidMerge {
		t.Errorf("expected ErrInvalidMerge, got %v", err)
	}
}

func TestMergeArrayConcatUnique(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	engine.Set(ctx, namespace, "tags", []any{"a", "b"}, nil)

	result, err := engine.Merge(ctx, namespace, "tags", []any{"b", "c"}, &MergeOpts{
		Strategy:      types.MergeStrategyDeepMerge,
		ArrayStrategy: types.ArrayStrategyConcatUnique,
	})
	if err != nil {
		t.Fatalf("failed to merge: %v", err)
	}

	merged, ok := result.MergedValue.([]any)
	if !ok {
		t.Fatalf("expected slice, got %T", result.MergedValue)
	}

	// Should have a, b, c (b not duplicated)
	if len(merged) != 3 {
		t.Errorf("expected 3 unique elements, got %d: %v", len(merged), merged)
	}
}

func TestMergeWithExpectedVersion(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	engine.Set(ctx, namespace, "key", 1, nil)

	// Merge with correct expected version
	_, err := engine.Merge(ctx, namespace, "key", 2, &MergeOpts{
		Strategy:        types.MergeStrategySum,
		ExpectedVersion: ptr(int64(1)),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Merge with wrong expected version
	_, err = engine.Merge(ctx, namespace, "key", 3, &MergeOpts{
		Strategy:        types.MergeStrategySum,
		ExpectedVersion: ptr(int64(1)), // Should be 2 now
	})
	if err != ErrVersionConflict {
		t.Errorf("expected ErrVersionConflict, got %v", err)
	}
}

// Helper functions

func ptr[T any](v T) *T {
	return &v
}

func TestToSlice(t *testing.T) {
	// Test nil
	if result := toSlice(nil); result != nil {
		t.Error("nil should return nil")
	}

	// Test slice
	result := toSlice([]int{1, 2, 3})
	if len(result) != 3 {
		t.Errorf("expected 3 elements, got %d", len(result))
	}

	// Test single value
	result = toSlice("single")
	if len(result) != 1 {
		t.Errorf("expected 1 element, got %d", len(result))
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		input    any
		expected float64
		ok       bool
	}{
		{int(5), 5, true},
		{int64(10), 10, true},
		{float64(3.14), 3.14, true},
		{float32(2.5), 2.5, true},
		{"not a number", 0, false},
		{nil, 0, false},
	}

	for _, tt := range tests {
		result, ok := toFloat64(tt.input)
		if ok != tt.ok {
			t.Errorf("toFloat64(%v) ok = %v, expected %v", tt.input, ok, tt.ok)
		}
		if ok && result != tt.expected {
			t.Errorf("toFloat64(%v) = %v, expected %v", tt.input, result, tt.expected)
		}
	}
}

func TestConcatUnique(t *testing.T) {
	a := []any{1, 2, 3}
	b := []any{2, 3, 4}

	result := concatUnique(a, b)
	if len(result) != 4 {
		t.Errorf("expected 4 unique elements, got %d", len(result))
	}
}
