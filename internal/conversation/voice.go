package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/observability"
	"github.com/petal-labs/cortex/pkg/types"
)

// Common errors returned by voice conversation memory operations.
var (
	// ErrVoiceStorageDisabled indicates voice transcript persistence was attempted
	// without opting in via VoiceConfig.TranscriptStorageEnabled.
	ErrVoiceStorageDisabled = errors.New("voice transcript storage is disabled")

	// ErrInvalidNamespace indicates an empty or whitespace-only namespace.
	ErrInvalidNamespace = errors.New("namespace cannot be empty")

	// ErrNamespaceNotAllowed indicates the namespace is excluded by the allowlist.
	ErrNamespaceNotAllowed = errors.New("namespace not allowed")

	// ErrInvalidSessionID indicates a required voice session identifier is missing.
	ErrInvalidSessionID = errors.New("session_id cannot be empty")

	// ErrInvalidRetentionDuration indicates a non-positive retention duration.
	ErrInvalidRetentionDuration = errors.New("retention duration must be positive")
)

const (
	voiceHistoryCursorPrefix = "vh:"
	voiceDedupScanLimit      = 200
)

// SetVoiceConfig configures opt-in voice transcript storage and retention policy.
// Passing nil resets to the safe default (TranscriptStorageEnabled: false).
func (e *Engine) SetVoiceConfig(cfg *config.VoiceConfig) {
	e.nsMu.Lock()
	defer e.nsMu.Unlock()
	if cfg == nil {
		d := config.DefaultConfig().Voice
		e.voiceCfg = &d
		return
	}
	cp := *cfg
	e.voiceCfg = &cp
}

// VoiceStorageEnabled reports whether opt-in voice transcript persistence is enabled.
func (e *Engine) VoiceStorageEnabled() bool {
	e.nsMu.RLock()
	defer e.nsMu.RUnlock()
	return e.voiceCfg != nil && e.voiceCfg.TranscriptStorageEnabled
}

// SetAllowedNamespaces configures an optional namespace allowlist enforced on
// voice memory operations. An empty slice permits all non-empty namespaces.
func (e *Engine) SetAllowedNamespaces(namespaces []string) {
	e.nsMu.Lock()
	defer e.nsMu.Unlock()
	if len(namespaces) == 0 {
		e.allowedNamespaces = nil
		e.allowedNamespaceList = nil
		return
	}
	allowed := make(map[string]struct{}, len(namespaces))
	list := make([]string, 0, len(namespaces))
	for _, raw := range namespaces {
		ns := strings.TrimSpace(raw)
		if ns == "" {
			continue
		}
		if _, exists := allowed[ns]; !exists {
			allowed[ns] = struct{}{}
			list = append(list, ns)
		}
	}
	e.allowedNamespaces = allowed
	e.allowedNamespaceList = list
}

func (e *Engine) validateVoiceNamespace(namespace string) (string, error) {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return "", ErrInvalidNamespace
	}
	e.nsMu.RLock()
	allowed := e.allowedNamespaces
	list := e.allowedNamespaceList
	e.nsMu.RUnlock()
	if len(allowed) > 0 {
		if _, ok := allowed[ns]; !ok {
			return "", fmt.Errorf("%w: %q (allowed: %s)", ErrNamespaceNotAllowed, ns, strings.Join(list, ", "))
		}
	}
	return ns, nil
}

func (e *Engine) lockThreadVoiceAppend(namespace, threadID string) func() {
	key := threadLockKey{namespace: namespace, threadID: threadID}

	e.voiceAppendMu.Lock()
	if e.voiceAppendLocks == nil {
		e.voiceAppendLocks = make(map[threadLockKey]*threadLock)
	}
	tl, ok := e.voiceAppendLocks[key]
	if !ok {
		tl = &threadLock{}
		e.voiceAppendLocks[key] = tl
	}
	tl.refs++
	e.voiceAppendMu.Unlock()

	tl.mu.Lock()

	return func() {
		tl.mu.Unlock()
		e.voiceAppendMu.Lock()
		tl.refs--
		if tl.refs == 0 {
			delete(e.voiceAppendLocks, key)
		}
		e.voiceAppendMu.Unlock()
	}
}

// VoiceAppendOpts configures AppendVoiceTranscript.
type VoiceAppendOpts struct {
	MaxContentLength int
	Metadata         map[string]string
	SkipEmbedding    bool
}

