package types

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestVoiceMetadataRoundTrip(t *testing.T) {
	ts := time.Date(2026, 11, 27, 18, 30, 0, 123456000, time.UTC)
	meta := VoiceMetadata{
		SessionID:   "sess-101",
		TurnID:      "turn-7",
		SpeakerRole: "user",
		SpeakerID:   "speaker_0",
		Source:      VoiceSourceSTT,
		Provider:    "elevenlabs",
		Model:       "scribe_v2",
		Language:    "en",
		EventType:   "user_transcript_committed",
		Sequence:    42,
		Timestamp:   ts,
		Duration:    1500 * time.Millisecond,
		IsFinal:     true,
	}

	if err := meta.Validate(); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	existing := map[string]string{
		"custom":           "keep-me",
		VoiceMetaSessionID: "should-be-overwritten",
	}
	merged := meta.ApplyTo(existing)
	if existing[VoiceMetaSessionID] != "should-be-overwritten" {
		t.Fatal("ApplyTo mutated caller map")
	}
	if merged["custom"] != "keep-me" {
		t.Errorf("expected custom metadata preserved, got %q", merged["custom"])
	}

	parsed, ok := ParseVoiceMetadata(merged)
	if !ok {
		t.Fatal("expected ParseVoiceMetadata to return ok=true")
	}
	if parsed.SessionID != meta.SessionID ||
		parsed.TurnID != meta.TurnID ||
		parsed.SpeakerRole != meta.SpeakerRole ||
		parsed.SpeakerID != meta.SpeakerID ||
		parsed.Source != meta.Source ||
		parsed.Provider != meta.Provider ||
		parsed.Model != meta.Model ||
		parsed.Language != meta.Language ||
		parsed.EventType != meta.EventType ||
		parsed.Sequence != meta.Sequence ||
		!parsed.Timestamp.Equal(ts) ||
		parsed.Duration != meta.Duration ||
		!parsed.IsFinal {
		t.Errorf("round-trip mismatch:\n got: %+v\nwant: %+v", parsed, meta)
	}

	msg := &Message{ID: "m1", Metadata: merged}
	if !IsVoiceMessage(msg) {
		t.Error("expected IsVoiceMessage to be true")
	}
	if IsVoiceMessage(&Message{ID: "m2", Metadata: map[string]string{"foo": "bar"}}) {
		t.Error("expected IsVoiceMessage to be false for non-voice message")
	}
	if IsVoiceMessage(nil) {
		t.Error("expected IsVoiceMessage(nil) to be false")
	}
}

func TestVoiceMetadataValidation(t *testing.T) {
	t.Run("rejects uncommitted transcript", func(t *testing.T) {
		err := (VoiceMetadata{SessionID: "s1", IsFinal: false}).Validate()
		if !errors.Is(err, ErrUncommittedTranscript) {
			t.Errorf("expected ErrUncommittedTranscript, got %v", err)
		}
	})

	t.Run("rejects missing session_id", func(t *testing.T) {
		err := (VoiceMetadata{IsFinal: true}).Validate()
		if !errors.Is(err, ErrInvalidVoiceMetadata) {
			t.Errorf("expected ErrInvalidVoiceMetadata, got %v", err)
		}
	})

	t.Run("rejects invalid speaker_role", func(t *testing.T) {
		err := (VoiceMetadata{SessionID: "s1", SpeakerRole: "narrator", IsFinal: true}).Validate()
		if !errors.Is(err, ErrInvalidVoiceMetadata) {
			t.Errorf("expected ErrInvalidVoiceMetadata, got %v", err)
		}
	})

	t.Run("rejects negative sequence or duration", func(t *testing.T) {
		if err := (VoiceMetadata{SessionID: "s1", Sequence: -1, IsFinal: true}).Validate(); !errors.Is(err, ErrInvalidVoiceMetadata) {
			t.Errorf("expected ErrInvalidVoiceMetadata for negative sequence, got %v", err)
		}
		if err := (VoiceMetadata{SessionID: "s1", Duration: -time.Second, IsFinal: true}).Validate(); !errors.Is(err, ErrInvalidVoiceMetadata) {
			t.Errorf("expected ErrInvalidVoiceMetadata for negative duration, got %v", err)
		}
	})
}

func TestVoiceTranscriptRecordValidationAndPrivacy(t *testing.T) {
	secretPhrase := "CONFIDENTIAL_TRANSCRIPT_TEXT_12345"
	rec := VoiceTranscriptRecord{
		Role:    "user",
		Content: secretPhrase,
		Metadata: VoiceMetadata{
			SessionID: "sess-priv",
			TurnID:    "turn-1",
			Source:    VoiceSourceSTT,
			IsFinal:   true,
		},
	}

	if err := rec.Validate(); err != nil {
		t.Fatalf("expected valid record, got %v", err)
	}
	if strings.Contains(rec.String(), secretPhrase) || strings.Contains(rec.GoString(), secretPhrase) {
		t.Fatalf("String/GoString leaked transcript text: %s", rec.String())
	}

	t.Run("rejects raw audio metadata keys", func(t *testing.T) {
		bad := rec
		bad.ExtraMetadata = map[string]string{"raw_audio": "AAAA"}
		if err := bad.Validate(); !errors.Is(err, ErrRawAudioDisallowed) {
			t.Errorf("expected ErrRawAudioDisallowed, got %v", err)
		}
	})

	t.Run("rejects inline audio data URI in content", func(t *testing.T) {
		bad := rec
		bad.Content = "data:audio/mpeg;base64,SUQzAw=="
		if err := bad.Validate(); !errors.Is(err, ErrRawAudioDisallowed) {
			t.Errorf("expected ErrRawAudioDisallowed, got %v", err)
		}
	})

	t.Run("rejects inline audio data URI in metadata value", func(t *testing.T) {
		bad := rec
		bad.ExtraMetadata = map[string]string{"attachment": "data:audio/pcm;base64,AAAA"}
		if err := bad.Validate(); !errors.Is(err, ErrRawAudioDisallowed) {
			t.Errorf("expected ErrRawAudioDisallowed, got %v", err)
		}
	})
}
