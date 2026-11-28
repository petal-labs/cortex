package cortex

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/petal-labs/iris/v2/core"

	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/pkg/types"
)

// DefaultSummaryPrefix is prepended to a thread's rolling summary when injected
// as a system message for Iris reasoning context.
const DefaultSummaryPrefix = "[Conversation summary]: "

// VoiceClient provides typed, privacy-first access to Cortex voice transcript
// memory, correlation metadata, Iris context injection, and retention controls.
type VoiceClient struct {
	engine *conversation.Engine
}

// StorageEnabled reports whether opt-in voice transcript persistence is enabled.
func (v *VoiceClient) StorageEnabled() bool {
	return v.engine.VoiceStorageEnabled()
}

// VoiceRecordOpts configures AppendTranscript and RecordVoiceEvent.
type VoiceRecordOpts struct {
	SessionID        string            // Fallback session_id if event omits one
	Source           string            // Override source attribution (default: "stt" / "agent")
	Provider         string            // Provider attribution (e.g., "elevenlabs", "iris_pipeline")
	Model            string            // Model attribution (e.g., "scribe_v2")
	MaxContentLength int               // Optional character truncation limit (0 = no truncation)
	Metadata         map[string]string // Optional extra non-audio metadata
	SkipEmbedding    bool              // Skip embedding generation even if semantic search is enabled
}

// VoiceTranscriptionOpts configures RecordTranscription for file/reader-based
// Iris Scribe transcriptions.
type VoiceTranscriptionOpts struct {
	SessionID        string            // Required unless resp.ID is set
	TurnID           string            // Optional turn identifier
	Role             string            // Speaker role (default: "user")
	SpeakerID        string            // Optional speaker label override (auto-extracted from diarization when unambiguous)
	Source           string            // Source attribution (default: "transcription")
	Provider         string            // Provider attribution (e.g., "elevenlabs")
	Timestamp        time.Time         // Utterance timestamp (default: now UTC)
	MaxContentLength int               // Optional character truncation limit
	Metadata         map[string]string // Optional extra non-audio metadata
	SkipEmbedding    bool              // Skip embedding generation
}

