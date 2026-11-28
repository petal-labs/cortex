// Example: Conversational voice agent backed by Iris v2 + Cortex Voice Memory.
//
// Demonstrates the end-to-end voice memory loop using only public packages
// (github.com/petal-labs/cortex/pkg/cortex, pkg/types, and
// github.com/petal-labs/iris/v2/{core,voice}):
//  1. Opt-in voice transcript storage with namespace enforcement
//  2. Iris voice.Pipeline streaming STT -> LLM -> streaming TTS
//  3. Committed-only persistence with session/turn correlation metadata
//     (partials, deltas, and raw audio bytes are never stored)
//  4. Retrieving namespace-scoped voice history + rolling summary for
//     Iris reasoning context injection in a subsequent session
//  5. Explicit session deletion for privacy compliance
//
// Runs offline out-of-the-box with deterministic mock speech/LLM providers:
//
//	go run -tags sqlite_fts5 ./examples/voice-agent
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/petal-labs/iris/v2/core"
	"github.com/petal-labs/iris/v2/voice"

	"github.com/petal-labs/cortex/pkg/cortex"
	"github.com/petal-labs/cortex/pkg/types"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tmpDir, err := os.MkdirTemp("", "cortex-voice-agent-*")
	if err != nil {
		log.Fatalf("create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	const (
		namespace = "support-voice"
		threadID  = "caller-42-thread"
		sessionID = "voice-session-001"
	)

	// 1. Open Cortex with explicit opt-in voice transcript storage, 30-day voice
	//    retention policy, namespace allowlist, and a thread summarizer.
	client, err := cortex.Open(ctx,
		cortex.WithSQLite(tmpDir),
		cortex.WithVoiceTranscriptStorage(true),
		cortex.WithVoiceTranscriptRetention(30),
		cortex.WithAllowedNamespaces(namespace),
		cortex.WithSummarizer(&demoSummarizer{}),
	)
	if err != nil {
		log.Fatalf("open cortex: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("close cortex: %v", err)
		}
	}()

	// 2. Construct an Iris v2 voice.Pipeline.
	//    In production, replace mockSTT/mockTTS with elevenlabs.New(apiKey) and
	//    mockLLM with openai.New(...) / anthropic.New(...) / gemini.New(...).
	stt := &demoSTT{}
	llm := &demoLLM{
		replies: []string{
			"Welcome back, Maya! Your order 8821 ships tomorrow morning.",
			"I have updated the delivery note to leave it at the side porch.",
		},
	}
	tts := &demoTTS{}

	pipeline, err := voice.NewPipeline(voice.PipelineConfig{
		STT:         stt,
		LLM:         llm,
		LLMModel:    "demo-chat",
		RealtimeTTS: tts,
		VoiceID:     "demo-voice",
	})
	if err != nil {
		log.Fatalf("create voice pipeline: %v", err)
	}

	sess, err := pipeline.StartVoiceSession(ctx, core.VoiceSessionConfig{
		SessionID: sessionID,
		Language:  "en",
	})
	if err != nil {
		log.Fatalf("start voice session: %v", err)
	}

	// 3. Attach a Cortex VoiceSessionRecorder. It filters out partials, deltas,
	//    and raw audio chunks, persisting ONLY committed utterances/responses.
	recorder := client.Voice().NewSessionRecorder(namespace, threadID, &cortex.VoiceRecordOpts{
		Provider: "elevenlabs",
		Model:    "scribe_v2",
	})

	turn1Done := make(chan struct{})
	turn2Done := make(chan struct{})
	completed := 0
	audioChunksPlayed := 0

	drainErr := make(chan error, 1)
	go func() {
		drainErr <- recorder.Drain(ctx, sess, func(evt core.VoiceEvent) {
			if evt.Type == core.VoiceEventAudioOutput && recorder.ShouldPlayAudio(evt) {
				audioChunksPlayed++
			}
			if evt.Type == core.VoiceEventTurnCompleted {
				completed++
				if completed == 1 {
					close(turn1Done)
				} else if completed == 2 {
					close(turn2Done)
				}
			}
		})
	}()

	// Turn 1: interim partial hypotheses followed by a committed user transcript.
	stt.sess.events <- core.RealtimeTranscriptEvent{
		Type:       core.RealtimeTranscriptEventPartial,
		SessionID:  sessionID,
		IsFinal:    false,
		Transcript: &core.TranscriptionResponse{Text: "Hi, this is Maya", IsFinal: false},
	}
	stt.sess.events <- core.RealtimeTranscriptEvent{
		Type:      core.RealtimeTranscriptEventCommitted,
		SessionID: sessionID,
		IsFinal:   true,
		Transcript: &core.TranscriptionResponse{
			Model:    "scribe_v2",
			Text:     "Hi, this is Maya checking on order 8821.",
			Language: "en",
			Duration: 1800 * time.Millisecond,
			IsFinal:  true,
		},
	}

	select {
	case <-turn1Done:
	case <-ctx.Done():
		log.Fatalf("timed out waiting for turn 1: %v", ctx.Err())
	}

	// Turn 2: committed follow-up utterance.
	stt.sess.events <- core.RealtimeTranscriptEvent{
		Type:      core.RealtimeTranscriptEventCommitted,
		SessionID: sessionID,
		IsFinal:   true,
		Transcript: &core.TranscriptionResponse{
			Model:    "scribe_v2",
			Text:     "Please leave the package at the side porch.",
			Language: "en",
			Duration: 1500 * time.Millisecond,
			IsFinal:  true,
		},
	}

	select {
	case <-turn2Done:
	case <-ctx.Done():
		log.Fatalf("timed out waiting for turn 2: %v", ctx.Err())
	}
	if err := sess.Close(); err != nil {
		log.Fatalf("close voice session: %v", err)
	}
	if err := <-drainErr; err != nil {
		log.Fatalf("drain voice session: %v", err)
	}

	fmt.Printf("Session %s finished (%d playable audio chunks; raw audio NOT persisted).\n\n",
		sessionID, audioChunksPlayed)

	// 4. Summarize older turns and retrieve Iris-ready context for a new session.
	if _, err := client.Conversation().Summarize(ctx, namespace, threadID, &cortex.SummarizeOpts{KeepRecent: 2}); err != nil {
		log.Fatalf("summarize thread: %v", err)
	}

	irisMsgs, hist, err := client.Voice().IrisMessages(ctx, namespace, threadID, &cortex.IrisContextOpts{
		SessionID:      sessionID,
		IncludeSummary: true,
		SystemPrompt:   "You are a concise voice support agent.",
	})
	if err != nil {
		log.Fatalf("retrieve iris context: %v", err)
	}

	fmt.Printf("Retrieved %d Iris context messages (summary present: %t):\n", len(irisMsgs), hist.Summary != "")
	for i, m := range irisMsgs {
		fmt.Printf("  [%d] (%s) %s\n", i, m.Role, m.Content)
	}
	fmt.Println()

	// 5. Inject retrieved context into a follow-up Iris chat call.
	followUpLLM := &demoLLM{replies: []string{"Your order 8821 is set for side-porch delivery tomorrow morning."}}
	followUpResp, err := followUpLLM.Chat(ctx, &core.ChatRequest{
		Model: "demo-chat",
		Messages: append(irisMsgs, core.Message{
			Role:    core.RoleUser,
			Content: "Can you recap my delivery plan?",
		}),
	})
	if err != nil {
		log.Fatalf("follow-up chat: %v", err)
	}
	fmt.Printf("Follow-up agent reply: %s\n\n", followUpResp.Output)

	// 6. Demonstrate explicit session deletion for privacy / right-to-be-forgotten.
	deleted, err := client.Voice().DeleteSession(ctx, namespace, sessionID)
	if err != nil {
		log.Fatalf("delete voice session: %v", err)
	}
	fmt.Printf("Deleted %d voice transcript record(s) for session %s.\n", deleted, sessionID)
}

