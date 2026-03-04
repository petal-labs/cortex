package conversation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// Common errors returned by the conversation engine.
var (
	ErrEmptyContent        = errors.New("message content cannot be empty")
	ErrInvalidRole         = errors.New("invalid message role")
	ErrThreadNotFound      = errors.New("thread not found")
	ErrNothingToSummarize  = errors.New("not enough messages to summarize")
	ErrSummarizerNotSet    = errors.New("summarizer not configured")
)

// Summarizer generates summaries from conversation messages.
type Summarizer interface {
	SummarizeMessages(ctx context.Context, messages []*types.Message) (string, error)
}

// ExtractionEnqueuer queues content for entity extraction.
type ExtractionEnqueuer interface {
	EnqueueForExtraction(ctx context.Context, namespace, sourceType, sourceID, content string) error
}

// ValidRoles defines the allowed message roles.
var ValidRoles = map[string]bool{
	"user":      true,
	"assistant": true,
	"system":    true,
	"tool":      true,
}

// Engine implements the conversation memory logic layer.
// It orchestrates storage and embedding operations for conversation management.
type Engine struct {
	storage             storage.Backend
	embedding           embedding.Provider
	summarizer          Summarizer
	extractionEnqueuer  ExtractionEnqueuer
	cfg                 *config.ConversationConfig
}

// NewEngine creates a new conversation engine.
// The embedding provider can be nil if semantic search is disabled.
// The summarizer can be nil if summarization is not needed.
func NewEngine(store storage.Backend, emb embedding.Provider, cfg *config.ConversationConfig) (*Engine, error) {
	if store == nil {
		return nil, errors.New("storage backend is required")
	}

	if cfg == nil {
		defaultCfg := config.DefaultConfig()
		cfg = &defaultCfg.Conversation
	}

	return &Engine{
		storage:   store,
		embedding: emb,
		cfg:       cfg,
	}, nil
}

// SetSummarizer sets the summarizer for conversation summarization.
// This is optional and can be called after engine creation.
func (e *Engine) SetSummarizer(s Summarizer) {
	e.summarizer = s
}

// SetExtractionEnqueuer sets the extraction enqueuer for entity extraction.
// When set, messages will be queued for background entity extraction.
func (e *Engine) SetExtractionEnqueuer(eq ExtractionEnqueuer) {
	e.extractionEnqueuer = eq
}

// AppendOpts contains options for appending a message.
type AppendOpts struct {
	MaxContentLength int               // Truncate content if exceeds this (0 = no truncation)
	Metadata         map[string]string // Optional metadata
	SkipEmbedding    bool              // Skip embedding generation even if enabled
}

// Append adds a message to a conversation thread.
// Creates the thread if it doesn't exist.
// Generates embedding if semantic search is enabled.
func (e *Engine) Append(ctx context.Context, namespace, threadID, role, content string, opts *AppendOpts) (*types.Message, error) {
	if content == "" {
		return nil, ErrEmptyContent
	}

	if !ValidRoles[role] {
		return nil, fmt.Errorf("%w: %s", ErrInvalidRole, role)
	}

	if opts == nil {
		opts = &AppendOpts{}
	}

	// Truncate content if needed
	if opts.MaxContentLength > 0 && len(content) > opts.MaxContentLength {
		content = content[:opts.MaxContentLength] + "\n[truncated]"
	}

	// Generate thread ID if not provided
	if threadID == "" {
		threadID = uuid.New().String()
	}

	// Create message
	msg := &types.Message{
		ID:        uuid.New().String(),
		Namespace: namespace,
		ThreadID:  threadID,
		Role:      role,
		Content:   content,
		Metadata:  opts.Metadata,
		CreatedAt: time.Now().UTC(),
	}

	// Store message (this also creates thread if needed)
	if err := e.storage.AppendMessage(ctx, msg); err != nil {
		return nil, fmt.Errorf("failed to append message: %w", err)
	}

	// Generate and store embedding if enabled
	if e.cfg.SemanticSearchEnabled && e.embedding != nil && !opts.SkipEmbedding {
		emb, err := e.embedding.Embed(ctx, content)
		if err != nil {
			// Log error but don't fail the append
			// The message is stored, just not searchable
			// Still try to queue for extraction
			e.enqueueForExtraction(ctx, namespace, msg.ID, content)
			return msg, nil
		}

		if err := e.storage.StoreMessageEmbedding(ctx, msg.ID, emb); err != nil {
			// Same - log but don't fail
			// Still try to queue for extraction
			e.enqueueForExtraction(ctx, namespace, msg.ID, content)
			return msg, nil
		}
	}

	// Queue message for entity extraction if enqueuer is configured
	e.enqueueForExtraction(ctx, namespace, msg.ID, content)

	return msg, nil
}