// VoiceHistoryOpts configures VoiceClient.History.
type VoiceHistoryOpts struct {
	SessionID         string   // Optional: filter to a specific voice session_id
	Roles             []string // Optional: allowed roles (default: ["user", "assistant"])
	IncludeNonVoice   bool     // Optional: include standard non-voice messages in the thread
	LastN             int      // Maximum messages to return (0 = default history limit)
	Cursor            string   // Opaque pagination cursor from a prior VoiceHistoryResult
	IncludeSummary    bool     // Include thread summary and return unsummarized messages alongside it
	SkipAutoSummarize bool     // Skip auto-summarization trigger
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

// IrisContextOpts configures VoiceClient.IrisMessages for injecting Cortex voice
// conversation history into Iris reasoning requests.
type IrisContextOpts struct {
	SessionID         string   // Optional: filter to a specific voice session_id
	Roles             []string // Optional: allowed roles (default: ["user", "assistant"])
	IncludeNonVoice   bool     // Optional: include standard non-voice messages
	LastN             int      // Maximum messages to retrieve from storage
	Cursor            string   // Optional pagination cursor
	IncludeSummary    bool     // Include thread summary in leading system context
	SkipAutoSummarize bool     // Skip auto-summarization trigger
	SystemPrompt      string   // Optional system prompt prepended ahead of summary and history
	SummaryPrefix     string   // Prefix before thread summary (default: DefaultSummaryPrefix)
	MaxMessages       int      // Optional cap on non-system messages returned (0 = uncapped)
}

// AppendTranscript persists a committed voice transcript record with correlation
// metadata, enforcing opt-in storage, namespace isolation, committed-only rules,
// and no raw audio persistence.
func (v *VoiceClient) AppendTranscript(ctx context.Context, namespace, threadID string, record *types.VoiceTranscriptRecord, opts *VoiceRecordOpts) (*types.Message, error) {
	var internalOpts *conversation.VoiceAppendOpts
	if opts != nil {
		internalOpts = &conversation.VoiceAppendOpts{
			MaxContentLength: opts.MaxContentLength,
			Metadata:         opts.Metadata,
			SkipEmbedding:    opts.SkipEmbedding,
		}
	}
	msg, err := v.engine.AppendVoiceTranscript(ctx, namespace, threadID, record, internalOpts)
	if err != nil {
		return nil, mapVoiceErr(err)
	}
	return msg, nil
}

// RecordVoiceEvent inspects an Iris core.VoiceEvent and persists ONLY committed
// user transcripts (VoiceEventUserTranscriptCommitted with IsFinal=true) and
// committed assistant text (VoiceEventAgentTextCommitted with IsFinal=true).
//
// Ephemeral partials, assistant deltas, synthesized audio chunks, and lifecycle
// events are ignored safely and return (nil, nil). Raw audio payloads are never
// persisted or logged.
func (v *VoiceClient) RecordVoiceEvent(ctx context.Context, namespace, threadID string, evt core.VoiceEvent, opts *VoiceRecordOpts) (*types.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if opts == nil {
		opts = &VoiceRecordOpts{}
	}

	switch evt.Type {
	case core.VoiceEventUserTranscriptCommitted:
		if !evt.IsFinal {
			return nil, fmt.Errorf("%w: %w", ErrInvalidInput, ErrUncommittedTranscript)
		}
		if evt.Transcript == nil {
			return nil, fmt.Errorf("%w: user_transcript_committed event requires transcript", ErrInvalidInput)
		}
		if !evt.Transcript.IsFinal {
			return nil, fmt.Errorf("%w: %w", ErrInvalidInput, ErrUncommittedTranscript)
		}
		if err := evt.Transcript.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		text := strings.TrimSpace(evt.Transcript.Text)
		if text == "" {
			return nil, fmt.Errorf("%w: committed user transcript text cannot be empty", ErrInvalidInput)
		}

		sessionID := strings.TrimSpace(evt.SessionID)
		if sessionID == "" {
			sessionID = strings.TrimSpace(opts.SessionID)
		}
		source := strings.TrimSpace(opts.Source)
		if source == "" {
			source = types.VoiceSourceSTT
		}
		model := strings.TrimSpace(string(evt.Transcript.Model))
		if model == "" {
			model = strings.TrimSpace(opts.Model)
		}

		role := string(evt.Role)
		if role == "" {
			role = "user"
		}

		rec := &types.VoiceTranscriptRecord{
			Role:    role,
			Content: text,
			Metadata: types.VoiceMetadata{
				SessionID:   sessionID,
				TurnID:      strings.TrimSpace(evt.TurnID),
				SpeakerRole: role,
				SpeakerID:   extractDominantSpeakerID(evt.Transcript),
				Source:      source,
				Provider:    strings.TrimSpace(opts.Provider),
				Model:       model,
				Language:    strings.TrimSpace(evt.Transcript.Language),
				EventType:   string(evt.Type),
				Sequence:    evt.Sequence,
				Timestamp:   evt.Timestamp,
				Duration:    evt.Transcript.Duration,
				IsFinal:     true,
			},
			ExtraMetadata: evt.Metadata,
		}
		return v.AppendTranscript(ctx, namespace, threadID, rec, opts)

	case core.VoiceEventAgentTextCommitted:
		if !evt.IsFinal {
			return nil, fmt.Errorf("%w: %w", ErrInvalidInput, ErrUncommittedTranscript)
		}
		text := strings.TrimSpace(evt.Text)
		if text == "" {
			return nil, fmt.Errorf("%w: committed agent text cannot be empty", ErrInvalidInput)
		}

		sessionID := strings.TrimSpace(evt.SessionID)
		if sessionID == "" {
			sessionID = strings.TrimSpace(opts.SessionID)
		}
		source := strings.TrimSpace(opts.Source)
		if source == "" {
			source = types.VoiceSourceAgent
		}
		role := string(evt.Role)
		if role == "" {
			role = "assistant"
		}

		rec := &types.VoiceTranscriptRecord{
			Role:    role,
			Content: text,
			Metadata: types.VoiceMetadata{
				SessionID:   sessionID,
				TurnID:      strings.TrimSpace(evt.TurnID),
				SpeakerRole: role,
				Source:      source,
				Provider:    strings.TrimSpace(opts.Provider),
				Model:       strings.TrimSpace(opts.Model),
				EventType:   string(evt.Type),
				Sequence:    evt.Sequence,
				Timestamp:   evt.Timestamp,
				IsFinal:     true,
			},
			ExtraMetadata: evt.Metadata,
		}
		return v.AppendTranscript(ctx, namespace, threadID, rec, opts)

	default:
		// Ignore partial transcripts, streaming deltas, audio output chunks, and
		// session/turn/tool lifecycle events without persisting or logging content.
		return nil, nil
	}
}

// RecordTranscription persists a finalized Iris core.TranscriptionResponse
// (such as from file/reader-based ElevenLabs Scribe transcription) into
// conversation memory with session, turn, language, speaker, and timing metadata.
func (v *VoiceClient) RecordTranscription(ctx context.Context, namespace, threadID string, resp *core.TranscriptionResponse, opts *VoiceTranscriptionOpts) (*types.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("%w: transcription response cannot be nil", ErrInvalidInput)
	}
	if err := resp.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if !resp.IsFinal {
		return nil, fmt.Errorf("%w: %w", ErrInvalidInput, ErrUncommittedTranscript)
	}
	text := strings.TrimSpace(resp.Text)
	if text == "" {
		return nil, fmt.Errorf("%w: transcription text cannot be empty", ErrInvalidInput)
	}

	if opts == nil {
		opts = &VoiceTranscriptionOpts{}
	}

	sessionID := strings.TrimSpace(opts.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(resp.ID)
	}
	if sessionID == "" {
		return nil, fmt.Errorf("%w: session_id is required", ErrInvalidInput)
	}

	role := strings.TrimSpace(opts.Role)
	if role == "" {
		role = "user"
	}
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		source = types.VoiceSourceTranscription
	}
	speakerID := strings.TrimSpace(opts.SpeakerID)
	if speakerID == "" {
		speakerID = extractDominantSpeakerID(resp)
	}

	rec := &types.VoiceTranscriptRecord{
		Role:    role,
		Content: text,
		Metadata: types.VoiceMetadata{
			SessionID:   sessionID,
			TurnID:      strings.TrimSpace(opts.TurnID),
			SpeakerRole: role,
			SpeakerID:   speakerID,
			Source:      source,
			Provider:    strings.TrimSpace(opts.Provider),
			Model:       strings.TrimSpace(string(resp.Model)),
			Language:    strings.TrimSpace(resp.Language),
			EventType:   "transcription",
			Timestamp:   opts.Timestamp,
			Duration:    resp.Duration,
			IsFinal:     true,
		},
	}

	return v.AppendTranscript(ctx, namespace, threadID, rec, &VoiceRecordOpts{
		MaxContentLength: opts.MaxContentLength,
		Metadata:         opts.Metadata,
		SkipEmbedding:    opts.SkipEmbedding,
	})
}

