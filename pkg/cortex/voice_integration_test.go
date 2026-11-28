package cortex_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/petal-labs/iris/v2/core"
	"github.com/petal-labs/iris/v2/voice"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/petal-labs/cortex/v2/internal/observability"
	"github.com/petal-labs/cortex/v2/pkg/cortex"
	"github.com/petal-labs/cortex/v2/pkg/types"
)

// --- Deterministic Iris v2 voice test doubles ---

type mockVoiceSTT struct {
	mu   sync.Mutex
	sess *mockVoiceSTTSession
}

func (m *mockVoiceSTT) StartRealtimeTranscription(_ context.Context, _ core.RealtimeTranscriptionConfig) (core.RealtimeTranscriptionSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sess == nil {
		m.sess = &mockVoiceSTTSession{
			events: make(chan core.RealtimeTranscriptEvent, 64),
			errCh:  make(chan error, 1),
		}
	}
	return m.sess, nil
}

type mockVoiceSTTSession struct {
	events    chan core.RealtimeTranscriptEvent
	errCh     chan error
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
}

func (s *mockVoiceSTTSession) SendAudio(_ context.Context, _ core.AudioChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return core.ErrRealtimeSessionClosed
	}
	return nil
}

func (s *mockVoiceSTTSession) Commit(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return core.ErrRealtimeSessionClosed
	}
	return nil
}

func (s *mockVoiceSTTSession) Events() <-chan core.RealtimeTranscriptEvent { return s.events }
func (s *mockVoiceSTTSession) Err() <-chan error                           { return s.errCh }

func (s *mockVoiceSTTSession) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.events)
		close(s.errCh)
	})
	return nil
}

type mockVoiceTTS struct {
	audioSentinel string
	delayPerChunk time.Duration
}

func (m *mockVoiceTTS) StartRealtimeSynthesis(_ context.Context, cfg core.RealtimeSynthesisConfig) (core.RealtimeSynthesisSession, error) {
	format := cfg.Format
	if format.IsZero() {
		format = core.AudioFormatPCM16000
	}
	return &mockVoiceTTSSession{
		format:        format,
		audioSentinel: m.audioSentinel,
		delayPerChunk: m.delayPerChunk,
		audio:         make(chan core.AudioChunk, 64),
		errCh:         make(chan error, 1),
	}, nil
}

type mockVoiceTTSSession struct {
	format        core.AudioFormat
	audioSentinel string
	delayPerChunk time.Duration
	audio         chan core.AudioChunk
	errCh         chan error
	mu            sync.Mutex
	seq           int64
	closed        bool
	closeOnce     sync.Once
}

func (s *mockVoiceTTSSession) SendText(ctx context.Context, text string) error {
	if s.delayPerChunk > 0 {
		select {
		case <-time.After(s.delayPerChunk):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return core.ErrRealtimeSessionClosed
	}
	payload := s.audioSentinel
	if payload == "" {
		payload = "pcm:" + text
	}
	s.audio <- core.AudioChunk{
		Data:     []byte(payload),
		Format:   s.format,
		Sequence: s.seq,
	}
	s.seq++
	return nil
}

func (s *mockVoiceTTSSession) Flush(_ context.Context) error { return nil }

func (s *mockVoiceTTSSession) Finish(_ context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.audio <- core.AudioChunk{Format: s.format, Sequence: s.seq, IsFinal: true}
		s.mu.Unlock()
		close(s.audio)
		close(s.errCh)
	})
	return nil
}

func (s *mockVoiceTTSSession) Audio() <-chan core.AudioChunk { return s.audio }
func (s *mockVoiceTTSSession) Err() <-chan error             { return s.errCh }

func (s *mockVoiceTTSSession) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.audio)
		close(s.errCh)
	})
	return nil
}

type mockVoiceLLM struct {
	mu       sync.Mutex
	requests []*core.ChatRequest
	replies  []string
	idx      int
	delay    time.Duration
}

func (p *mockVoiceLLM) ID() string { return "mock-llm" }
func (p *mockVoiceLLM) Models() []core.ModelInfo {
	return []core.ModelInfo{{
		ID:           "mock-model",
		Capabilities: []core.Feature{core.FeatureChat, core.FeatureChatStreaming},
	}}
}
func (p *mockVoiceLLM) Supports(f core.Feature) bool {
	return f == core.FeatureChat || f == core.FeatureChatStreaming
}

