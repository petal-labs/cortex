package types

import "time"

// ContextEntry represents a key-value entry in the workflow context.
type ContextEntry struct {
	Namespace    string    `json:"namespace"`
	RunID        *string   `json:"run_id,omitempty"` // Nil for cross-run state
	Key          string    `json:"key"`
	Value        any       `json:"value"`
	Version      int64     `json:"version"`                  // Optimistic concurrency
	UpdatedAt    time.Time `json:"updated_at"`
	UpdatedBy    string    `json:"updated_by,omitempty"`     // Agent/task that last wrote
	TTLExpiresAt *time.Time `json:"ttl_expires_at,omitempty"` // Auto-expire time
}

// MergeStrategy defines how values are merged in context.merge.
type MergeStrategy string

const (
	MergeStrategyDeepMerge MergeStrategy = "deep_merge"
	MergeStrategyAppend    MergeStrategy = "append"
	MergeStrategyReplace   MergeStrategy = "replace"
	MergeStrategyMax       MergeStrategy = "max"
	MergeStrategyMin       MergeStrategy = "min"
	MergeStrategySum       MergeStrategy = "sum"
)

// ArrayStrategy defines how arrays are handled in deep_merge.
type ArrayStrategy string

const (
	ArrayStrategyConcat       ArrayStrategy = "concat"
	ArrayStrategyConcatUnique ArrayStrategy = "concat_unique"
	ArrayStrategyReplace      ArrayStrategy = "replace"
)

// ContextGetResponse represents the response from context.get.
type ContextGetResponse struct {
	Key       string    `json:"key"`
	Value     any       `json:"value"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	Exists    bool      `json:"exists"`
}

// ContextSetResponse represents the response from context.set.
type ContextSetResponse struct {
	Key             string `json:"key"`
	Version         int64  `json:"version"`
	PreviousVersion int64  `json:"previous_version"`
}

// ContextMergeResponse represents the response from context.merge.
type ContextMergeResponse struct {
	Key         string `json:"key"`
	Version     int64  `json:"version"`
	MergedValue any    `json:"merged_value"`
}

// ContextListResponse represents the response from context.list.
type ContextListResponse struct {
	Keys       []string `json:"keys"`
	Count      int      `json:"count"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// ContextHistoryEntry represents a single entry in context history.
type ContextHistoryEntry struct {
	Version   int64     `json:"version"`
	Value     any       `json:"value"`
	Operation string    `json:"operation"` // "set", "merge", "delete"
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by,omitempty"`
}

// ContextHistoryResponse represents the response from context.history.
type ContextHistoryResponse struct {
	Key        string                 `json:"key"`
	History    []*ContextHistoryEntry `json:"history"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

// ErrVersionConflict is returned when optimistic concurrency check fails.
type ErrVersionConflict struct {
	Key             string
	ExpectedVersion int64
	ActualVersion   int64
}

func (e *ErrVersionConflict) Error() string {
	return "version conflict: expected version does not match actual version"
}