// History retrieves namespace-scoped committed voice transcript and assistant
// text history in chronological order with lossless pagination and summary
// integration.
func (v *VoiceClient) History(ctx context.Context, namespace, threadID string, opts *VoiceHistoryOpts) (*VoiceHistoryResult, error) {
	var internalOpts *conversation.VoiceHistoryOpts
	if opts != nil {
		internalOpts = &conversation.VoiceHistoryOpts{
			SessionID:         opts.SessionID,
			Roles:             opts.Roles,
			IncludeNonVoice:   opts.IncludeNonVoice,
			LastN:             opts.LastN,
			Cursor:            opts.Cursor,
			IncludeSummary:    opts.IncludeSummary,
			SkipAutoSummarize: opts.SkipAutoSummarize,
		}
	}
	res, err := v.engine.VoiceHistory(ctx, namespace, threadID, internalOpts)
	if err != nil {
		return nil, mapVoiceErr(err)
	}
	return &VoiceHistoryResult{
		Messages:   res.Messages,
		Summary:    res.Summary,
		NextCursor: res.NextCursor,
		ThreadID:   res.ThreadID,
		SessionID:  res.SessionID,
	}, nil
}

// IrisMessages retrieves namespace-scoped committed voice conversation history
// (plus optional rolling thread summary and system prompt) formatted directly
// as []core.Message for Iris chat/voice reasoning context injection.
func (v *VoiceClient) IrisMessages(ctx context.Context, namespace, threadID string, opts *IrisContextOpts) ([]core.Message, *VoiceHistoryResult, error) {
	if opts == nil {
		opts = &IrisContextOpts{IncludeSummary: true}
	}

	hist, err := v.History(ctx, namespace, threadID, &VoiceHistoryOpts{
		SessionID:         opts.SessionID,
		Roles:             opts.Roles,
		IncludeNonVoice:   opts.IncludeNonVoice,
		LastN:             opts.LastN,
		Cursor:            opts.Cursor,
		IncludeSummary:    opts.IncludeSummary,
		SkipAutoSummarize: opts.SkipAutoSummarize,
	})
	if err != nil {
		return nil, nil, err
	}

	msgs := make([]core.Message, 0, len(hist.Messages)+1)

	// Build optional leading system message from SystemPrompt and/or rolling Summary.
	sysPrompt := strings.TrimSpace(opts.SystemPrompt)
	summary := strings.TrimSpace(hist.Summary)
	if sysPrompt != "" || summary != "" {
		prefix := opts.SummaryPrefix
		if prefix == "" {
			prefix = DefaultSummaryPrefix
		}
		var sysContent string
		switch {
		case sysPrompt != "" && summary != "":
			sysContent = sysPrompt + "\n\n" + prefix + summary
		case sysPrompt != "":
			sysContent = sysPrompt
		default:
			sysContent = prefix + summary
		}
		msgs = append(msgs, core.Message{
			Role:    core.RoleSystem,
			Content: sysContent,
		})
	}

	convMsgs := make([]core.Message, 0, len(hist.Messages))
	for _, m := range hist.Messages {
		if m == nil {
			continue
		}
		convMsgs = append(convMsgs, core.Message{
			Role:    core.Role(m.Role),
			Content: m.Content,
		})
	}

	if opts.MaxMessages > 0 && len(convMsgs) > opts.MaxMessages {
		excess := len(convMsgs) - opts.MaxMessages
		for excess < len(convMsgs) && convMsgs[excess].Role == core.RoleTool {
			excess++
		}
		if excess < len(convMsgs) {
			convMsgs = convMsgs[excess:]
		} else {
			convMsgs = nil
		}
	}

	msgs = append(msgs, convMsgs...)
	return msgs, hist, nil
}