// enqueueForExtraction queues content for entity extraction.
// This is a fire-and-forget operation that logs errors but doesn't fail the caller.
func (e *Engine) enqueueForExtraction(ctx context.Context, namespace, messageID, content string) {
	if e.extractionEnqueuer == nil {
		return
	}

	// Fire and forget - extraction is best-effort
	if err := e.extractionEnqueuer.EnqueueForExtraction(ctx, namespace, "conversation", messageID, content); err != nil {
		// Log but don't fail - extraction is optional
		// In production, use structured logging
		_ = err
	}
}

// HistoryOpts contains options for retrieving conversation history.
type HistoryOpts struct {
	LastN              int    // Number of recent messages to retrieve (0 = use default)
	IncludeSummary     bool   // Prepend thread summary if available
	Cursor             string // Pagination cursor
	SkipAutoSummarize  bool   // Skip auto-summarization check (internal use)
}

// HistoryResult contains the result of a history query.
type HistoryResult struct {
	Messages   []*types.Message `json:"messages"`
	Summary    string           `json:"summary,omitempty"`
	NextCursor string           `json:"next_cursor,omitempty"`
	ThreadID   string           `json:"thread_id"`
}

// History retrieves conversation history for a thread.
// Returns messages in chronological order (oldest first).
// Auto-triggers summarization if message count exceeds threshold.
func (e *Engine) History(ctx context.Context, namespace, threadID string, opts *HistoryOpts) (*HistoryResult, error) {
	if opts == nil {
		opts = &HistoryOpts{}
	}

	// Check for auto-summarization before fetching history
	if !opts.SkipAutoSummarize && e.summarizer != nil {
		should, err := e.ShouldSummarize(ctx, namespace, threadID)
		if err == nil && should {
			// Trigger summarization with default keep_recent (10)
			_, _ = e.Summarize(ctx, namespace, threadID, nil)
			// Ignore errors - summarization is best-effort
			// The history will still be returned even if summarization fails
		}
	}

	limit := opts.LastN
	if limit <= 0 {
		limit = e.cfg.DefaultHistoryLimit
	}

	// Get messages from storage
	messages, nextCursor, err := e.storage.GetMessages(ctx, namespace, threadID, limit, opts.Cursor)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}

	result := &HistoryResult{
		Messages:   messages,
		NextCursor: nextCursor,
		ThreadID:   threadID,
	}

	// Include summary if requested
	if opts.IncludeSummary {
		thread, err := e.storage.GetThread(ctx, namespace, threadID)
		if err == nil && thread.Summary != "" {
			result.Summary = thread.Summary
		}
		// Ignore error - thread might not exist yet or have no summary
	}

	return result, nil
}

// SearchOpts contains options for semantic search.
type SearchOpts struct {
	ThreadID *string // Optional: limit search to specific thread
	TopK     int     // Number of results (0 = default 10)
	MinScore float64 // Minimum similarity score (0-1)
}

// SearchResult contains search results.
type SearchResult struct {
	Results []*types.MessageResult `json:"results"`
	Query   string                 `json:"query"`
}

// Search performs semantic search across messages in a namespace.
// Requires semantic search to be enabled and embedding provider configured.
func (e *Engine) Search(ctx context.Context, namespace, query string, opts *SearchOpts) (*SearchResult, error) {
	if !e.cfg.SemanticSearchEnabled {
		return nil, errors.New("semantic search is disabled")
	}

	if e.embedding == nil {
		return nil, errors.New("embedding provider not configured")
	}

	if query == "" {
		return nil, errors.New("search query cannot be empty")
	}

	if opts == nil {
		opts = &SearchOpts{}
	}

	// Generate query embedding
	queryEmb, err := e.embedding.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Search messages
	searchOpts := storage.MessageSearchOpts{
		TopK:     opts.TopK,
		MinScore: opts.MinScore,
		ThreadID: opts.ThreadID,
	}

	if searchOpts.TopK <= 0 {
		searchOpts.TopK = 10
	}

	results, err := e.storage.SearchMessages(ctx, namespace, queryEmb, searchOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to search messages: %w", err)
	}

	return &SearchResult{
		Results: results,
		Query:   query,
	}, nil
}

// Clear deletes all messages in a thread and the thread itself.
func (e *Engine) Clear(ctx context.Context, namespace, threadID string) error {
	err := e.storage.DeleteThread(ctx, namespace, threadID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrThreadNotFound
		}
		return fmt.Errorf("failed to clear thread: %w", err)
	}
	return nil
}

// ListThreads returns all threads in a namespace.
func (e *Engine) ListThreads(ctx context.Context, namespace string, cursor string, limit int) ([]*types.Thread, string, error) {
	if limit <= 0 {
		limit = 50
	}

	threads, nextCursor, err := e.storage.ListThreads(ctx, namespace, cursor, limit)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list threads: %w", err)
	}

	return threads, nextCursor, nil
}

