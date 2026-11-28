package types

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Canonical metadata keys stored inside Message.Metadata for voice records.
// Using the "voice." namespace preserves full backwards compatibility with the
// existing conversation storage schema and wire DTO contracts.
const (
	VoiceMetaSessionID   = "voice.session_id"
	VoiceMetaTurnID      = "voice.turn_id"
	VoiceMetaSpeakerRole = "voice.speaker_role"
	VoiceMetaSpeakerID   = "voice.speaker_id"
	VoiceMetaSource      = "voice.source"
	VoiceMetaProvider    = "voice.provider"
	VoiceMetaModel       = "voice.model"
	VoiceMetaLanguage    = "voice.language"
	VoiceMetaEventType   = "voice.event_type"
	VoiceMetaTimestamp   = "voice.timestamp"
	VoiceMetaDurationMS  = "voice.duration_ms"
	VoiceMetaSequence    = "voice.sequence"
	VoiceMetaFinal       = "voice.is_final"
)

// Standard source attribution labels for committed voice records.
const (
	VoiceSourceSTT           = "stt"
	VoiceSourceTranscription = "transcription"
	VoiceSourceAgent         = "agent"
	VoiceSourceVoiceSession  = "voice_session"
)

// Sentinel errors returned by voice metadata and record validation.
var (
	ErrInvalidVoiceMetadata  = errors.New("invalid voice metadata")
	ErrUncommittedTranscript = errors.New("uncommitted partial transcript cannot be persisted")
	ErrRawAudioDisallowed    = errors.New("raw audio persistence is disabled")
)

// VoiceMetadata captures stable session, turn, speaker, and source attribution
// metadata attached to committed voice conversation messages.
//
// Privacy: String and GoString emit non-sensitive correlation identifiers only.
type VoiceMetadata struct {
	SessionID   string        `json:"session_id"`
	TurnID      string        `json:"turn_id,omitempty"`
	SpeakerRole string        `json:"speaker_role,omitempty"`
	SpeakerID   string        `json:"speaker_id,omitempty"`
	Source      string        `json:"source,omitempty"`
	Provider    string        `json:"provider,omitempty"`
	Model       string        `json:"model,omitempty"`
	Language    string        `json:"language,omitempty"`
	EventType   string        `json:"event_type,omitempty"`
	Sequence    int64         `json:"sequence,omitempty"`
	Timestamp   time.Time     `json:"timestamp,omitempty"`
	Duration    time.Duration `json:"duration,omitempty"`
	IsFinal     bool          `json:"is_final"`
}

// Validate checks that VoiceMetadata carries a non-empty SessionID, represents
// a committed (IsFinal) utterance, and has valid optional role/timing bounds.
func (m VoiceMetadata) Validate() error {
	if !m.IsFinal {
		return ErrUncommittedTranscript
	}
	if strings.TrimSpace(m.SessionID) == "" {
		return fmt.Errorf("%w: session_id is required", ErrInvalidVoiceMetadata)
	}
	if m.SpeakerRole != "" && !isValidVoiceSpeakerRole(m.SpeakerRole) {
		return fmt.Errorf("%w: invalid speaker_role %q", ErrInvalidVoiceMetadata, m.SpeakerRole)
	}
	if m.Sequence < 0 {
		return fmt.Errorf("%w: sequence must be non-negative, got %d", ErrInvalidVoiceMetadata, m.Sequence)
	}
	if m.Duration < 0 {
		return fmt.Errorf("%w: duration must be non-negative, got %s", ErrInvalidVoiceMetadata, m.Duration)
	}
	return nil
}