// DeleteSession deletes all persisted voice transcript messages belonging to
// sessionID strictly within namespace.
func (v *VoiceClient) DeleteSession(ctx context.Context, namespace, sessionID string) (int64, error) {
	deleted, err := v.engine.DeleteVoiceSession(ctx, namespace, sessionID)
	if err != nil {
		return 0, mapVoiceErr(err)
	}
	return deleted, nil
}

// DeleteThread deletes an entire voice conversation thread within namespace.
func (v *VoiceClient) DeleteThread(ctx context.Context, namespace, threadID string) error {
	return mapVoiceErr(v.engine.DeleteVoiceThread(ctx, namespace, threadID))
}

// PurgeExpired removes persisted voice transcripts older than olderThan.
func (v *VoiceClient) PurgeExpired(ctx context.Context, olderThan time.Duration) (int64, error) {
	deleted, err := v.engine.PurgeExpiredVoiceTranscripts(ctx, olderThan)
	if err != nil {
		return 0, mapVoiceErr(err)
	}
	return deleted, nil
}

// VoiceSessionRecorder consumes Iris core.VoiceEvent values, maintains a
// consumer-side core.VoiceTurnTracker for stale-audio suppression on barge-in,
// and persists ONLY committed transcripts and committed assistant text to Cortex.
type VoiceSessionRecorder struct {
	voice     *VoiceClient
	namespace string
	threadID  string
	opts      VoiceRecordOpts
	tracker   *core.VoiceTurnTracker
}

// NewSessionRecorder creates a VoiceSessionRecorder bound to (namespace, threadID).
func (v *VoiceClient) NewSessionRecorder(namespace, threadID string, opts *VoiceRecordOpts) *VoiceSessionRecorder {
	var copied VoiceRecordOpts
	if opts != nil {
		copied = *opts
	}
	return &VoiceSessionRecorder{
		voice:     v,
		namespace: namespace,
		threadID:  threadID,
		opts:      copied,
		tracker:   core.NewVoiceTurnTracker("cortex"),
	}
}

