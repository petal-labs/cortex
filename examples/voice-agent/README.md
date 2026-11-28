# Voice Agent Example (Iris v2 + Cortex Voice Memory)

This example demonstrates a complete conversational voice agent loop connecting
**Iris v2** (`github.com/petal-labs/iris/v2/voice`) with **Cortex Voice Memory**
(`github.com/petal-labs/cortex/v2/pkg/cortex`):

1. **Opt-in transcript storage** (`cortex.WithVoiceTranscriptStorage(true)`) with
   a configured retention window (`cortex.WithVoiceTranscriptRetention(30)`) and
   strict namespace enforcement (`cortex.WithAllowedNamespaces(...)`).
2. **Committed-only persistence** via `client.Voice().NewSessionRecorder(...)`:
   interim partial transcripts, assistant text deltas, and raw audio chunks are
   never persisted or logged.
3. **Session & turn correlation metadata** (`voice.session_id`, `voice.turn_id`,
   `voice.speaker_role`, `voice.source`, `voice.provider`, `voice.model`,
   `voice.language`, `voice.timestamp`, `voice.duration_ms`, `voice.is_final`).
4. **History & summary context injection** into Iris via
   `client.Voice().IrisMessages(...)`.
5. **Explicit privacy deletion** via `client.Voice().DeleteSession(...)`.

## Run (Offline Deterministic Demo)

From the repository root:

```bash
go run -tags sqlite_fts5 ./examples/voice-agent
```

No API keys or network access are required.

## Connecting Live ElevenLabs + LLM Providers

Swap the demo doubles in `main.go` with live Iris v2 providers:

```go
el := elevenlabs.New(os.Getenv("ELEVENLABS_API_KEY"))
llm := openai.New(os.Getenv("OPENAI_API_KEY"))

pipeline, err := voice.NewPipeline(voice.PipelineConfig{
    STT:         el,
    STTModel:    elevenlabs.ModelScribeV2Realtime,
    LLM:         llm,
    LLMModel:    "gpt-4o-mini",
    RealtimeTTS: el,
    TTSModel:    elevenlabs.ModelElevenFlashV25,
    VoiceID:     os.Getenv("ELEVENLABS_VOICE_ID"),
})
```

See [docs/voice-memory.md](../../docs/voice-memory.md) for the full architecture,
privacy controls, and failure/cancellation semantics.
