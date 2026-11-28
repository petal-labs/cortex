package conversation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/observability"
	"github.com/petal-labs/cortex/pkg/types"
)

func setupVoiceEngine(t *testing.T, enabled bool, allowedNS []string) *Engine {
	t.Helper()
	engine, backend := setupTestEngine(t, false)
	t.Cleanup(func() { _ = backend.Close() })
	engine.SetVoiceConfig(&config.VoiceConfig{
		TranscriptStorageEnabled: enabled,
	})
	if len(allowedNS) > 0 {
		engine.SetAllowedNamespaces(allowedNS)
	}
	return engine
}

func TestAppendVoiceTranscriptOptInAndPrivacy(t *testing.T) {
	ctx := context.Background()

	t.Run("disabled by default (opt-in required)", func(t *testing.T) {
		engine := setupVoiceEngine(t, false, nil)
		_, err := engine.AppendVoiceTranscript(ctx, "ns-1", "thread-1", &types.VoiceTranscriptRecord{
			Role:    "user",
			Content: "hello",
			Metadata: types.VoiceMetadata{
				SessionID: "sess-1",
				TurnID:    "turn-1",
				Source:    types.VoiceSourceSTT,
				IsFinal:   true,
			},
		}, nil)
		if !errors.Is(err, ErrVoiceStorageDisabled) {
			t.Fatalf("expected ErrVoiceStorageDisabled, got %v", err)
		}
	})

	t.Run("rejects uncommitted partial transcript", func(t *testing.T) {
		engine := setupVoiceEngine(t, true, nil)
		_, err := engine.AppendVoiceTranscript(ctx, "ns-1", "thread-1", &types.VoiceTranscriptRecord{
			Role:    "user",
			Content: "hel...",
			Metadata: types.VoiceMetadata{
				SessionID: "sess-1",
				TurnID:    "turn-1",
				Source:    types.VoiceSourceSTT,
				IsFinal:   false,
			},
		}, nil)
		if !errors.Is(err, types.ErrUncommittedTranscript) {
			t.Fatalf("expected ErrUncommittedTranscript, got %v", err)
		}
	})

	t.Run("rejects raw audio metadata and inline data URI", func(t *testing.T) {
		engine := setupVoiceEngine(t, true, nil)
		_, err := engine.AppendVoiceTranscript(ctx, "ns-1", "thread-1", &types.VoiceTranscriptRecord{
			Role:    "user",
			Content: "hello",
			Metadata: types.VoiceMetadata{
				SessionID: "sess-1",
				TurnID:    "turn-1",
				IsFinal:   true,
			},
		}, &VoiceAppendOpts{
			Metadata: map[string]string{"raw_audio": "base64pcm"},
		})
		if !errors.Is(err, types.ErrRawAudioDisallowed) {
			t.Fatalf("expected ErrRawAudioDisallowed, got %v", err)
		}

		_, err = engine.AppendVoiceTranscript(ctx, "ns-1", "thread-1", &types.VoiceTranscriptRecord{
			Role:    "user",
			Content: "data:audio/wav;base64,UklGRg==",
			Metadata: types.VoiceMetadata{
				SessionID: "sess-1",
				TurnID:    "turn-1",
				IsFinal:   true,
			},
		}, nil)
		if !errors.Is(err, types.ErrRawAudioDisallowed) {
			t.Fatalf("expected ErrRawAudioDisallowed for data URI, got %v", err)
		}
	})

	t.Run("safe logging omits transcript content", func(t *testing.T) {
		core, logs := observer.New(zap.DebugLevel)
		prevLogger := observability.DefaultLogger
		observability.DefaultLogger = &observability.Logger{Logger: zap.New(core)}
		t.Cleanup(func() { observability.DefaultLogger = prevLogger })

		engine := setupVoiceEngine(t, true, nil)
		secret := "PRIVATE_VOICE_TRANSCRIPT_PHRASE_XYZ"
		_, err := engine.AppendVoiceTranscript(ctx, "ns-log", "thread-log", &types.VoiceTranscriptRecord{
			Role:    "user",
			Content: secret,
			Metadata: types.VoiceMetadata{
				SessionID: "sess-log",
				TurnID:    "turn-log",
				Source:    types.VoiceSourceSTT,
				Provider:  "elevenlabs",
				IsFinal:   true,
			},
		}, nil)
		if err != nil {
			t.Fatalf("AppendVoiceTranscript: %v", err)
		}
		if logs.Len() == 0 {
			t.Fatal("expected at least one debug log entry")
		}
		for _, entry := range logs.All() {
			if strings.Contains(entry.Message, secret) {
				t.Fatalf("log message leaked transcript: %s", entry.Message)
			}
			for _, f := range entry.Context {
				if strings.Contains(f.String, secret) {
					t.Fatalf("log field %q leaked transcript", f.Key)
				}
			}
		}
	})
}