// Observe updates turn-tracking state for evt and persists committed user
// transcripts or committed assistant text into Cortex. Non-committed events and
// audio output events are never persisted and return (nil, nil).
func (r *VoiceSessionRecorder) Observe(ctx context.Context, evt core.VoiceEvent) (*types.Message, error) {
	r.tracker.ObserveEvent(evt)
	return r.voice.RecordVoiceEvent(ctx, r.namespace, r.threadID, evt, &r.opts)
}

// ShouldPlayAudio reports whether evt is an audio chunk belonging to the active,
// non-interrupted turn (stale chunks from interrupted turns return false).
func (r *VoiceSessionRecorder) ShouldPlayAudio(evt core.VoiceEvent) bool {
	return r.tracker.ShouldPlayAudio(evt)
}

// Drain reads events from sess until sess closes or ctx is cancelled, persisting
// committed transcripts/responses into Cortex and optionally forwarding every
// event (after stale-audio filtering is available via ShouldPlayAudio) to onEvent.
//
// Returns the first persistence error, session terminal error, or ctx.Err().
func (r *VoiceSessionRecorder) Drain(ctx context.Context, sess core.VoiceSession, onEvent func(core.VoiceEvent)) error {
	if sess == nil {
		return fmt.Errorf("%w: voice session cannot be nil", ErrInvalidInput)
	}
	defer r.tracker.Close()

	eventsCh := sess.Events()
	errCh := sess.Err()

	var firstErr error
	for eventsCh != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt, ok := <-eventsCh:
			if !ok {
				eventsCh = nil
				break
			}
			if _, err := r.Observe(ctx, evt); err != nil && firstErr == nil {
				firstErr = err
			}
			if onEvent != nil {
				onEvent(evt)
			}
		}
	}

	if errCh != nil {
		select {
		case err, ok := <-errCh:
			if ok && err != nil && firstErr == nil {
				firstErr = err
			}
		case <-ctx.Done():
			if firstErr == nil {
				firstErr = ctx.Err()
			}
		}
	}

	return firstErr
}

// IrisMemoryOpts configures NewIrisMemory.
type IrisMemoryOpts struct {
	Provider       string
	Model          string
	IncludeSummary bool
	HistoryLimit   int
}

// IrisMemory adapts Cortex VoiceClient to Iris's core.Memory interface for
// namespace-scoped, persistent voice conversation history.
type IrisMemory struct {
	voice          *VoiceClient
	namespace      string
	threadID       string
	sessionID      string
	provider       string
	model          string
	includeSummary bool
	historyLimit   int

	mu      sync.Mutex
	seq     int64
	lastErr error
}

// Compile-time check that IrisMemory implements Iris core.Memory.
var _ core.Memory = (*IrisMemory)(nil)

// NewIrisMemory creates an Iris core.Memory adapter backed by Cortex voice memory.
func (v *VoiceClient) NewIrisMemory(namespace, threadID, sessionID string, opts *IrisMemoryOpts) *IrisMemory {
	if opts == nil {
		opts = &IrisMemoryOpts{IncludeSummary: true}
	}
	limit := opts.HistoryLimit
	if limit <= 0 {
		limit = 200
	}
	if strings.TrimSpace(threadID) == "" {
		threadID = sessionID
	}
	return &IrisMemory{
		voice:          v,
		namespace:      namespace,
		threadID:       threadID,
		sessionID:      sessionID,
		provider:       opts.Provider,
		model:          opts.Model,
		includeSummary: opts.IncludeSummary,
		historyLimit:   limit,
	}
}

// LastErr returns the most recent storage or policy error encountered by the
// core.Memory methods (which do not return errors directly).
func (m *IrisMemory) LastErr() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

func (m *IrisMemory) setErr(err error) {
	m.mu.Lock()
	m.lastErr = err
	m.mu.Unlock()
}

// AddMessage persists msg as a committed voice record in Cortex.
func (m *IrisMemory) AddMessage(ctx context.Context, msg core.Message) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(msg.Content) == "" {
		return
	}

	m.mu.Lock()
	m.seq++
	seq := m.seq
	m.mu.Unlock()

	source := types.VoiceSourceVoiceSession
	switch msg.Role {
	case core.RoleUser:
		source = types.VoiceSourceSTT
	case core.RoleAssistant:
		source = types.VoiceSourceAgent
	}

	_, err := m.voice.AppendTranscript(ctx, m.namespace, m.threadID, &types.VoiceTranscriptRecord{
		Role:    string(msg.Role),
		Content: msg.Content,
		Metadata: types.VoiceMetadata{
			SessionID:   m.sessionID,
			SpeakerRole: string(msg.Role),
			Source:      source,
			Provider:    m.provider,
			Model:       m.model,
			Sequence:    seq,
			Timestamp:   time.Now().UTC(),
			IsFinal:     true,
		},
	}, nil)
	m.setErr(err)
}

