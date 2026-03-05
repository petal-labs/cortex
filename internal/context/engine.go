package context

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// Common errors returned by the context engine.
var (
	ErrKeyNotFound     = errors.New("context key not found")
	ErrVersionConflict = errors.New("version conflict")
	ErrInvalidMerge    = errors.New("cannot merge incompatible types")
	ErrEmptyKey        = errors.New("context key cannot be empty")
)

// Engine implements the workflow context logic layer.
// It orchestrates storage operations and provides merge functionality.
type Engine struct {
	storage storage.Backend
	cfg     *config.ContextConfig
}

// NewEngine creates a new context engine.
func NewEngine(store storage.Backend, cfg *config.ContextConfig) (*Engine, error) {
	if store == nil {
		return nil, errors.New("storage backend is required")
	}

	if cfg == nil {
		defaultCfg := config.DefaultConfig()
		cfg = &defaultCfg.Context
	}

	return &Engine{
		storage: store,
		cfg:     cfg,
	}, nil
}

// GetOpts contains options for retrieving context.
type GetOpts struct {
	RunID *string // Optional: nil for persistent context
}

// GetResult contains the result of a context get operation.
type GetResult struct {
	Key       string    `json:"key"`
	Value     any       `json:"value"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	Exists    bool      `json:"exists"`
}

// Get retrieves a context entry by key.
func (e *Engine) Get(ctx context.Context, namespace, key string, opts *GetOpts) (*GetResult, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}

	if opts == nil {
		opts = &GetOpts{}
	}

	entry, err := e.storage.GetContext(ctx, namespace, key, opts.RunID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return &GetResult{
				Key:    key,
				Exists: false,
			}, nil
		}
		return nil, fmt.Errorf("failed to get context: %w", err)
	}

	return &GetResult{
		Key:       key,
		Value:     entry.Value,
		Version:   entry.Version,
		UpdatedAt: entry.UpdatedAt,
		Exists:    true,
	}, nil
}

// SetOpts contains options for setting context.
type SetOpts struct {
	RunID           *string       // Optional: nil for persistent context
	ExpectedVersion *int64        // For optimistic concurrency
	TTL             time.Duration // Auto-expire duration (0 = no expiration)
	UpdatedBy       string        // Agent/task identifier
}

// SetResult contains the result of a context set operation.
type SetResult struct {
	Key             string `json:"key"`
	Version         int64  `json:"version"`
	PreviousVersion int64  `json:"previous_version"`
}

// Set stores a context entry.
func (e *Engine) Set(ctx context.Context, namespace, key string, value any, opts *SetOpts) (*SetResult, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}

	if opts == nil {
		opts = &SetOpts{}
	}

	// Get current version for the result
	var previousVersion int64
	existing, err := e.storage.GetContext(ctx, namespace, key, opts.RunID)
	if err == nil {
		previousVersion = existing.Version
	}

	// Calculate TTL expiration
	var ttlExpires *time.Time
	if opts.TTL > 0 {
		exp := time.Now().Add(opts.TTL)
		ttlExpires = &exp
	}

	entry := &types.ContextEntry{
		Namespace:    namespace,
		RunID:        opts.RunID,
		Key:          key,
		Value:        value,
		UpdatedAt:    time.Now().UTC(),
		UpdatedBy:    opts.UpdatedBy,
		TTLExpiresAt: ttlExpires,
	}

	if err := e.storage.SetContext(ctx, entry, opts.ExpectedVersion); err != nil {
		if errors.Is(err, storage.ErrVersionConflict) {
			return nil, ErrVersionConflict
		}
		return nil, fmt.Errorf("failed to set context: %w", err)
	}

	// Get the new version
	updated, err := e.storage.GetContext(ctx, namespace, key, opts.RunID)
	if err != nil {
		return nil, fmt.Errorf("failed to get updated context: %w", err)
	}

	return &SetResult{
		Key:             key,
		Version:         updated.Version,
		PreviousVersion: previousVersion,
	}, nil
}

// MergeOpts contains options for merging context.
type MergeOpts struct {
	RunID           *string             // Optional: nil for persistent context
	ExpectedVersion *int64              // For optimistic concurrency
	Strategy        types.MergeStrategy // Merge strategy (default: deep_merge)
	ArrayStrategy   types.ArrayStrategy // How to handle arrays in deep_merge
	UpdatedBy       string              // Agent/task identifier
}

// MergeResult contains the result of a context merge operation.
type MergeResult struct {
	Key         string `json:"key"`
	Version     int64  `json:"version"`
	MergedValue any    `json:"merged_value"`
}

// Merge combines a new value with an existing context entry.
func (e *Engine) Merge(ctx context.Context, namespace, key string, value any, opts *MergeOpts) (*MergeResult, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}

	if opts == nil {
		opts = &MergeOpts{}
	}

	if opts.Strategy == "" {
		opts.Strategy = types.MergeStrategyDeepMerge
	}
	if opts.ArrayStrategy == "" {
		opts.ArrayStrategy = types.ArrayStrategyConcat
	}

	// Get existing value
	existing, err := e.storage.GetContext(ctx, namespace, key, opts.RunID)

	var mergedValue any
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			// No existing value, just set the new value
			mergedValue = value
		} else {
			return nil, fmt.Errorf("failed to get existing context: %w", err)
		}
	} else {
		// Check version if expected
		if opts.ExpectedVersion != nil && existing.Version != *opts.ExpectedVersion {
			return nil, ErrVersionConflict
		}

		// Merge values based on strategy
		mergedValue, err = e.mergeValues(existing.Value, value, opts.Strategy, opts.ArrayStrategy)
		if err != nil {
			return nil, err
		}
	}

	// Set the merged value
	setOpts := &SetOpts{
		RunID:     opts.RunID,
		UpdatedBy: opts.UpdatedBy,
	}

	result, err := e.Set(ctx, namespace, key, mergedValue, setOpts)
	if err != nil {
		return nil, err
	}

	return &MergeResult{
		Key:         key,
		Version:     result.Version,
		MergedValue: mergedValue,
	}, nil
}

// Delete removes a context entry.
func (e *Engine) Delete(ctx context.Context, namespace, key string, runID *string) error {
	if key == "" {
		return ErrEmptyKey
	}

	err := e.storage.DeleteContext(ctx, namespace, key, runID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrKeyNotFound
		}
		return fmt.Errorf("failed to delete context: %w", err)
	}
	return nil
}

// ListOpts contains options for listing context keys.
type ListOpts struct {
	RunID  *string // Optional: nil for persistent context
	Prefix *string // Optional key prefix filter
	Cursor string  // Pagination cursor
	Limit  int     // Max keys to return (0 = default)
}

// ListResult contains the result of a context list operation.
type ListResult struct {
	Keys       []string `json:"keys"`
	Count      int      `json:"count"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// List returns all keys matching the criteria.
func (e *Engine) List(ctx context.Context, namespace string, opts *ListOpts) (*ListResult, error) {
	if opts == nil {
		opts = &ListOpts{}
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	keys, nextCursor, err := e.storage.ListContextKeys(ctx, namespace, opts.Prefix, opts.RunID, opts.Cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list context keys: %w", err)
	}

	return &ListResult{
		Keys:       keys,
		Count:      len(keys),
		NextCursor: nextCursor,
	}, nil
}

// HistoryOpts contains options for retrieving context history.
type HistoryOpts struct {
	RunID  *string // Optional: nil for persistent context
	Cursor string  // Pagination cursor
	Limit  int     // Max entries to return (0 = default)
}

// HistoryResult contains the result of a context history operation.
type HistoryResult struct {
	Key        string                       `json:"key"`
	History    []*types.ContextHistoryEntry `json:"history"`
	NextCursor string                       `json:"next_cursor,omitempty"`
}

// History retrieves the version history for a key.
func (e *Engine) History(ctx context.Context, namespace, key string, opts *HistoryOpts) (*HistoryResult, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}

	if opts == nil {
		opts = &HistoryOpts{}
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}

	history, nextCursor, err := e.storage.GetContextHistory(ctx, namespace, key, opts.RunID, opts.Cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get context history: %w", err)
	}

	return &HistoryResult{
		Key:        key,
		History:    history,
		NextCursor: nextCursor,
	}, nil
}

// CleanupExpired removes all context entries past their TTL expiration.
func (e *Engine) CleanupExpired(ctx context.Context) (int64, error) {
	count, err := e.storage.CleanupExpiredContext(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired context: %w", err)
	}
	return count, nil
}

// CleanupRun removes all context entries for a specific run.
func (e *Engine) CleanupRun(ctx context.Context, namespace, runID string) error {
	if runID == "" {
		return errors.New("run ID is required")
	}

	if err := e.storage.CleanupRunContext(ctx, namespace, runID); err != nil {
		return fmt.Errorf("failed to cleanup run context: %w", err)
	}
	return nil
}

// mergeValues combines two values based on the merge strategy.
func (e *Engine) mergeValues(existing, incoming any, strategy types.MergeStrategy, arrayStrategy types.ArrayStrategy) (any, error) {
	switch strategy {
	case types.MergeStrategyReplace:
		return incoming, nil

	case types.MergeStrategyAppend:
		return e.appendValues(existing, incoming)

	case types.MergeStrategyMax:
		return e.maxValues(existing, incoming)

	case types.MergeStrategyMin:
		return e.minValues(existing, incoming)

	case types.MergeStrategySum:
		return e.sumValues(existing, incoming)

	case types.MergeStrategyDeepMerge:
		return e.deepMerge(existing, incoming, arrayStrategy)

	default:
		return e.deepMerge(existing, incoming, arrayStrategy)
	}
}

// appendValues concatenates arrays or converts to array and appends.
func (e *Engine) appendValues(existing, incoming any) (any, error) {
	// Convert both to slices
	existingSlice := toSlice(existing)
	incomingSlice := toSlice(incoming)

	return append(existingSlice, incomingSlice...), nil
}

// maxValues returns the larger of two numeric values.
func (e *Engine) maxValues(existing, incoming any) (any, error) {
	existingNum, ok1 := toFloat64(existing)
	incomingNum, ok2 := toFloat64(incoming)

	if !ok1 || !ok2 {
		return nil, ErrInvalidMerge
	}

	if incomingNum > existingNum {
		return incoming, nil
	}
	return existing, nil
}

// minValues returns the smaller of two numeric values.
func (e *Engine) minValues(existing, incoming any) (any, error) {
	existingNum, ok1 := toFloat64(existing)
	incomingNum, ok2 := toFloat64(incoming)

	if !ok1 || !ok2 {
		return nil, ErrInvalidMerge
	}

	if incomingNum < existingNum {
		return incoming, nil
	}
	return existing, nil
}

// sumValues adds two numeric values.
func (e *Engine) sumValues(existing, incoming any) (any, error) {
	existingNum, ok1 := toFloat64(existing)
	incomingNum, ok2 := toFloat64(incoming)

	if !ok1 || !ok2 {
		return nil, ErrInvalidMerge
	}

	return existingNum + incomingNum, nil
}

// deepMerge recursively merges two maps or replaces incompatible types.
func (e *Engine) deepMerge(existing, incoming any, arrayStrategy types.ArrayStrategy) (any, error) {
	// Convert to maps if possible
	existingMap := toMap(existing)
	incomingMap := toMap(incoming)

	// If both are maps, deep merge
	if existingMap != nil && incomingMap != nil {
		result := make(map[string]any)

		// Copy existing values
		maps.Copy(result, existingMap)

		// Merge incoming values
		for k, v := range incomingMap {
			if existingVal, exists := result[k]; exists {
				// Recursively merge nested values
				merged, err := e.deepMerge(existingVal, v, arrayStrategy)
				if err != nil {
					return nil, err
				}
				result[k] = merged
			} else {
				result[k] = v
			}
		}

		return result, nil
	}

	// If at least one is actually a slice, merge based on array strategy
	existingIsSlice := isSlice(existing)
	incomingIsSlice := isSlice(incoming)

	if existingIsSlice || incomingIsSlice {
		existingSlice := toSlice(existing)
		incomingSlice := toSlice(incoming)

		switch arrayStrategy {
		case types.ArrayStrategyReplace:
			return incoming, nil
		case types.ArrayStrategyConcatUnique:
			return concatUnique(existingSlice, incomingSlice), nil
		default: // concat
			return append(existingSlice, incomingSlice...), nil
		}
	}

	// For non-mergeable scalar types, replace with incoming
	return incoming, nil
}

// Helper functions

// isSlice checks if a value is a slice type.
func isSlice(v any) bool {
	if v == nil {
		return false
	}
	return reflect.ValueOf(v).Kind() == reflect.Slice
}

// toSlice converts a value to a slice.
func toSlice(v any) []any {
	if v == nil {
		return nil
	}

	// Check if already a slice
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice {
		result := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			result[i] = rv.Index(i).Interface()
		}
		return result
	}

	// Single value becomes single-element slice
	return []any{v}
}

// toMap converts a value to a map[string]any.
func toMap(v any) map[string]any {
	if v == nil {
		return nil
	}

	// Check if already a map
	if m, ok := v.(map[string]any); ok {
		return m
	}

	// Try to convert via JSON for other map types
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}

	return result
}

// toFloat64 converts a numeric value to float64.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// concatUnique concatenates two slices, removing duplicates.
func concatUnique(a, b []any) []any {
	seen := make(map[string]bool)
	result := make([]any, 0, len(a)+len(b))

	// Helper to get a comparable key
	toKey := func(v any) string {
		data, _ := json.Marshal(v)
		return string(data)
	}

	for _, v := range a {
		key := toKey(v)
		if !seen[key] {
			seen[key] = true
			result = append(result, v)
		}
	}

	for _, v := range b {
		key := toKey(v)
		if !seen[key] {
			seen[key] = true
			result = append(result, v)
		}
	}

	return result
}