// AppendVoiceTranscript persists a committed voice utterance or assistant text
// response into conversation memory with session/turn correlation metadata.
//
// Enforces:
//   - Context cancellation fail-fast (writes nothing when ctx is cancelled)
//   - Opt-in persistence policy (ErrVoiceStorageDisabled unless enabled)
//   - Namespace validation and allowlist enforcement
//   - Committed-only ingestion (rejects IsFinal == false)
//   - No raw audio payloads in content or metadata
//   - Idempotent turn deduplication (prevents duplicate committed records)
//   - Privacy-safe structured logging (never logs transcript text or audio)
func (e *Engine) AppendVoiceTranscript(ctx context.Context, namespace, threadID string, record *types.VoiceTranscriptRecord, opts *VoiceAppendOpts) (*types.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !e.VoiceStorageEnabled() {
		return nil, ErrVoiceStorageDisabled
	}

	ns, err := e.validateVoiceNamespace(namespace)
	if err != nil {
		return nil, err
	}

	if record == nil {
		return nil, fmt.Errorf("%w: voice transcript record cannot be nil", types.ErrInvalidVoiceMetadata)
	}
	if err := record.Validate(); err != nil {
		return nil, err
	}

	if opts == nil {
		opts = &VoiceAppendOpts{}
	}
	if key, bad := types.HasRawAudioMetadata(opts.Metadata); bad {
		return nil, fmt.Errorf("%w: metadata key %q cannot store raw audio", types.ErrRawAudioDisallowed, key)
	}

	role := record.EffectiveRole()
	if !ValidRoles[role] {
		return nil, fmt.Errorf("%w: %s", ErrInvalidRole, role)
	}

	content := strings.TrimSpace(record.Content)
	if content == "" {
		return nil, ErrEmptyContent
	}
	if opts.MaxContentLength > 0 && len(content) > opts.MaxContentLength {
		content = content[:opts.MaxContentLength] + "\n[truncated]"
	}

	meta := record.Metadata
	meta.SessionID = strings.TrimSpace(meta.SessionID)
	meta.TurnID = strings.TrimSpace(meta.TurnID)
	if meta.SpeakerRole == "" {
		meta.SpeakerRole = role
	}
	if meta.Timestamp.IsZero() {
		meta.Timestamp = time.Now().UTC()
	} else {
		meta.Timestamp = meta.Timestamp.UTC()
	}

	tid := strings.TrimSpace(threadID)
	if tid == "" {
		tid = meta.SessionID
	}

	unlock := e.lockThreadVoiceAppend(ns, tid)
	defer unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Idempotent deduplication: avoid storing duplicate committed turns on replay.
	if existing, err := e.findDuplicateVoiceMessage(ctx, ns, tid, role, meta, content); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	// Combine ExtraMetadata -> opts.Metadata -> canonical voice.* metadata.
	mergedMeta := make(map[string]string, len(record.ExtraMetadata)+len(opts.Metadata))
	for k, v := range record.ExtraMetadata {
		mergedMeta[k] = v
	}
	for k, v := range opts.Metadata {
		mergedMeta[k] = v
	}
	mergedMeta = meta.ApplyTo(mergedMeta)

	msg := &types.Message{
		ID:        uuid.New().String(),
		Namespace: ns,
		ThreadID:  tid,
		Role:      role,
		Content:   content,
		Metadata:  mergedMeta,
		CreatedAt: meta.Timestamp,
	}

	if err := e.storage.AppendMessage(ctx, msg); err != nil {
		return nil, fmt.Errorf("failed to append voice transcript: %w", err)
	}

	// Privacy-safe structured log: correlation IDs and length only; never text or audio.
	logCtx := observability.WithTurnID(
		observability.WithSessionID(
			observability.WithThreadID(
				observability.WithNamespace(ctx, ns),
				tid,
			),
			meta.SessionID,
		),
		meta.TurnID,
	)
	observability.Debug(logCtx, "persisted committed voice transcript",
		zap.String("message_id", msg.ID),
		zap.String("role", role),
		zap.String("source", meta.Source),
		zap.String("provider", meta.Provider),
		zap.Int("content_len", len(msg.Content)),
	)

	// Optional semantic embedding (best-effort, matching Engine.Append semantics).
	if e.cfg.SemanticSearchEnabled && e.embedding != nil && !opts.SkipEmbedding && ctx.Err() == nil {
		if emb, err := e.embedding.Embed(ctx, content); err == nil {
			_ = e.storage.StoreMessageEmbedding(ctx, msg.ID, emb)
		}
	}

	if ctx.Err() == nil {
		e.enqueueForExtraction(ctx, ns, msg.ID, content)
	}

	return msg, nil
}