// AddMessages persists each message in msgs in order.
func (m *IrisMemory) AddMessages(ctx context.Context, msgs []core.Message) {
	for _, msg := range msgs {
		if ctx != nil && ctx.Err() != nil {
			m.setErr(ctx.Err())
			return
		}
		m.AddMessage(ctx, msg)
	}
}

// GetHistory retrieves namespace-scoped committed voice messages formatted for Iris.
func (m *IrisMemory) GetHistory(ctx context.Context) []core.Message {
	if ctx == nil {
		ctx = context.Background()
	}
	msgs, _, err := m.voice.IrisMessages(ctx, m.namespace, m.threadID, &IrisContextOpts{
		SessionID:      m.sessionID,
		Roles:          []string{"system", "user", "assistant", "tool"},
		LastN:          m.historyLimit,
		IncludeSummary: m.includeSummary,
	})
	m.setErr(err)
	if err != nil {
		return nil
	}
	return msgs
}

// GetLastN retrieves the last n committed voice messages formatted for Iris.
func (m *IrisMemory) GetLastN(ctx context.Context, n int) []core.Message {
	if n <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	msgs, _, err := m.voice.IrisMessages(ctx, m.namespace, m.threadID, &IrisContextOpts{
		SessionID:      m.sessionID,
		Roles:          []string{"system", "user", "assistant", "tool"},
		LastN:          n,
		IncludeSummary: false,
	})
	m.setErr(err)
	if err != nil {
		return nil
	}
	return msgs
}

// Clear removes all voice messages for this session within the namespace.
func (m *IrisMemory) Clear(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := m.voice.DeleteSession(ctx, m.namespace, m.sessionID)
	m.setErr(err)
}

// Len returns the number of retained committed voice messages in this session.
func (m *IrisMemory) Len(ctx context.Context) int {
	return len(m.GetHistory(ctx))
}

// SetMessages replaces the session's persisted voice messages with msgs.
func (m *IrisMemory) SetMessages(ctx context.Context, msgs []core.Message) {
	if ctx == nil {
		ctx = context.Background()
	}
	m.Clear(ctx)
	if m.LastErr() != nil {
		return
	}
	m.AddMessages(ctx, slices.Clone(msgs))
}

func extractDominantSpeakerID(resp *core.TranscriptionResponse) string {
	if resp == nil {
		return ""
	}
	var speaker string
	for _, seg := range resp.Segments {
		id := strings.TrimSpace(seg.SpeakerID)
		if id == "" {
			continue
		}
		if speaker == "" {
			speaker = id
		} else if speaker != id {
			return ""
		}
	}
	if speaker != "" {
		return speaker
	}
	for _, w := range resp.Words {
		id := strings.TrimSpace(w.SpeakerID)
		if id == "" {
			continue
		}
		if speaker == "" {
			speaker = id
		} else if speaker != id {
			return ""
		}
	}
	return speaker
}

func mapVoiceErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, conversation.ErrVoiceStorageDisabled):
		return fmt.Errorf("%w: %v", ErrVoiceStorageDisabled, err)
	case errors.Is(err, types.ErrUncommittedTranscript):
		return fmt.Errorf("%w: %v", ErrUncommittedTranscript, err)
	case errors.Is(err, types.ErrRawAudioDisallowed):
		return fmt.Errorf("%w: %v", ErrRawAudioDisallowed, err)
	case errors.Is(err, conversation.ErrNamespaceNotAllowed):
		return fmt.Errorf("%w: %v", ErrNamespaceNotAllowed, err)
	case errors.Is(err, conversation.ErrInvalidNamespace),
		errors.Is(err, conversation.ErrInvalidSessionID),
		errors.Is(err, conversation.ErrInvalidRetentionDuration),
		errors.Is(err, types.ErrInvalidVoiceMetadata):
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	default:
		return mapConversationErr(err)
	}
}