// ToMap serializes VoiceMetadata into canonical "voice.*" metadata entries.
func (m VoiceMetadata) ToMap() map[string]string {
	out := make(map[string]string, 13)
	if v := strings.TrimSpace(m.SessionID); v != "" {
		out[VoiceMetaSessionID] = v
	}
	if v := strings.TrimSpace(m.TurnID); v != "" {
		out[VoiceMetaTurnID] = v
	}
	if v := strings.TrimSpace(m.SpeakerRole); v != "" {
		out[VoiceMetaSpeakerRole] = v
	}
	if v := strings.TrimSpace(m.SpeakerID); v != "" {
		out[VoiceMetaSpeakerID] = v
	}
	if v := strings.TrimSpace(m.Source); v != "" {
		out[VoiceMetaSource] = v
	}
	if v := strings.TrimSpace(m.Provider); v != "" {
		out[VoiceMetaProvider] = v
	}
	if v := strings.TrimSpace(m.Model); v != "" {
		out[VoiceMetaModel] = v
	}
	if v := strings.TrimSpace(m.Language); v != "" {
		out[VoiceMetaLanguage] = v
	}
	if v := strings.TrimSpace(m.EventType); v != "" {
		out[VoiceMetaEventType] = v
	}
	if m.Sequence > 0 {
		out[VoiceMetaSequence] = strconv.FormatInt(m.Sequence, 10)
	}
	if !m.Timestamp.IsZero() {
		out[VoiceMetaTimestamp] = m.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	if m.Duration > 0 {
		out[VoiceMetaDurationMS] = strconv.FormatInt(m.Duration.Milliseconds(), 10)
	}
	if m.IsFinal {
		out[VoiceMetaFinal] = "true"
	} else {
		out[VoiceMetaFinal] = "false"
	}
	return out
}

// ApplyTo returns a new metadata map combining dst with the canonical "voice.*"
// fields from m. Canonical voice keys take precedence over conflicting keys in
// dst, and dst is never mutated.
func (m VoiceMetadata) ApplyTo(dst map[string]string) map[string]string {
	voiceFields := m.ToMap()
	merged := make(map[string]string, len(dst)+len(voiceFields))
	for k, v := range dst {
		merged[k] = v
	}
	for k, v := range voiceFields {
		merged[k] = v
	}
	return merged
}

// String returns a log-safe summary containing correlation identifiers only.
func (m VoiceMetadata) String() string {
	return fmt.Sprintf(
		"VoiceMetadata{session_id=%q, turn_id=%q, speaker_role=%q, source=%q, provider=%q, model=%q, final=%t}",
		m.SessionID, m.TurnID, m.SpeakerRole, m.Source, m.Provider, m.Model, m.IsFinal,
	)
}

// GoString returns a log-safe representation for %#v formatting.
func (m VoiceMetadata) GoString() string {
	return "types." + m.String()
}

// ParseVoiceMetadata extracts structured VoiceMetadata from a Message.Metadata
// map. Returns (nil, false) when the map does not contain voice session
// metadata.
func ParseVoiceMetadata(meta map[string]string) (*VoiceMetadata, bool) {
	if len(meta) == 0 {
		return nil, false
	}
	sessionID := strings.TrimSpace(meta[VoiceMetaSessionID])
	if sessionID == "" {
		return nil, false
	}

	vm := &VoiceMetadata{
		SessionID:   sessionID,
		TurnID:      strings.TrimSpace(meta[VoiceMetaTurnID]),
		SpeakerRole: strings.TrimSpace(meta[VoiceMetaSpeakerRole]),
		SpeakerID:   strings.TrimSpace(meta[VoiceMetaSpeakerID]),
		Source:      strings.TrimSpace(meta[VoiceMetaSource]),
		Provider:    strings.TrimSpace(meta[VoiceMetaProvider]),
		Model:       strings.TrimSpace(meta[VoiceMetaModel]),
		Language:    strings.TrimSpace(meta[VoiceMetaLanguage]),
		EventType:   strings.TrimSpace(meta[VoiceMetaEventType]),
		IsFinal:     strings.EqualFold(strings.TrimSpace(meta[VoiceMetaFinal]), "true"),
	}

	if rawSeq := strings.TrimSpace(meta[VoiceMetaSequence]); rawSeq != "" {
		if seq, err := strconv.ParseInt(rawSeq, 10, 64); err == nil && seq >= 0 {
			vm.Sequence = seq
		}
	}
	if rawTS := strings.TrimSpace(meta[VoiceMetaTimestamp]); rawTS != "" {
		if ts, err := time.Parse(time.RFC3339Nano, rawTS); err == nil {
			vm.Timestamp = ts.UTC()
		}
	}
	if rawDur := strings.TrimSpace(meta[VoiceMetaDurationMS]); rawDur != "" {
		if ms, err := strconv.ParseInt(rawDur, 10, 64); err == nil && ms >= 0 {
			vm.Duration = time.Duration(ms) * time.Millisecond
		}
	}

	return vm, true
}

// IsVoiceMessage reports whether msg carries voice session correlation metadata.
func IsVoiceMessage(msg *Message) bool {
	if msg == nil {
		return false
	}
	_, ok := ParseVoiceMetadata(msg.Metadata)
	return ok
}

// VoiceTranscriptRecord represents a committed voice utterance or assistant
// voice text response ready to be persisted in conversation memory.
//
// Privacy: String and GoString omit Content and ExtraMetadata values so default
// logging never exposes transcripts or user data.
type VoiceTranscriptRecord struct {
	Role          string            `json:"role"`
	Content       string            `json:"content"`
	Metadata      VoiceMetadata     `json:"metadata"`
	ExtraMetadata map[string]string `json:"extra_metadata,omitempty"`
}

// EffectiveRole returns the resolved message role (falling back to
// Metadata.SpeakerRole when Role is unset).
func (r VoiceTranscriptRecord) EffectiveRole() string {
	if role := strings.TrimSpace(r.Role); role != "" {
		return role
	}
	return strings.TrimSpace(r.Metadata.SpeakerRole)
}

// Validate checks that the record carries committed voice metadata, non-blank
// transcript text, a valid role, and no raw audio payloads.
func (r *VoiceTranscriptRecord) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: voice transcript record cannot be nil", ErrInvalidVoiceMetadata)
	}
	if err := r.Metadata.Validate(); err != nil {
		return err
	}
	role := r.EffectiveRole()
	if !isValidVoiceSpeakerRole(role) {
		return fmt.Errorf("%w: invalid role %q", ErrInvalidVoiceMetadata, role)
	}
	if strings.TrimSpace(r.Content) == "" {
		return fmt.Errorf("%w: content cannot be empty", ErrInvalidVoiceMetadata)
	}
	if key, bad := HasRawAudioMetadata(r.ExtraMetadata); bad {
		return fmt.Errorf("%w: metadata key %q cannot store raw audio", ErrRawAudioDisallowed, key)
	}
	if LooksLikeInlineAudioPayload(r.Content) {
		return fmt.Errorf("%w: inline audio data URI is not allowed in transcript content", ErrRawAudioDisallowed)
	}
	return nil
}