func (e *Engine) findDuplicateVoiceMessage(ctx context.Context, namespace, threadID, role string, meta types.VoiceMetadata, content string) (*types.Message, error) {
	if meta.TurnID == "" && meta.Sequence == 0 {
		return nil, nil
	}
	recent, _, err := e.storage.GetMessages(ctx, namespace, threadID, voiceDedupScanLimit, "")
	if err != nil {
		return nil, fmt.Errorf("failed to check duplicate voice message: %w", err)
	}
	for i := len(recent) - 1; i >= 0; i-- {
		existing := recent[i]
		vm, ok := types.ParseVoiceMetadata(existing.Metadata)
		if !ok || !vm.IsFinal {
			continue
		}
		if vm.SessionID != meta.SessionID || existing.Role != role {
			continue
		}
		if meta.Sequence > 0 && vm.Sequence > 0 {
			if vm.Sequence == meta.Sequence && vm.TurnID == meta.TurnID {
				return existing, nil
			}
			continue
		}
		if meta.TurnID != "" && vm.TurnID == meta.TurnID {
			if meta.EventType != "" && vm.EventType != "" && vm.EventType != meta.EventType {
				continue
			}
			if existing.Content == content || (meta.EventType != "" && vm.EventType == meta.EventType) {
				return existing, nil
			}
		}
	}
	return nil, nil
}

// VoiceHistoryOpts configures VoiceHistory retrieval.
type VoiceHistoryOpts struct {
	SessionID         string   // Optional: filter to a specific voice session_id
	Roles             []string // Optional: allowed roles (default: ["user", "assistant"])
	IncludeNonVoice   bool     // Optional: include standard non-voice messages in the thread
	LastN             int      // Maximum number of messages to return (0 = default history limit)
	Cursor            string   // Pagination cursor from a previous VoiceHistoryResult
	IncludeSummary    bool     // Include thread summary and return unsummarized messages alongside it
	SkipAutoSummarize bool     // Skip auto-summarization check
}

// VoiceHistoryResult contains namespace-scoped committed voice conversation
// history in chronological order (oldest first).
type VoiceHistoryResult struct {
	Messages   []*types.Message `json:"messages"`
	Summary    string           `json:"summary,omitempty"`
	NextCursor string           `json:"next_cursor,omitempty"`
	ThreadID   string           `json:"thread_id"`
	SessionID  string           `json:"session_id,omitempty"`
}

