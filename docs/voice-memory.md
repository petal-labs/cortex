# Voice Conversation Memory (Iris v2 / ElevenLabs Integration)

**Iris Dependency Revision**: `github.com/petal-labs/iris/v2 v2.0.0` (Go `1.25.0`)  
**Scope**: Cortex issues `#76`, `#77`, `#78`, `#79`, and `#80`.

Cortex provides opt-in, privacy-first conversational memory for real-time voice
agents and batch speech transcriptions produced by Iris v2 (including ElevenLabs
Scribe STT, streaming TTS, `voice.Pipeline`, Speech Engine, and ElevenAgents).

Cortex intentionally does **not** duplicate Iris speech/transport logic; it owns
persistent committed transcript memory, correlation metadata, namespace-scoped
history retrieval, summarization integration, retention, and deletion.

---

## 1. Correlation Metadata Schema (No Breaking Schema Changes)

Voice records are stored inside Cortex's existing `messages` and `threads`
tables using canonical `voice.*` keys in `Message.Metadata` (`pkg/types/voice.go`).
Existing conversation tables, embeddings, FTS5/tsvector indexes, export/import,
and MCP `schema_version: 1` DTO contracts remain 100% backwards-compatible.

| Metadata Key | Constant | Description |
|---|---|---|
| `voice.session_id` | `types.VoiceMetaSessionID` | Stable voice session correlation ID (required) |
| `voice.turn_id` | `types.VoiceMetaTurnID` | Monotonic turn correlation ID from Iris `VoiceEvent.TurnID` |
| `voice.speaker_role` | `types.VoiceMetaSpeakerRole` | Speaker role (`user`, `assistant`, `system`, `tool`) |
| `voice.speaker_id` | `types.VoiceMetaSpeakerID` | Optional diarized speaker label (e.g., `speaker_0`) |
| `voice.source` | `types.VoiceMetaSource` | Attribution (`stt`, `transcription`, `agent`, `voice_session`) |
| `voice.provider` | `types.VoiceMetaProvider` | Provider attribution (e.g., `elevenlabs`, `iris_pipeline`) |
| `voice.model` | `types.VoiceMetaModel` | Speech/LLM model ID (e.g., `scribe_v2`) |
| `voice.language` | `types.VoiceMetaLanguage` | ISO-639 / BCP-47 language code |
| `voice.event_type` | `types.VoiceMetaEventType` | Iris event type (`user_transcript_committed`, `agent_text_committed`, `transcription`) |
| `voice.sequence` | `types.VoiceMetaSequence` | Non-negative per-session monotonic sequence |
| `voice.timestamp` | `types.VoiceMetaTimestamp` | RFC3339Nano UTC utterance timestamp |
| `voice.duration_ms` | `types.VoiceMetaDurationMS` | Optional audio duration in milliseconds |
| `voice.is_final` | `types.VoiceMetaFinal` | Always `"true"` for persisted records |

Use `types.ParseVoiceMetadata(msg.Metadata)` and `types.IsVoiceMessage(msg)` to
read structured `types.VoiceMetadata` from any retrieved `*types.Message`.

---

## 2. Privacy, Opt-In Storage, Retention & Deletion

### Opt-In by Default

Voice transcript storage is **disabled by default**:

- **Config (`~/.cortex/config.yaml`)**:
  ```yaml
  voice:
    transcript_storage_enabled: false # Set true to opt in
    transcript_retention_days: 30     # 0 disables voice-specific GC
  namespace:
    allowed_namespaces: ["support-voice"]
  ```
- **Go SDK (`pkg/cortex`)**:
  ```go
  client, err := cortex.Open(ctx,
      cortex.WithSQLite(dataDir),
      cortex.WithVoiceTranscriptStorage(true),
      cortex.WithVoiceTranscriptRetention(30),
      cortex.WithAllowedNamespaces("support-voice"),
  )
  ```

Attempting to persist voice transcripts without opting in returns
`cortex.ErrVoiceStorageDisabled` (and writes nothing).

### Committed-Only & No Raw Audio by Default

- Only finalized utterances (`IsFinal == true`) are persisted:
  - `core.VoiceEventUserTranscriptCommitted` (`IsFinal: true`)
  - `core.VoiceEventAgentTextCommitted` (`IsFinal: true`)
  - `*core.TranscriptionResponse` (`IsFinal: true`)
- Partial hypotheses (`VoiceEventUserTranscriptPartial`), streaming deltas
  (`VoiceEventAgentTextDelta`), and audio chunks (`VoiceEventAudioOutput`) are
  **never persisted** by `RecordVoiceEvent` or `VoiceSessionRecorder`.