// String returns a log-safe summary omitting transcript content and raw metadata.
func (r VoiceTranscriptRecord) String() string {
	return fmt.Sprintf(
		"VoiceTranscriptRecord{role=%q, content_len=%d, meta=%s, extra_keys=%d}",
		r.EffectiveRole(), len(r.Content), r.Metadata.String(), len(r.ExtraMetadata),
	)
}

// GoString returns a log-safe representation for %#v formatting.
func (r VoiceTranscriptRecord) GoString() string {
	return "types." + r.String()
}

// VoiceHistoryResponse represents namespace-scoped committed voice conversation
// history retrieved for reasoning context injection.
type VoiceHistoryResponse struct {
	Messages   []*Message `json:"messages"`
	Summary    string     `json:"summary,omitempty"`
	NextCursor string     `json:"next_cursor,omitempty"`
	ThreadID   string     `json:"thread_id"`
	SessionID  string     `json:"session_id,omitempty"`
}

// IsAudioMetadataKey reports whether key names a raw audio payload field that
// must be rejected when raw audio persistence is disabled.
func IsAudioMetadataKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}
	switch k {
	case "audio", "raw_audio", "audio_bytes", "audio_data", "audio_base64",
		"audio_chunk", "audio_payload", "pcm", "pcm_bytes", "wav", "mp3":
		return true
	}
	for _, prefix := range []string{"audio.", "raw_audio.", "voice.audio", "voice.raw_audio"} {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// HasRawAudioMetadata checks whether meta contains any key that appears to
// store raw audio bytes or encoded audio payloads.
func HasRawAudioMetadata(meta map[string]string) (string, bool) {
	for k, v := range meta {
		if IsAudioMetadataKey(k) {
			return k, true
		}
		if LooksLikeInlineAudioPayload(v) {
			return k, true
		}
	}
	return "", false
}

// LooksLikeInlineAudioPayload reports whether s carries an inline audio data URI
// (e.g., "data:audio/mpeg;base64,...").
func LooksLikeInlineAudioPayload(s string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(trimmed, "data:audio/")
}

func isValidVoiceSpeakerRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "user", "assistant", "system", "tool":
		return true
	default:
		return false
	}
}