// VoiceHistory retrieves namespace-scoped committed voice transcript and
// assistant-text history in chronological order with lossless pagination and
// optional summary integration.
func (e *Engine) VoiceHistory(ctx context.Context, namespace, threadID string, opts *VoiceHistoryOpts) (*VoiceHistoryResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ns, err := e.validateVoiceNamespace(namespace)
	if err != nil {
		return nil, err
	}

	if opts == nil {
		opts = &VoiceHistoryOpts{}
	}

	sessionFilter := strings.TrimSpace(opts.SessionID)
	tid := strings.TrimSpace(threadID)
	if tid == "" && sessionFilter != "" {
		tid = sessionFilter
	}
	if tid == "" {
		return nil, fmt.Errorf("%w: thread_id or session_id is required", types.ErrInvalidVoiceMetadata)
	}

	// Trigger auto-summarization if configured and not skipped (matching Engine.History).
	if !opts.SkipAutoSummarize && e.summarizer != nil {
		should, err := e.ShouldSummarize(ctx, ns, tid)
		if err == nil && should {
			unlock := e.lockThreadSummarize(ns, tid)
			should, err = e.ShouldSummarize(ctx, ns, tid)
			if err == nil && should {
				keepRecent := 10
				if e.cfg.AutoSummarizeThreshold > 0 && e.cfg.AutoSummarizeThreshold < keepRecent {
					keepRecent = e.cfg.AutoSummarizeThreshold
				}
				_, _ = e.summarizeLocked(ctx, ns, tid, &SummarizeOpts{KeepRecent: keepRecent})
			}
			unlock()
		}
	}

	limit := opts.LastN
	if limit <= 0 {
		limit = e.cfg.DefaultHistoryLimit
		if limit <= 0 {
			limit = 20
		}
	}

	var summary string
	if opts.IncludeSummary {
		thread, err := e.storage.GetThread(ctx, ns, tid)
		if err == nil && thread.Summary != "" {
			summary = thread.Summary
		}
	}

	// Build allowed role set (defaults to user + assistant).
	allowedRoles := make(map[string]struct{}, 2)
	if len(opts.Roles) == 0 {
		allowedRoles["user"] = struct{}{}
		allowedRoles["assistant"] = struct{}{}
	} else {
		for _, r := range opts.Roles {
			r = strings.TrimSpace(r)
			if r != "" {
				allowedRoles[r] = struct{}{}
			}
		}
	}

	matches := func(m *types.Message) bool {
		if _, ok := allowedRoles[m.Role]; !ok {
			return false
		}
		vm, isVoice := types.ParseVoiceMetadata(m.Metadata)
		if !isVoice {
			return opts.IncludeNonVoice && sessionFilter == ""
		}
		if !vm.IsFinal {
			return false
		}
		if sessionFilter != "" && vm.SessionID != sessionFilter {
			return false
		}
		return true
	}

	beforeID := ""
	passedCursor := true
	storageCursor := ""
	if opts.Cursor != "" {
		if strings.HasPrefix(opts.Cursor, voiceHistoryCursorPrefix) {
			beforeID = strings.TrimPrefix(opts.Cursor, voiceHistoryCursorPrefix)
			passedCursor = false
		} else {
			storageCursor = opts.Cursor
		}
	}

	batchSize := limit * 2
	if batchSize < 50 {
		batchSize = 50
	}

	// Walk storage pages backwards (newest -> oldest) until we collect limit+1
	// matching messages or exhaust the thread.
	collectedDesc := make([]*types.Message, 0, limit+1)
	useUnsummarized := opts.IncludeSummary && summary != ""

	for len(collectedDesc) <= limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		var (
			page []*types.Message
			next string
			err  error
		)
		if useUnsummarized {
			page, next, err = e.storage.GetUnsummarizedMessages(ctx, ns, tid, batchSize, storageCursor)
		} else {
			page, next, err = e.storage.GetMessages(ctx, ns, tid, batchSize, storageCursor)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to get voice history: %w", err)
		}

		// Storage returns each page in chronological order (oldest -> newest);
		// iterate newest -> oldest so cursor advancement and limit slicing match.
		for i := len(page) - 1; i >= 0; i-- {
			m := page[i]
			if !passedCursor {
				if m.ID == beforeID {
					passedCursor = true
				}
				continue
			}
			if !matches(m) {
				continue
			}
			collectedDesc = append(collectedDesc, m)
			if len(collectedDesc) > limit {
				break
			}
		}

		if next == "" || len(collectedDesc) > limit {
			break
		}
		storageCursor = next
	}

	var nextCursor string
	if len(collectedDesc) > limit {
		collectedDesc = collectedDesc[:limit]
		if len(collectedDesc) > 0 {
			nextCursor = voiceHistoryCursorPrefix + collectedDesc[len(collectedDesc)-1].ID
		}
	}

	// Reverse to chronological order (oldest -> newest).
	messages := make([]*types.Message, len(collectedDesc))
	for i := range collectedDesc {
		messages[len(collectedDesc)-1-i] = collectedDesc[i]
	}

	return &VoiceHistoryResult{
		Messages:   messages,
		Summary:    summary,
		NextCursor: nextCursor,
		ThreadID:   tid,
		SessionID:  sessionFilter,
	}, nil
}

// DeleteVoiceSession deletes all persisted voice transcripts belonging to
// sessionID strictly within namespace.
func (e *Engine) DeleteVoiceSession(ctx context.Context, namespace, sessionID string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	ns, err := e.validateVoiceNamespace(namespace)
	if err != nil {
		return 0, err
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return 0, ErrInvalidSessionID
	}

	deleted, err := e.storage.DeleteVoiceSession(ctx, ns, sid)
	if err != nil {
		return 0, fmt.Errorf("failed to delete voice session: %w", err)
	}

	logCtx := observability.WithSessionID(observability.WithNamespace(ctx, ns), sid)
	observability.Info(logCtx, "deleted voice session transcripts",
		zap.Int64("deleted_messages", deleted),
	)

	return deleted, nil
}

// DeleteVoiceThread removes an entire voice conversation thread after validating
// namespace rules.
func (e *Engine) DeleteVoiceThread(ctx context.Context, namespace, threadID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ns, err := e.validateVoiceNamespace(namespace)
	if err != nil {
		return err
	}
	return e.Clear(ctx, ns, threadID)
}

// PurgeExpiredVoiceTranscripts deletes persisted voice transcripts older than
// olderThan across storage.
func (e *Engine) PurgeExpiredVoiceTranscripts(ctx context.Context, olderThan time.Duration) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if olderThan <= 0 {
		return 0, ErrInvalidRetentionDuration
	}

	deleted, err := e.storage.DeleteOldVoiceTranscripts(ctx, olderThan)
	if err != nil {
		return 0, fmt.Errorf("failed to purge expired voice transcripts: %w", err)
	}

	observability.Info(ctx, "purged expired voice transcripts",
		zap.Duration("older_than", olderThan),
		zap.Int64("deleted_messages", deleted),
	)

	return deleted, nil
}
