package cortex

import (
	"context"
	"errors"
	"fmt"

	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/pkg/types"
)

// ConversationClient provides typed access to Cortex Conversation Memory.
type ConversationClient struct {
	engine *conversation.Engine
}

// AppendOpts configures ConversationClient.Append.
type AppendOpts struct {
	MaxContentLength int
	Metadata         map[string]string
	SkipEmbedding    bool
}

// HistoryOpts configures ConversationClient.History.
type HistoryOpts struct {
	LastN             int
	IncludeSummary    bool
	Cursor            string
	SkipAutoSummarize bool
}

// HistoryResult contains the result of ConversationClient.History.
type HistoryResult struct {
	Messages   []*types.Message `json:"messages"`
	Summary    string           `json:"summary,omitempty"`
	NextCursor string           `json:"next_cursor,omitempty"`
	ThreadID   string           `json:"thread_id"`
}

// ConversationSearchOpts configures ConversationClient.Search.
type ConversationSearchOpts struct {
	ThreadID   *string
	TopK       int
	MinScore   float64
	SearchMode SearchMode
	Alpha      float64
}

// ConversationSearchResult contains the result of ConversationClient.Search.
type ConversationSearchResult struct {
	Results []*types.MessageResult `json:"results"`
	Query   string                 `json:"query"`
}

// SummarizeOpts configures ConversationClient.Summarize.
type SummarizeOpts struct {
	KeepRecent int
}

// SummarizeResult contains the result of ConversationClient.Summarize.
type SummarizeResult struct {
	Summary            string `json:"summary"`
	MessagesSummarized int    `json:"messages_summarized"`
	MessagesKept       int    `json:"messages_kept"`
	ThreadID           string `json:"thread_id"`
}

// Append adds a message to a conversation thread, creating the thread if needed.
func (c *ConversationClient) Append(ctx context.Context, namespace, threadID, role, content string, opts *AppendOpts) (*types.Message, error) {
	var internalOpts *conversation.AppendOpts
	if opts != nil {
		internalOpts = &conversation.AppendOpts{
			MaxContentLength: opts.MaxContentLength,
			Metadata:         opts.Metadata,
			SkipEmbedding:    opts.SkipEmbedding,
		}
	}
	msg, err := c.engine.Append(ctx, namespace, threadID, role, content, internalOpts)
	if err != nil {
		return nil, mapConversationErr(err)
	}
	return msg, nil
}

// History retrieves chronological message history for a thread.
func (c *ConversationClient) History(ctx context.Context, namespace, threadID string, opts *HistoryOpts) (*HistoryResult, error) {
	var internalOpts *conversation.HistoryOpts
	if opts != nil {
		internalOpts = &conversation.HistoryOpts{
			LastN:             opts.LastN,
			IncludeSummary:    opts.IncludeSummary,
			Cursor:            opts.Cursor,
			SkipAutoSummarize: opts.SkipAutoSummarize,
		}
	}
	res, err := c.engine.History(ctx, namespace, threadID, internalOpts)
	if err != nil {
		return nil, mapConversationErr(err)
	}
	return &HistoryResult{
		Messages:   res.Messages,
		Summary:    res.Summary,
		NextCursor: res.NextCursor,
		ThreadID:   res.ThreadID,
	}, nil
}

// Search performs vector, hybrid, or text search across conversation messages.
func (c *ConversationClient) Search(ctx context.Context, namespace, query string, opts *ConversationSearchOpts) (*ConversationSearchResult, error) {
	var internalOpts *conversation.SearchOpts
	if opts != nil {
		internalOpts = &conversation.SearchOpts{
			ThreadID:   opts.ThreadID,
			TopK:       opts.TopK,
			MinScore:   opts.MinScore,
			SearchMode: conversation.SearchMode(opts.SearchMode),
			Alpha:      opts.Alpha,
		}
	}
	res, err := c.engine.Search(ctx, namespace, query, internalOpts)
	if err != nil {
		return nil, mapConversationErr(err)
	}
	return &ConversationSearchResult{
		Results: res.Results,
		Query:   res.Query,
	}, nil
}

// Summarize compresses older messages in a thread into a summary using the configured Summarizer.
func (c *ConversationClient) Summarize(ctx context.Context, namespace, threadID string, opts *SummarizeOpts) (*SummarizeResult, error) {
	var internalOpts *conversation.SummarizeOpts
	if opts != nil {
		internalOpts = &conversation.SummarizeOpts{
			KeepRecent: opts.KeepRecent,
		}
	}
	res, err := c.engine.Summarize(ctx, namespace, threadID, internalOpts)
	if err != nil {
		return nil, mapConversationErr(err)
	}
	return &SummarizeResult{
		Summary:            res.Summary,
		MessagesSummarized: res.MessagesSummarized,
		MessagesKept:       res.MessagesKept,
		ThreadID:           res.ThreadID,
	}, nil
}

// ListThreads lists threads in a namespace with cursor pagination.
func (c *ConversationClient) ListThreads(ctx context.Context, namespace, cursor string, limit int) ([]*types.Thread, string, error) {
	threads, nextCursor, err := c.engine.ListThreads(ctx, namespace, cursor, limit)
	if err != nil {
		return nil, "", mapConversationErr(err)
	}
	return threads, nextCursor, nil
}

// GetThread retrieves metadata for a single thread.
func (c *ConversationClient) GetThread(ctx context.Context, namespace, threadID string) (*types.Thread, error) {
	thread, err := c.engine.GetThread(ctx, namespace, threadID)
	if err != nil {
		return nil, mapConversationErr(err)
	}
	return thread, nil
}

// Clear removes a thread and all of its messages.
func (c *ConversationClient) Clear(ctx context.Context, namespace, threadID string) error {
	return mapConversationErr(c.engine.Clear(ctx, namespace, threadID))
}

func mapConversationErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, conversation.ErrThreadNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, conversation.ErrEmptyContent), errors.Is(err, conversation.ErrInvalidRole):
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	default:
		return err
	}
}