// --- Offline deterministic doubles for the runnable demo ---

type demoSummarizer struct{}

func (d *demoSummarizer) SummarizeMessages(_ context.Context, msgs []*types.Message) (string, error) {
	return fmt.Sprintf("Prior %d message(s) confirmed Maya's order 8821 ships tomorrow morning.", len(msgs)), nil
}

type demoSTT struct {
	mu   sync.Mutex
	sess *demoSTTSession
}

func (d *demoSTT) StartRealtimeTranscription(_ context.Context, _ core.RealtimeTranscriptionConfig) (core.RealtimeTranscriptionSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sess == nil {
		d.sess = &demoSTTSession{
			events: make(chan core.RealtimeTranscriptEvent, 32),
			errCh:  make(chan error, 1),
		}
	}
	return d.sess, nil
}

type demoSTTSession struct {
	events    chan core.RealtimeTranscriptEvent
	errCh     chan error
	closeOnce sync.Once
}

func (s *demoSTTSession) SendAudio(_ context.Context, _ core.AudioChunk) error { return nil }
func (s *demoSTTSession) Commit(_ context.Context) error                       { return nil }
func (s *demoSTTSession) Events() <-chan core.RealtimeTranscriptEvent          { return s.events }
func (s *demoSTTSession) Err() <-chan error                                    { return s.errCh }
func (s *demoSTTSession) Close() error {
	s.closeOnce.Do(func() {
		close(s.events)
		close(s.errCh)
	})
	return nil
}