- Explicit appends with `IsFinal: false` are rejected with
  `cortex.ErrUncommittedTranscript`.
- Raw audio metadata keys (`audio`, `raw_audio`, `audio_bytes`, `audio_base64`,
  `voice.audio*`, etc.) and inline `data:audio/...` data URIs are rejected with
  `cortex.ErrRawAudioDisallowed`.
- Structured logs emit correlation IDs and character lengths only
  (`namespace`, `thread_id`, `session_id`, `turn_id`, `role`, `source`,
  `content_len`) — never raw transcript text or audio bytes.

### Namespace Enforcement, Retention & Deletion

- Empty/whitespace namespaces are rejected (`ErrInvalidInput`).
- When `namespace.allowed_namespaces` / `WithAllowedNamespaces(...)` is set,
  reads, writes, and deletes outside the allowlist fail with
  `cortex.ErrNamespaceNotAllowed`.
- **Automatic GC**: `voice.transcript_retention_days > 0` deletes expired voice
  transcript messages (and empty threads) on each GC cycle without deleting
  non-voice conversation threads.
- **Explicit deletion**:
  - `client.Voice().DeleteSession(ctx, namespace, sessionID)` (and MCP tool
    `voice_session_delete`) deletes only messages matching `voice.session_id`
    within `namespace`, leaving other namespaces and non-voice messages intact.
  - `client.Voice().PurgeExpired(ctx, olderThan)` deletes expired voice
    transcripts on demand.

---

## 3. Retrieving Voice History for Iris Context Injection

```go
irisMsgs, hist, err := client.Voice().IrisMessages(ctx, namespace, threadID, &cortex.IrisContextOpts{
    SessionID:      sessionID,
    IncludeSummary: true,
    SystemPrompt:   "You are a concise voice assistant.",
    LastN:          20,
})
```

- Returns messages in strict chronological order (oldest first), scoped to
  `(namespace, threadID)` and optionally filtered by `SessionID` and `Roles`
  (defaulting to `["user", "assistant"]`).
- Lossless cursor pagination (`NextCursor` / `Cursor`) works reliably even when
  multiple turns occur within the same second.
- When `IncludeSummary: true` and the thread has a rolling summary, the summary
  is injected into the leading `core.RoleSystem` message alongside only the
  unsummarized recent turns.
- Alternatively, `client.Voice().NewIrisMemory(namespace, threadID, sessionID, opts)`
  implements Iris's `core.Memory` interface directly for `core.NewConversation`.

---

## 4. MCP Tools

Three additive, backwards-compatible MCP tools are exposed alongside the
existing `conversation_*` tools:

- `voice_transcript_append`: Persist a committed voice transcript (`is_final: true` required; enforces opt-in policy, namespace allowlist, and no raw audio).
- `voice_history`: Retrieve namespace-scoped committed voice history with ordering, pagination, and optional summary integration.
- `voice_session_delete`: Delete all persisted voice transcripts for `(namespace, session_id)`.

---

## 5. Failure, Interruption & Cancellation Semantics

- **Context cancellation**: `AppendTranscript`, `RecordVoiceEvent`,
  `RecordTranscription`, `History`, `IrisMessages`, `DeleteSession`, and
  `PurgeExpired` check `ctx.Err()` before mutating storage or advancing
  pagination loops. A cancelled context fails fast with `context.Canceled` /
  `context.DeadlineExceeded` and persists no partial state.
- **Barge-in / Interruption**: `VoiceSessionRecorder` maintains a
  `core.VoiceTurnTracker` guard; `recorder.ShouldPlayAudio(evt)` immediately
  returns `false` for stale audio chunks from interrupted turns, and interrupted
  assistant text that never reaches `VoiceEventAgentTextCommitted` is never
  persisted.
- **Idempotent replay**: Re-delivering the same committed `(session_id, turn_id, role, event_type/sequence)` returns the existing record without creating duplicates.
- **Storage errors**: Returned wrapped to the caller (never silently dropped);
  MCP handlers sanitize unexpected internal errors while returning actionable
  messages for policy/validation sentinels.

---

## 6. Runnable Example & Test Verification

- Sample conversational voice agent: [`examples/voice-agent`](../examples/voice-agent/README.md)
  ```bash
  go run -tags sqlite_fts5 ./examples/voice-agent
  ```
- Deterministic offline unit & integration tests (with race detector):
  ```bash
  go test -tags sqlite_fts5 -race ./pkg/... ./internal/conversation/... ./internal/server/... ./internal/storage/sqlite/...
  ```
