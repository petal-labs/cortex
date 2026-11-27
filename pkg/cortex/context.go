package cortex

import (
	"context"
	"errors"
	"fmt"
	"time"

	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/pkg/types"
)

// ContextClient provides typed access to Cortex Workflow Context.
type ContextClient struct {
	engine *ctxengine.Engine
}

// ContextGetOpts configures ContextClient.Get.
type ContextGetOpts struct {
	RunID *string
}

// ContextGetResult contains the result of ContextClient.Get.
type ContextGetResult struct {
	Key       string    `json:"key"`
	Value     any       `json:"value"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	Exists    bool      `json:"exists"`
}

// ContextSetOpts configures ContextClient.Set.
type ContextSetOpts struct {
	RunID           *string
	ExpectedVersion *int64
	TTL             time.Duration
	UpdatedBy       string
}

// ContextSetResult contains the result of ContextClient.Set.
type ContextSetResult struct {
	Key             string `json:"key"`
	Version         int64  `json:"version"`
	PreviousVersion int64  `json:"previous_version"`
}

// ContextMergeOpts configures ContextClient.Merge.
type ContextMergeOpts struct {
	RunID           *string
	ExpectedVersion *int64
	Strategy        types.MergeStrategy
	ArrayStrategy   types.ArrayStrategy
	UpdatedBy       string
}

// ContextMergeResult contains the result of ContextClient.Merge.
type ContextMergeResult struct {
	Key         string `json:"key"`
	Version     int64  `json:"version"`
	MergedValue any    `json:"merged_value"`
}

// ContextListOpts configures ContextClient.List.
type ContextListOpts struct {
	RunID  *string
	Prefix *string
	Cursor string
	Limit  int
}

// ContextListResult contains the result of ContextClient.List.
type ContextListResult struct {
	Keys       []string `json:"keys"`
	Count      int      `json:"count"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// ContextHistoryOpts configures ContextClient.History.
type ContextHistoryOpts struct {
	RunID  *string
	Cursor string
	Limit  int
}

// ContextHistoryResult contains the result of ContextClient.History.
type ContextHistoryResult struct {
	Key        string                       `json:"key"`
	History    []*types.ContextHistoryEntry `json:"history"`
	NextCursor string                       `json:"next_cursor,omitempty"`
}

// Get retrieves a workflow context entry by key.
func (c *ContextClient) Get(ctx context.Context, namespace, key string, opts *ContextGetOpts) (*ContextGetResult, error) {
	var internalOpts *ctxengine.GetOpts
	if opts != nil {
		internalOpts = &ctxengine.GetOpts{RunID: opts.RunID}
	}
	res, err := c.engine.Get(ctx, namespace, key, internalOpts)
	if err != nil {
		return nil, mapContextErr(err)
	}
	return &ContextGetResult{
		Key:       res.Key,
		Value:     res.Value,
		Version:   res.Version,
		UpdatedAt: res.UpdatedAt,
		Exists:    res.Exists,
	}, nil
}

// Set stores a workflow context entry with optional optimistic locking and TTL.
func (c *ContextClient) Set(ctx context.Context, namespace, key string, value any, opts *ContextSetOpts) (*ContextSetResult, error) {
	var internalOpts *ctxengine.SetOpts
	if opts != nil {
		internalOpts = &ctxengine.SetOpts{
			RunID:           opts.RunID,
			ExpectedVersion: opts.ExpectedVersion,
			TTL:             opts.TTL,
			UpdatedBy:       opts.UpdatedBy,
		}
	}
	res, err := c.engine.Set(ctx, namespace, key, value, internalOpts)
	if err != nil {
		return nil, mapContextErr(err)
	}
	return &ContextSetResult{
		Key:             res.Key,
		Version:         res.Version,
		PreviousVersion: res.PreviousVersion,
	}, nil
}

// Merge combines value with an existing context entry using the requested strategy.
func (c *ContextClient) Merge(ctx context.Context, namespace, key string, value any, opts *ContextMergeOpts) (*ContextMergeResult, error) {
	var internalOpts *ctxengine.MergeOpts
	if opts != nil {
		internalOpts = &ctxengine.MergeOpts{
			RunID:           opts.RunID,
			ExpectedVersion: opts.ExpectedVersion,
			Strategy:        opts.Strategy,
			ArrayStrategy:   opts.ArrayStrategy,
			UpdatedBy:       opts.UpdatedBy,
		}
	}
	res, err := c.engine.Merge(ctx, namespace, key, value, internalOpts)
	if err != nil {
		return nil, mapContextErr(err)
	}
	return &ContextMergeResult{
		Key:         res.Key,
		Version:     res.Version,
		MergedValue: res.MergedValue,
	}, nil
}

// List returns context keys matching optional run ID and prefix filters.
func (c *ContextClient) List(ctx context.Context, namespace string, opts *ContextListOpts) (*ContextListResult, error) {
	var internalOpts *ctxengine.ListOpts
	if opts != nil {
		internalOpts = &ctxengine.ListOpts{
			RunID:  opts.RunID,
			Prefix: opts.Prefix,
			Cursor: opts.Cursor,
			Limit:  opts.Limit,
		}
	}
	res, err := c.engine.List(ctx, namespace, internalOpts)
	if err != nil {
		return nil, mapContextErr(err)
	}
	return &ContextListResult{
		Keys:       res.Keys,
		Count:      res.Count,
		NextCursor: res.NextCursor,
	}, nil
}

// Delete removes a context entry.
func (c *ContextClient) Delete(ctx context.Context, namespace, key string, runID *string) error {
	return mapContextErr(c.engine.Delete(ctx, namespace, key, runID))
}

// History retrieves the version history for a context key.
func (c *ContextClient) History(ctx context.Context, namespace, key string, opts *ContextHistoryOpts) (*ContextHistoryResult, error) {
	var internalOpts *ctxengine.HistoryOpts
	if opts != nil {
		internalOpts = &ctxengine.HistoryOpts{
			RunID:  opts.RunID,
			Cursor: opts.Cursor,
			Limit:  opts.Limit,
		}
	}
	res, err := c.engine.History(ctx, namespace, key, internalOpts)
	if err != nil {
		return nil, mapContextErr(err)
	}
	return &ContextHistoryResult{
		Key:        res.Key,
		History:    res.History,
		NextCursor: res.NextCursor,
	}, nil
}

// CleanupRun deletes all run-scoped context entries for runID in namespace.
func (c *ContextClient) CleanupRun(ctx context.Context, namespace, runID string) error {
	return mapContextErr(c.engine.CleanupRun(ctx, namespace, runID))
}

// CleanupExpired deletes all context entries past their TTL expiration.
func (c *ContextClient) CleanupExpired(ctx context.Context) (int64, error) {
	n, err := c.engine.CleanupExpired(ctx)
	if err != nil {
		return 0, mapContextErr(err)
	}
	return n, nil
}

func mapContextErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ctxengine.ErrKeyNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, ctxengine.ErrVersionConflict):
		return fmt.Errorf("%w: %v", ErrVersionConflict, err)
	case errors.Is(err, ctxengine.ErrEmptyKey), errors.Is(err, ctxengine.ErrInvalidMerge):
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	default:
		return err
	}
}