func TestAppendVoiceTranscriptDeduplicationAndCancellation(t *testing.T) {
	ctx := context.Background()
	engine := setupVoiceEngine(t, true, nil)

	rec := &types.VoiceTranscriptRecord{
		Role:    "user",
		Content: "Check my calendar",
		Metadata: types.VoiceMetadata{
			SessionID: "sess-dedup",
			TurnID:    "turn-1",
			Source:    types.VoiceSourceSTT,
			EventType: "user_transcript_committed",
			Sequence:  3,
			IsFinal:   true,
		},
	}

	first, err := engine.AppendVoiceTranscript(ctx, "ns-1", "thread-1", rec, nil)
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	second, err := engine.AppendVoiceTranscript(ctx, "ns-1", "thread-1", rec, nil)
	if err != nil {
		t.Fatalf("second append: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("expected idempotent replay to return same message ID %q, got %q", first.ID, second.ID)
	}

	hist, err := engine.VoiceHistory(ctx, "ns-1", "thread-1", nil)
	if err != nil {
		t.Fatalf("VoiceHistory: %v", err)
	}
	if len(hist.Messages) != 1 {
		t.Fatalf("expected 1 deduplicated message, got %d", len(hist.Messages))
	}

	// Cancelled context must fail fast without persisting.
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = engine.AppendVoiceTranscript(cancelCtx, "ns-1", "thread-1", &types.VoiceTranscriptRecord{
		Role:    "assistant",
		Content: "Should not be persisted",
		Metadata: types.VoiceMetadata{
			SessionID: "sess-dedup",
			TurnID:    "turn-2",
			IsFinal:   true,
		},
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	hist, err = engine.VoiceHistory(ctx, "ns-1", "thread-1", nil)
	if err != nil {
		t.Fatalf("VoiceHistory after cancel: %v", err)
	}
	if len(hist.Messages) != 1 {
		t.Fatalf("expected cancelled append not to persist, got %d messages", len(hist.Messages))
	}
}

func TestVoiceHistoryOrderingPaginationSummaryAndIsolation(t *testing.T) {
	ctx := context.Background()
	engine := setupVoiceEngine(t, true, []string{"ns-allowed", "ns-other"})

	// Disallowed namespace must be rejected on both write and read.
	_, err := engine.AppendVoiceTranscript(ctx, "ns-blocked", "t1", &types.VoiceTranscriptRecord{
		Role:     "user",
		Content:  "blocked",
		Metadata: types.VoiceMetadata{SessionID: "s1", TurnID: "t1", IsFinal: true},
	}, nil)
	if !errors.Is(err, ErrNamespaceNotAllowed) {
		t.Fatalf("expected ErrNamespaceNotAllowed on write, got %v", err)
	}
	_, err = engine.VoiceHistory(ctx, "ns-blocked", "t1", nil)
	if !errors.Is(err, ErrNamespaceNotAllowed) {
		t.Fatalf("expected ErrNamespaceNotAllowed on read, got %v", err)
	}

	// Append non-voice message + 6 voice messages sharing the exact same second.
	sameSec := time.Now().UTC().Truncate(time.Second)
	if _, err := engine.Append(ctx, "ns-allowed", "shared-thread", "user", "non-voice text", nil); err != nil {
		t.Fatalf("Append non-voice: %v", err)
	}

	expectedContents := []string{
		"user turn 1", "assistant turn 1",
		"user turn 2", "assistant turn 2",
		"user turn 3", "assistant turn 3",
	}
	for i, content := range expectedContents {
		role := "user"
		source := types.VoiceSourceSTT
		if i%2 == 1 {
			role = "assistant"
			source = types.VoiceSourceAgent
		}
		_, err := engine.AppendVoiceTranscript(ctx, "ns-allowed", "shared-thread", &types.VoiceTranscriptRecord{
			Role:    role,
			Content: content,
			Metadata: types.VoiceMetadata{
				SessionID: "sess-a",
				TurnID:    "turn-" + string(rune('1'+i/2)),
				Source:    source,
				Timestamp: sameSec,
				Sequence:  int64(i + 1),
				IsFinal:   true,
			},
		}, nil)
		if err != nil {
			t.Fatalf("AppendVoiceTranscript %d: %v", i, err)
		}
	}

	// Also write into another namespace with the same session_id (distinct thread_id per threads PK).
	if _, err := engine.AppendVoiceTranscript(ctx, "ns-other", "shared-thread-other", &types.VoiceTranscriptRecord{
		Role:    "user",
		Content: "SECRET FROM OTHER NS",
		Metadata: types.VoiceMetadata{
			SessionID: "sess-a",
			TurnID:    "turn-1",
			Source:    types.VoiceSourceSTT,
			IsFinal:   true,
		},
	}, nil); err != nil {
		t.Fatalf("AppendVoiceTranscript ns-other: %v", err)
	}

	// Cross-namespace read of ns-allowed's thread from ns-other must return empty.
	crossHist, err := engine.VoiceHistory(ctx, "ns-other", "shared-thread", &VoiceHistoryOpts{SessionID: "sess-a"})
	if err != nil {
		t.Fatalf("cross-namespace VoiceHistory: %v", err)
	}
	if len(crossHist.Messages) != 0 {
		t.Fatalf("expected 0 messages on cross-namespace read, got %d", len(crossHist.Messages))
	}

	// Paginate 2 at a time and reconstruct full chronological history.
	var pages [][]*types.Message
	cursor := ""
	for p := 0; p < 10; p++ {
		res, err := engine.VoiceHistory(ctx, "ns-allowed", "shared-thread", &VoiceHistoryOpts{
			SessionID: "sess-a",
			LastN:     2,
			Cursor:    cursor,
		})
		if err != nil {
			t.Fatalf("VoiceHistory page %d: %v", p, err)
		}
		pages = append(pages, res.Messages)
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if len(pages) != 3 {
		t.Fatalf("expected 3 pages of 2 messages, got %d pages", len(pages))
	}

	// Reassemble oldest -> newest (page 2 is oldest, page 0 is newest).
	var got []string
	for i := len(pages) - 1; i >= 0; i-- {
		for _, m := range pages[i] {
			got = append(got, m.Content)
		}
	}
	if len(got) != len(expectedContents) {
		t.Fatalf("expected %d messages, got %d (%v)", len(expectedContents), len(got), got)
	}
	for i := range expectedContents {
		if got[i] != expectedContents[i] {
			t.Errorf("index %d: got %q, want %q", i, got[i], expectedContents[i])
		}
	}

	// Summary integration: summarize oldest messages, then query with IncludeSummary=true.
	engine.SetSummarizer(&stubVoiceSummarizer{summary: "Earlier turns discussed onboarding."})
	if _, err := engine.Summarize(ctx, "ns-allowed", "shared-thread", &SummarizeOpts{KeepRecent: 2}); err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	withSummary, err := engine.VoiceHistory(ctx, "ns-allowed", "shared-thread", &VoiceHistoryOpts{
		SessionID:      "sess-a",
		IncludeSummary: true,
	})
	if err != nil {
		t.Fatalf("VoiceHistory with summary: %v", err)
	}
	if withSummary.Summary != "Earlier turns discussed onboarding." {
		t.Errorf("unexpected summary: %q", withSummary.Summary)
	}
	if len(withSummary.Messages) != 2 || withSummary.Messages[0].Content != "user turn 3" || withSummary.Messages[1].Content != "assistant turn 3" {
		t.Errorf("expected only 2 unsummarized recent voice messages alongside summary, got %d", len(withSummary.Messages))
	}

	// Delete session in ns-allowed must not touch ns-other.
	deleted, err := engine.DeleteVoiceSession(ctx, "ns-allowed", "sess-a")
	if err != nil {
		t.Fatalf("DeleteVoiceSession: %v", err)
	}
	if deleted != 6 {
		t.Fatalf("expected 6 deleted messages, got %d", deleted)
	}
	otherHist, err := engine.VoiceHistory(ctx, "ns-other", "shared-thread-other", &VoiceHistoryOpts{SessionID: "sess-a"})
	if err != nil {
		t.Fatalf("VoiceHistory ns-other after delete: %v", err)
	}
	if len(otherHist.Messages) != 1 || otherHist.Messages[0].Content != "SECRET FROM OTHER NS" {
		t.Fatalf("expected ns-other untouched, got %+v", otherHist.Messages)
	}
}

type stubVoiceSummarizer struct {
	summary string
}

func (s *stubVoiceSummarizer) SummarizeMessages(_ context.Context, _ []*types.Message) (string, error) {
	return s.summary, nil
}