func (p *mockVoiceLLM) nextReply(req *core.ChatRequest) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := *req
	cp.Messages = append([]core.Message(nil), req.Messages...)
	p.requests = append(p.requests, &cp)
	if p.idx < len(p.replies) {
		r := p.replies[p.idx]
		p.idx++
		return r
	}
	return "Acknowledged."
}

func (p *mockVoiceLLM) recordedRequests() []*core.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*core.ChatRequest, len(p.requests))
	copy(out, p.requests)
	return out
}

func (p *mockVoiceLLM) Chat(ctx context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &core.ChatResponse{
		ID:     "chat-resp",
		Model:  req.Model,
		Output: p.nextReply(req),
	}, nil
}

func (p *mockVoiceLLM) StreamChat(ctx context.Context, req *core.ChatRequest) (*core.ChatStream, error) {
	reply := p.nextReply(req)
	ch := make(chan core.ChatChunk, 8)
	errCh := make(chan error, 1)
	finalCh := make(chan *core.ChatResponse, 1)

	go func() {
		defer close(ch)
		defer close(errCh)
		defer close(finalCh)

		words := strings.Fields(reply)
		for i, w := range words {
			if p.delay > 0 {
				select {
				case <-time.After(p.delay):
				case <-ctx.Done():
					errCh <- ctx.Err()
					return
				}
			}
			delta := w
			if i > 0 {
				delta = " " + w
			}
			select {
			case ch <- core.ChatChunk{Delta: delta}:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}
		finalCh <- &core.ChatResponse{
			ID:     "stream-resp",
			Model:  req.Model,
			Output: reply,
		}
	}()

	return &core.ChatStream{Ch: ch, Err: errCh, Final: finalCh}, nil
}

type voiceStubSummarizer struct {
	summary string
}

func (s *voiceStubSummarizer) SummarizeMessages(_ context.Context, _ []*types.Message) (string, error) {
	return s.summary, nil
}

// --- End-to-end mocked integration tests (Issues #76-#80) ---

func TestVoicePipelineEndToEnd_MockSpeechToCortexToIrisContext(t *testing.T) {
	const (
		namespace        = "voice-e2e"
		threadID         = "voice-thread-e2e"
		sessionID        = "voice-sess-e2e"
		secretTranscript = "TOP_SECRET_TRANSCRIPT_PHRASE"
		audioSentinel    = "RAW_PCM_AUDIO_SENTINEL_BYTES"
	)

	// Capture all structured logs at DebugLevel to verify zero transcript/audio logging.
	zapCore, logObserver := observer.New(zap.DebugLevel)
	prevLogger := observability.DefaultLogger
	observability.DefaultLogger = &observability.Logger{Logger: zap.New(zapCore)}
	t.Cleanup(func() { observability.DefaultLogger = prevLogger })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := cortex.Open(ctx,
		cortex.WithSQLite(t.TempDir()),
		cortex.WithVoiceTranscriptStorage(true),
		cortex.WithAllowedNamespaces(namespace),
		cortex.WithSummarizer(&voiceStubSummarizer{summary: "User Alice shared a verification code and greeted the assistant."}),
	)
	if err != nil {
		t.Fatalf("cortex.Open: %v", err)
	}
	defer func() { _ = client.Close() }()

	stt := &mockVoiceSTT{}
	llm := &mockVoiceLLM{
		replies: []string{
			"Hello Alice, I have noted your request.",
			"Your name is Alice.",
		},
	}
	tts := &mockVoiceTTS{audioSentinel: audioSentinel}

	pipeline, err := voice.NewPipeline(voice.PipelineConfig{
		STT:         stt,
		LLM:         llm,
		LLMModel:    "mock-model",
		RealtimeTTS: tts,
		VoiceID:     "voice-test",
	})
	if err != nil {
		t.Fatalf("voice.NewPipeline: %v", err)
	}

	sess, err := pipeline.StartVoiceSession(ctx, core.VoiceSessionConfig{
		SessionID: sessionID,
		Language:  "en",
	})
	if err != nil {
		t.Fatalf("StartVoiceSession: %v", err)
	}

	recorder := client.Voice().NewSessionRecorder(namespace, threadID, &cortex.VoiceRecordOpts{
		Provider: "elevenlabs",
		Model:    "scribe_v2",
	})

	// Consume session events in background; signal after each turn completes.
	turn1Done := make(chan struct{})
	turnsDone := make(chan struct{})
	completedTurns := 0
	var audioChunksSeen int
	var lastCommittedEvt core.VoiceEvent

	drainDone := make(chan error, 1)
	go func() {
		drainDone <- recorder.Drain(ctx, sess, func(evt core.VoiceEvent) {
			if evt.Type == core.VoiceEventAudioOutput && recorder.ShouldPlayAudio(evt) {
				audioChunksSeen++
			}
			if evt.Type == core.VoiceEventUserTranscriptCommitted {
				lastCommittedEvt = evt
			}
			if evt.Type == core.VoiceEventTurnCompleted {
				completedTurns++
				switch completedTurns {
				case 1:
					close(turn1Done)
				case 2:
					close(turnsDone)
				}
			}
		})
	}()

	// Emit Turn 1: multiple partials followed by a single committed utterance.
	stt.sess.events <- core.RealtimeTranscriptEvent{
		Type:      core.RealtimeTranscriptEventPartial,
		SessionID: sessionID,
		IsFinal:   false,
		Transcript: &core.TranscriptionResponse{
			Text:    "Hello my name",
			IsFinal: false,
		},
	}
	stt.sess.events <- core.RealtimeTranscriptEvent{
		Type:      core.RealtimeTranscriptEventPartial,
		SessionID: sessionID,
		IsFinal:   false,
		Transcript: &core.TranscriptionResponse{
			Text:    "Hello my name is Alice",
			IsFinal: false,
		},
	}
	stt.sess.events <- core.RealtimeTranscriptEvent{
		Type:      core.RealtimeTranscriptEventCommitted,
		SessionID: sessionID,
		IsFinal:   true,
		Transcript: &core.TranscriptionResponse{
			Model:    "scribe_v2",
			Text:     "Hello my name is Alice and my code is " + secretTranscript,
			Language: "en",
			Duration: 1250 * time.Millisecond,
			IsFinal:  true,
			Segments: []core.TranscriptionSegment{
				{Text: "Hello my name is Alice", SpeakerID: "speaker_0", Start: 0, End: 1250 * time.Millisecond},
			},
		},
	}

	// Wait deterministically for Turn 1 to emit VoiceEventTurnCompleted before Turn 2.
	select {
	case <-turn1Done:
	case <-ctx.Done():
		t.Fatal("timed out waiting for turn 1 to complete")
	}

	// Emit Turn 2: partial then committed.
	stt.sess.events <- core.RealtimeTranscriptEvent{
		Type:       core.RealtimeTranscriptEventPartial,
		SessionID:  sessionID,
		IsFinal:    false,
		Transcript: &core.TranscriptionResponse{Text: "What is my", IsFinal: false},
	}
	stt.sess.events <- core.RealtimeTranscriptEvent{
		Type:      core.RealtimeTranscriptEventCommitted,
		SessionID: sessionID,
		IsFinal:   true,
		Transcript: &core.TranscriptionResponse{
			Model:    "scribe_v2",
			Text:     "What is my name?",
			Language: "en",
			IsFinal:  true,
		},
	}

	select {
	case <-turnsDone:
	case <-ctx.Done():
		t.Fatal("timed out waiting for 2 voice turns to complete")
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("sess.Close: %v", err)
	}
	if err := <-drainDone; err != nil {
		t.Fatalf("recorder.Drain: %v", err)
	}
	if audioChunksSeen == 0 {
		t.Error("expected audio output chunks to be observed during playback")
	}

	// Verify ONLY the 4 committed messages were persisted (zero partials, zero deltas, zero audio).
	hist, err := client.Voice().History(ctx, namespace, threadID, &cortex.VoiceHistoryOpts{
		SessionID: sessionID,
		LastN:     20,
	})
	if err != nil {
		t.Fatalf("Voice.History: %v", err)
	}
	if len(hist.Messages) != 4 {
		t.Fatalf("expected 4 committed messages (2 user + 2 assistant), got %d", len(hist.Messages))
	}

	expectedRoles := []string{"user", "assistant", "user", "assistant"}
	for i, m := range hist.Messages {
		if m.Role != expectedRoles[i] {
			t.Errorf("msg[%d] role = %q, want %q", i, m.Role, expectedRoles[i])
		}
		vm, ok := types.ParseVoiceMetadata(m.Metadata)
		if !ok {
			t.Fatalf("msg[%d] missing parsed VoiceMetadata", i)
		}
		if vm.SessionID != sessionID || vm.TurnID == "" || !vm.IsFinal {
			t.Errorf("msg[%d] invalid correlation metadata: %+v", i, vm)
		}
		if strings.Contains(m.Content, audioSentinel) {
			t.Errorf("msg[%d] persisted raw audio sentinel", i)
		}
	}
	user0Meta, _ := types.ParseVoiceMetadata(hist.Messages[0].Metadata)
	if user0Meta.SpeakerID != "speaker_0" || user0Meta.Language != "en" || user0Meta.Source != types.VoiceSourceSTT {
		t.Errorf("unexpected user0 voice metadata: %+v", user0Meta)
	}

	// Replaying the last committed event must be idempotent (no duplicate).
	if _, err := client.Voice().RecordVoiceEvent(ctx, namespace, threadID, lastCommittedEvt, &cortex.VoiceRecordOpts{Provider: "elevenlabs"}); err != nil {
		t.Fatalf("idempotent RecordVoiceEvent: %v", err)
	}
	afterReplay, err := client.Voice().History(ctx, namespace, threadID, &cortex.VoiceHistoryOpts{SessionID: sessionID})
	if err != nil {
		t.Fatalf("History after replay: %v", err)
	}
	if len(afterReplay.Messages) != 4 {
		t.Fatalf("expected 4 messages after replay (no duplicate), got %d", len(afterReplay.Messages))
	}

	// Summarize older turn, then inject Cortex voice history + summary into a new Iris reasoning request!
	if _, err := client.Conversation().Summarize(ctx, namespace, threadID, &cortex.SummarizeOpts{KeepRecent: 2}); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	irisMsgs, histRes, err := client.Voice().IrisMessages(ctx, namespace, threadID, &cortex.IrisContextOpts{
		SessionID:      sessionID,
		IncludeSummary: true,
		SystemPrompt:   "You are a helpful voice assistant.",
	})
	if err != nil {
		t.Fatalf("IrisMessages: %v", err)
	}
	if histRes.Summary == "" {
		t.Fatal("expected thread summary to be returned")
	}
	if len(irisMsgs) != 3 {
		t.Fatalf("expected 3 Iris messages (1 system+summary + 2 recent turns), got %d: %+v", len(irisMsgs), irisMsgs)
	}
	if irisMsgs[0].Role != core.RoleSystem ||
		!strings.Contains(irisMsgs[0].Content, "You are a helpful voice assistant.") ||
		!strings.Contains(irisMsgs[0].Content, histRes.Summary) {
		t.Errorf("unexpected leading system+summary message: %+v", irisMsgs[0])
	}
	if irisMsgs[1].Role != core.RoleUser || irisMsgs[1].Content != "What is my name?" {
		t.Errorf("unexpected turn 2 user message: %+v", irisMsgs[1])
	}
	if irisMsgs[2].Role != core.RoleAssistant || irisMsgs[2].Content != "Your name is Alice." {
		t.Errorf("unexpected turn 2 assistant message: %+v", irisMsgs[2])
	}

	// Inject irisMsgs into a follow-up Iris chat provider call and verify the LLM sees them.
	followUpLLM := &mockVoiceLLM{replies: []string{"Still Alice!"}}
	reqMsgs := append(irisMsgs, core.Message{Role: core.RoleUser, Content: "Confirm my name again."})
	resp, err := followUpLLM.Chat(ctx, &core.ChatRequest{
		Model:    "mock-model",
		Messages: reqMsgs,
	})
	if err != nil {
		t.Fatalf("followUpLLM.Chat: %v", err)
	}
	if resp.Output != "Still Alice!" {
		t.Errorf("unexpected follow-up response: %q", resp.Output)
	}
	recorded := followUpLLM.recordedRequests()
	if len(recorded) != 1 || len(recorded[0].Messages) != 4 {
		t.Fatalf("expected follow-up LLM to receive 4 context messages, got %+v", recorded)
	}

	// Privacy assertion: ZERO log lines or fields may contain transcript text or raw audio bytes!
	for _, entry := range logObserver.All() {
		if strings.Contains(entry.Message, secretTranscript) || strings.Contains(entry.Message, audioSentinel) {
			t.Fatalf("log message leaked sensitive content: %s", entry.Message)
		}
		for _, field := range entry.Context {
			if strings.Contains(field.String, secretTranscript) || strings.Contains(field.String, audioSentinel) {
				t.Fatalf("log field %q leaked sensitive content", field.Key)
			}
		}
	}
}

func TestVoiceIntegration_BargeInInterruptionAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := cortex.Open(ctx,
		cortex.WithSQLite(t.TempDir()),
		cortex.WithVoiceTranscriptStorage(true),
	)
	if err != nil {
		t.Fatalf("cortex.Open: %v", err)
	}
	defer func() { _ = client.Close() }()

	recorder := client.Voice().NewSessionRecorder("ns-barge", "thread-barge", &cortex.VoiceRecordOpts{
		Provider: "elevenlabs",
	})

	// Turn 1 starts, emits audio, then gets interrupted by barge-in.
	if _, err := recorder.Observe(ctx, core.VoiceEvent{
		Type:      core.VoiceEventTurnStarted,
		SessionID: "sess-barge",
		TurnID:    "turn-1",
	}); err != nil {
		t.Fatalf("Observe turn_started: %v", err)
	}

	turn1Audio := core.VoiceEvent{
		Type:      core.VoiceEventAudioOutput,
		SessionID: "sess-barge",
		TurnID:    "turn-1",
		Audio:     &core.AudioChunk{Data: []byte("turn1-pcm"), Format: core.AudioFormatPCM16000},
	}
	if !recorder.ShouldPlayAudio(turn1Audio) {
		t.Fatal("expected turn-1 audio playable while active")
	}

	if _, err := recorder.Observe(ctx, core.VoiceEvent{
		Type:      core.VoiceEventTurnInterrupted,
		SessionID: "sess-barge",
		TurnID:    "turn-1",
		Interruption: &core.VoiceInterruption{
			CancelledTurnID: "turn-1",
			Reason:          core.VoiceInterruptionBargeIn,
		},
	}); err != nil {
		t.Fatalf("Observe turn_interrupted: %v", err)
	}
	if recorder.ShouldPlayAudio(turn1Audio) {
		t.Fatal("expected stale turn-1 audio suppressed after barge-in interruption")
	}

	// Context cancellation on RecordVoiceEvent must fail fast without persisting.
	canceledCtx, cancelNow := context.WithCancel(ctx)
	cancelNow()
	_, err = client.Voice().RecordVoiceEvent(canceledCtx, "ns-barge", "thread-barge", core.VoiceEvent{
		Type:      core.VoiceEventUserTranscriptCommitted,
		SessionID: "sess-barge",
		TurnID:    "turn-2",
		IsFinal:   true,
		Transcript: &core.TranscriptionResponse{
			Text:    "This should not persist",
			IsFinal: true,
		},
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestVoiceIntegration_ScribeTranscriptionAndIrisMemoryAdapter(t *testing.T) {
	ctx := context.Background()
	client, err := cortex.Open(ctx,
		cortex.WithSQLite(t.TempDir()),
		cortex.WithVoiceTranscriptStorage(true),
	)
	if err != nil {
		t.Fatalf("cortex.Open: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Uncommitted TranscriptionResponse must be rejected.
	_, err = client.Voice().RecordTranscription(ctx, "ns-scribe", "thread-scribe", &core.TranscriptionResponse{
		Text:    "interim",
		IsFinal: false,
	}, &cortex.VoiceTranscriptionOpts{SessionID: "sess-scribe"})
	if !errors.Is(err, cortex.ErrUncommittedTranscript) {
		t.Fatalf("expected ErrUncommittedTranscript, got %v", err)
	}

	// Committed Scribe transcription persists with language, duration, model, speaker_id.
	msg, err := client.Voice().RecordTranscription(ctx, "ns-scribe", "thread-scribe", &core.TranscriptionResponse{
		ID:       "tr-1",
		Model:    "scribe_v2",
		Text:     "Schedule a design review for Friday.",
		Language: "en",
		Duration: 2400 * time.Millisecond,
		IsFinal:  true,
		Words: []core.TranscriptionWord{
			{Text: "Schedule", Start: 0, End: 400 * time.Millisecond, SpeakerID: "speaker_2"},
		},
	}, &cortex.VoiceTranscriptionOpts{
		SessionID: "sess-scribe",
		TurnID:    "turn-1",
		Provider:  "elevenlabs",
	})
	if err != nil {
		t.Fatalf("RecordTranscription: %v", err)
	}
	vm, ok := types.ParseVoiceMetadata(msg.Metadata)
	if !ok || vm.Source != types.VoiceSourceTranscription || vm.SpeakerID != "speaker_2" || vm.Model != "scribe_v2" {
		t.Fatalf("unexpected transcription metadata: %+v", vm)
	}

	// Exercise Iris core.Memory adapter directly with core.NewConversation.
	mem := client.Voice().NewIrisMemory("ns-iris-mem", "thread-iris-mem", "sess-iris-mem", &cortex.IrisMemoryOpts{
		Provider: "elevenlabs",
		Model:    "mock-model",
	})
	llm := &mockVoiceLLM{replies: []string{"Hello from Iris!", "Yes, I remember."}}
	irisClient := core.NewClient(llm)
	conv := core.NewConversation(ctx, irisClient, "mock-model",
		core.WithMemoryStore(mem),
		core.WithSystemMessage("Be concise."),
	)
	if mem.LastErr() != nil {
		t.Fatalf("NewConversation init memory err: %v", mem.LastErr())
	}

	r1, err := conv.Send(ctx, "Remember that I prefer Go.")
	if err != nil {
		t.Fatalf("conv.Send 1: %v", err)
	}
	if r1.Output != "Hello from Iris!" {
		t.Errorf("unexpected r1: %q", r1.Output)
	}
	r2, err := conv.Send(ctx, "Do you remember my preference?")
	if err != nil {
		t.Fatalf("conv.Send 2: %v", err)
	}
	if r2.Output != "Yes, I remember." {
		t.Errorf("unexpected r2: %q", r2.Output)
	}
	reqs := llm.recordedRequests()
	if len(reqs) != 2 || len(reqs[1].Messages) < 4 {
		t.Fatalf("expected second Iris turn to receive Cortex-backed history, got %+v", reqs)
	}

	mem.Clear(ctx)
	if mem.LastErr() != nil {
		t.Fatalf("mem.Clear: %v", mem.LastErr())
	}
	if mem.Len(ctx) != 0 {
		t.Errorf("expected 0 messages after Clear, got %d", mem.Len(ctx))
	}
}

func TestVoiceIntegration_OptInNamespaceIsolationAndRetention(t *testing.T) {
	ctx := context.Background()

	// 1. Default Open disables voice transcript persistence.
	defaultClient, err := cortex.Open(ctx, cortex.WithSQLite(t.TempDir()))
	if err != nil {
		t.Fatalf("cortex.Open default: %v", err)
	}
	defer func() { _ = defaultClient.Close() }()

	if defaultClient.Voice().StorageEnabled() {
		t.Fatal("expected voice storage disabled by default")
	}
	_, err = defaultClient.Voice().RecordVoiceEvent(ctx, "ns-a", "t-a", core.VoiceEvent{
		Type:      core.VoiceEventUserTranscriptCommitted,
		SessionID: "sess-default",
		TurnID:    "turn-1",
		IsFinal:   true,
		Transcript: &core.TranscriptionResponse{
			Text:    "should fail",
			IsFinal: true,
		},
	}, nil)
	if !errors.Is(err, cortex.ErrVoiceStorageDisabled) {
		t.Fatalf("expected ErrVoiceStorageDisabled, got %v", err)
	}

	// 2. Opt-in + namespace allowlist + retention purge.
	client, err := cortex.Open(ctx,
		cortex.WithSQLite(t.TempDir()),
		cortex.WithVoiceTranscriptStorage(true),
		cortex.WithVoiceTranscriptRetention(7),
		cortex.WithAllowedNamespaces("ns-alpha", "ns-beta"),
	)
	if err != nil {
		t.Fatalf("cortex.Open opted-in: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Blocked namespace rejected.
	_, err = client.Voice().AppendTranscript(ctx, "ns-gamma", "t-gamma", &types.VoiceTranscriptRecord{
		Role:     "user",
		Content:  "unauthorized",
		Metadata: types.VoiceMetadata{SessionID: "s-1", TurnID: "t-1", IsFinal: true},
	}, nil)
	if !errors.Is(err, cortex.ErrNamespaceNotAllowed) {
		t.Fatalf("expected ErrNamespaceNotAllowed, got %v", err)
	}

	// Old voice transcript vs fresh voice transcript vs old non-voice message.
	oldTS := time.Now().UTC().Add(-72 * time.Hour)
	if _, err := client.Voice().AppendTranscript(ctx, "ns-alpha", "t-alpha-old", &types.VoiceTranscriptRecord{
		Role:    "user",
		Content: "expired voice transcript",
		Metadata: types.VoiceMetadata{
			SessionID: "sess-shared",
			TurnID:    "turn-old",
			Timestamp: oldTS,
			IsFinal:   true,
		},
	}, nil); err != nil {
		t.Fatalf("append old voice: %v", err)
	}
	if _, err := client.Voice().AppendTranscript(ctx, "ns-beta", "t-beta-fresh", &types.VoiceTranscriptRecord{
		Role:    "user",
		Content: "fresh beta transcript",
		Metadata: types.VoiceMetadata{
			SessionID: "sess-shared",
			TurnID:    "turn-fresh",
			Timestamp: time.Now().UTC(),
			IsFinal:   true,
		},
	}, nil); err != nil {
		t.Fatalf("append fresh beta voice: %v", err)
	}
	if _, err := client.Conversation().Append(ctx, "ns-alpha", "t-alpha-text", "user", "regular text message", nil); err != nil {
		t.Fatalf("append regular text: %v", err)
	}

	// Purge voice transcripts older than 24h: only ns-alpha's expired voice transcript is deleted.
	purged, err := client.Voice().PurgeExpired(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if purged != 1 {
		t.Fatalf("expected 1 purged voice transcript, got %d", purged)
	}

	// ns-beta fresh transcript and ns-alpha regular text message must remain intact.
	betaHist, err := client.Voice().History(ctx, "ns-beta", "t-beta-fresh", &cortex.VoiceHistoryOpts{SessionID: "sess-shared"})
	if err != nil {
		t.Fatalf("History ns-beta: %v", err)
	}
	if len(betaHist.Messages) != 1 {
		t.Fatalf("expected ns-beta fresh transcript retained, got %d", len(betaHist.Messages))
	}
	textHist, err := client.Conversation().History(ctx, "ns-alpha", "t-alpha-text", nil)
	if err != nil {
		t.Fatalf("History text: %v", err)
	}
	if len(textHist.Messages) != 1 {
		t.Fatalf("expected regular text message retained, got %d", len(textHist.Messages))
	}

	// Pagination across multiple turns in ns-beta.
	for i := 2; i <= 5; i++ {
		if _, err := client.Voice().AppendTranscript(ctx, "ns-beta", "t-beta-fresh", &types.VoiceTranscriptRecord{
			Role:    "user",
			Content: fmt.Sprintf("turn %d", i),
			Metadata: types.VoiceMetadata{
				SessionID: "sess-shared",
				TurnID:    fmt.Sprintf("turn-%d", i),
				Sequence:  int64(i),
				IsFinal:   true,
			},
		}, nil); err != nil {
			t.Fatalf("append turn %d: %v", i, err)
		}
	}
	page1, err := client.Voice().History(ctx, "ns-beta", "t-beta-fresh", &cortex.VoiceHistoryOpts{
		SessionID: "sess-shared",
		LastN:     2,
	})
	if err != nil || len(page1.Messages) != 2 || page1.NextCursor == "" {
		t.Fatalf("expected 2 messages and next cursor on page 1, got len=%d cursor=%q err=%v", len(page1.Messages), page1.NextCursor, err)
	}
	page2, err := client.Voice().History(ctx, "ns-beta", "t-beta-fresh", &cortex.VoiceHistoryOpts{
		SessionID: "sess-shared",
		LastN:     2,
		Cursor:    page1.NextCursor,
	})
	if err != nil || len(page2.Messages) != 2 || page2.NextCursor == "" {
		t.Fatalf("expected 2 messages and next cursor on page 2, got len=%d cursor=%q err=%v", len(page2.Messages), page2.NextCursor, err)
	}
}