// GetThread retrieves a single thread by ID.
func (e *Engine) GetThread(ctx context.Context, namespace, threadID string) (*types.Thread, error) {
	thread, err := e.storage.GetThread(ctx, namespace, threadID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrThreadNotFound
		}
		return nil, fmt.Errorf("failed to get thread: %w", err)
	}
	return thread, nil
}

// UpdateThread updates a thread's metadata (title, summary).
func (e *Engine) UpdateThread(ctx context.Context, thread *types.Thread) error {
	if err := e.storage.UpdateThread(ctx, thread); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrThreadNotFound
		}
		return fmt.Errorf("failed to update thread: %w", err)
	}
	return nil
}

// MarkSummarized marks messages as having been included in a summary.
// This is used after summarization to track which messages were summarized.
func (e *Engine) MarkSummarized(ctx context.Context, namespace, threadID string, beforeTime time.Time) error {
	return e.storage.MarkMessagesSummarized(ctx, namespace, threadID, beforeTime.Unix())
}

// SummarizeOpts contains options for summarization.
type SummarizeOpts struct {
	KeepRecent int // Number of recent messages to keep unsummarized (default: 10)
}

// SummarizeResult contains the result of summarization.
type SummarizeResult struct {
	Summary            string `json:"summary"`
	MessagesSummarized int    `json:"messages_summarized"`
	MessagesKept       int    `json:"messages_kept"`
	ThreadID           string `json:"thread_id"`
}

// Summarize compresses older messages into a summary via LLM.
// The summary is stored in the thread record and older messages are marked as summarized.
// Recent messages (controlled by KeepRecent) are left unsummarized.
func (e *Engine) Summarize(ctx context.Context, namespace, threadID string, opts *SummarizeOpts) (*SummarizeResult, error) {
	if e.summarizer == nil {
		return nil, ErrSummarizerNotSet
	}

	if opts == nil {
		opts = &SummarizeOpts{KeepRecent: 10}
	}

	if opts.KeepRecent <= 0 {
		opts.KeepRecent = 10
	}

	// Get all messages in the thread (we need to retrieve all for summarization)
	// Use a large limit to get all messages
	allMessages, _, err := e.storage.GetMessages(ctx, namespace, threadID, 10000, "")
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}

	// Check if there are enough messages to summarize
	if len(allMessages) <= opts.KeepRecent {
		return nil, ErrNothingToSummarize
	}

	// Separate messages into "to summarize" and "to keep"
	splitIndex := len(allMessages) - opts.KeepRecent
	toSummarize := allMessages[:splitIndex]

	// Get the thread to check for existing summary
	thread, err := e.storage.GetThread(ctx, namespace, threadID)
	if err != nil {
		return nil, fmt.Errorf("failed to get thread: %w", err)
	}

	// If there's an existing summary, prepend it to the messages to summarize
	// This creates a rolling summary that incorporates previous summaries
	if thread.Summary != "" {
		summaryMsg := &types.Message{
			Role:    "system",
			Content: fmt.Sprintf("[Previous summary]: %s", thread.Summary),
		}
		toSummarize = append([]*types.Message{summaryMsg}, toSummarize...)
	}

	// Generate the summary
	summary, err := e.summarizer.SummarizeMessages(ctx, toSummarize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate summary: %w", err)
	}

	// Update the thread with the new summary
	thread.Summary = summary
	thread.UpdatedAt = time.Now().UTC()
	if err := e.storage.UpdateThread(ctx, thread); err != nil {
		return nil, fmt.Errorf("failed to update thread with summary: %w", err)
	}

	// Mark the summarized messages
	// Use the timestamp of the last summarized message + 1 second
	// to include it in the "before" comparison (storage uses <)
	lastSummarizedTime := allMessages[splitIndex-1].CreatedAt
	if err := e.storage.MarkMessagesSummarized(ctx, namespace, threadID, lastSummarizedTime.Unix()+1); err != nil {
		return nil, fmt.Errorf("failed to mark messages as summarized: %w", err)
	}

	return &SummarizeResult{
		Summary:            summary,
		MessagesSummarized: splitIndex,
		MessagesKept:       len(allMessages) - splitIndex,
		ThreadID:           threadID,
	}, nil
}

// ShouldSummarize checks if a thread should be auto-summarized based on message count.
func (e *Engine) ShouldSummarize(ctx context.Context, namespace, threadID string) (bool, error) {
	if e.cfg.AutoSummarizeThreshold <= 0 {
		return false, nil
	}

	// Get message count (use a minimal query)
	messages, _, err := e.storage.GetMessages(ctx, namespace, threadID, e.cfg.AutoSummarizeThreshold+1, "")
	if err != nil {
		return false, err
	}

	return len(messages) > e.cfg.AutoSummarizeThreshold, nil
}
