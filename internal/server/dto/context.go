package dto

import (
	"time"

	ctxengine "github.com/petal-labs/cortex/internal/context"
)

// ContextGet is the context_get response.
type ContextGet struct {
	Contract
	Key       string    `json:"key"`
	Value     any       `json:"value"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	Exists    bool      `json:"exists"`
}

// NewContextGet maps an engine get result.
func NewContextGet(r *ctxengine.GetResult) ContextGet {
	if r == nil {
		return ContextGet{Contract: Contract{SchemaVersion}}
	}
	return ContextGet{
		Contract:  Contract{SchemaVersion},
		Key:       r.Key,
		Value:     r.Value,
		Version:   r.Version,
		UpdatedAt: r.UpdatedAt,
		Exists:    r.Exists,
	}
}

// ContextSet is the context_set response.
type ContextSet struct {
	Contract
	Key             string `json:"key"`
	Version         int64  `json:"version"`
	PreviousVersion int64  `json:"previous_version"`
}

// NewContextSet maps an engine set result.
func NewContextSet(r *ctxengine.SetResult) ContextSet {
	if r == nil {
		return ContextSet{Contract: Contract{SchemaVersion}}
	}
	return ContextSet{
		Contract:        Contract{SchemaVersion},
		Key:             r.Key,
		Version:         r.Version,
		PreviousVersion: r.PreviousVersion,
	}
}

// ContextMerge is the context_merge response.
type ContextMerge struct {
	Contract
	Key         string `json:"key"`
	Version     int64  `json:"version"`
	MergedValue any    `json:"merged_value"`
}

// NewContextMerge maps an engine merge result.
func NewContextMerge(r *ctxengine.MergeResult) ContextMerge {
	if r == nil {
		return ContextMerge{Contract: Contract{SchemaVersion}}
	}
	return ContextMerge{
		Contract:    Contract{SchemaVersion},
		Key:         r.Key,
		Version:     r.Version,
		MergedValue: r.MergedValue,
	}
}

// ContextList is the context_list response.
type ContextList struct {
	Contract
	Keys       []string `json:"keys"`
	Count      int      `json:"count"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// NewContextList maps an engine list result.
func NewContextList(r *ctxengine.ListResult) ContextList {
	if r == nil {
		return ContextList{
			Contract: Contract{SchemaVersion},
			Keys:     []string{},
		}
	}
	// Keys is never nil on the wire — clients iterate it directly, so an
	// empty result emits [] rather than null.
	keys := r.Keys
	if keys == nil {
		keys = []string{}
	}
	return ContextList{
		Contract:   Contract{SchemaVersion},
		Keys:       keys,
		Count:      r.Count,
		NextCursor: r.NextCursor,
	}
}

// ContextHistoryEntry mirrors types.ContextHistoryEntry.
type ContextHistoryEntry struct {
	Version   int64     `json:"version"`
	Value     any       `json:"value"`
	Operation string    `json:"operation"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by,omitempty"`
}

// ContextHistory is the context_history response.
type ContextHistory struct {
	Contract
	Key        string                `json:"key"`
	History    []ContextHistoryEntry `json:"history"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

// NewContextHistory maps an engine history result.
func NewContextHistory(r *ctxengine.HistoryResult) ContextHistory {
	if r == nil {
		return ContextHistory{Contract: Contract{SchemaVersion}, History: []ContextHistoryEntry{}}
	}
	out := ContextHistory{
		Contract:   Contract{SchemaVersion},
		Key:        r.Key,
		History:    make([]ContextHistoryEntry, 0, len(r.History)),
		NextCursor: r.NextCursor,
	}
	for _, h := range r.History {
		if h == nil {
			continue
		}
		out.History = append(out.History, ContextHistoryEntry{
			Version:   h.Version,
			Value:     h.Value,
			Operation: h.Operation,
			UpdatedAt: h.UpdatedAt,
			UpdatedBy: h.UpdatedBy,
		})
	}
	return out
}