type demoTTS struct{}

func (d *demoTTS) StartRealtimeSynthesis(_ context.Context, cfg core.RealtimeSynthesisConfig) (core.RealtimeSynthesisSession, error) {
	format := cfg.Format
	if format.IsZero() {
		format = core.AudioFormatPCM16000
	}
	return &demoTTSSession{
		format: format,
		audio:  make(chan core.AudioChunk, 32),
		errCh:  make(chan error, 1),
	}, nil
}

type demoTTSSession struct {
	format    core.AudioFormat
	audio     chan core.AudioChunk
	errCh     chan error
	seq       int64
	closeOnce sync.Once
}

func (s *demoTTSSession) SendText(_ context.Context, text string) error {
	s.audio <- core.AudioChunk{Data: []byte("audio:" + text), Format: s.format, Sequence: s.seq}
	s.seq++
	return nil
}
func (s *demoTTSSession) Flush(_ context.Context) error { return nil }
func (s *demoTTSSession) Finish(_ context.Context) error {
	s.closeOnce.Do(func() {
		s.audio <- core.AudioChunk{Format: s.format, Sequence: s.seq, IsFinal: true}
		close(s.audio)
		close(s.errCh)
	})
	return nil
}
func (s *demoTTSSession) Audio() <-chan core.AudioChunk { return s.audio }
func (s *demoTTSSession) Err() <-chan error             { return s.errCh }
func (s *demoTTSSession) Close() error {
	s.closeOnce.Do(func() {
		close(s.audio)
		close(s.errCh)
	})
	return nil
}

type demoLLM struct {
	mu      sync.Mutex
	replies []string
	idx     int
}

func (l *demoLLM) ID() string { return "demo-llm" }
func (l *demoLLM) Models() []core.ModelInfo {
	return []core.ModelInfo{{ID: "demo-chat", Capabilities: []core.Feature{core.FeatureChat, core.FeatureChatStreaming}}}
}
func (l *demoLLM) Supports(f core.Feature) bool {
	return f == core.FeatureChat || f == core.FeatureChatStreaming
}
func (l *demoLLM) next() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.idx < len(l.replies) {
		r := l.replies[l.idx]
		l.idx++
		return r
	}
	return "Okay."
}
func (l *demoLLM) Chat(_ context.Context, req *core.ChatRequest) (*core.ChatResponse, error) {
	return &core.ChatResponse{ID: "demo-resp", Model: req.Model, Output: l.next()}, nil
}
func (l *demoLLM) StreamChat(ctx context.Context, req *core.ChatRequest) (*core.ChatStream, error) {
	reply := l.next()
	ch := make(chan core.ChatChunk, 8)
	errCh := make(chan error, 1)
	finalCh := make(chan *core.ChatResponse, 1)
	go func() {
		defer close(ch)
		defer close(errCh)
		defer close(finalCh)
		for i, word := range strings.Fields(reply) {
			if i > 0 {
				word = " " + word
			}
			select {
			case ch <- core.ChatChunk{Delta: word}:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}
		finalCh <- &core.ChatResponse{ID: "demo-stream", Model: req.Model, Output: reply}
	}()
	return &core.ChatStream{Ch: ch, Err: errCh, Final: finalCh}, nil
}
