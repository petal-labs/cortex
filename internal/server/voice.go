package server

import (
	"context"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/petal-labs/cortex/v2/internal/conversation"
	"github.com/petal-labs/cortex/v2/internal/server/dto"
	"github.com/petal-labs/cortex/v2/pkg/types"
)

// registerVoiceTools registers voice conversation memory MCP tools.
func (s *Server) registerVoiceTools() {
	// voice_transcript_append
	appendTool := mcp.NewTool("voice_transcript_append",
		mcp.WithDescription("Persist a committed voice transcript or assistant voice response with session/turn correlation metadata. Requires opt-in voice transcript storage and rejects partial transcripts and raw audio payloads."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("thread_id",
			mcp.Description("Conversation thread identifier (defaults to session_id when omitted)"),
		),
		mcp.WithString("session_id",
			mcp.Required(),
			mcp.Description("Stable voice session correlation identifier"),
		),
		mcp.WithString("turn_id",
			mcp.Description("Voice turn correlation identifier"),
		),
		mcp.WithString("role",
			mcp.Required(),
			mcp.Description("Speaker role"),
			mcp.Enum("user", "assistant", "system", "tool"),
		),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("Committed transcript or assistant response text"),
		),
		mcp.WithBoolean("is_final",
			mcp.Required(),
			mcp.Description("Must be true; partial uncommitted transcripts are rejected"),
		),
		mcp.WithString("source",
			mcp.Description("Source attribution (e.g., stt, transcription, agent, voice_session)"),
		),
		mcp.WithString("provider",
			mcp.Description("Provider attribution (e.g., elevenlabs, iris_pipeline)"),
		),
		mcp.WithString("model",
			mcp.Description("Model attribution (e.g., scribe_v2)"),
		),
		mcp.WithString("language",
			mcp.Description("ISO-639 / BCP-47 language code"),
		),
		mcp.WithString("speaker_id",
			mcp.Description("Optional diarized speaker identifier"),
		),
		mcp.WithString("event_type",
			mcp.Description("Iris voice event type (e.g., user_transcript_committed, agent_text_committed)"),
		),
		mcp.WithNumber("sequence",
			mcp.Description("Optional non-negative per-session monotonic event sequence"),
		),
		mcp.WithNumber("duration_ms",
			mcp.Description("Optional utterance audio duration in milliseconds"),
		),
		mcp.WithString("timestamp",
			mcp.Description("Optional RFC3339/RFC3339Nano UTC utterance timestamp"),
		),
		mcp.WithObject("metadata",
			mcp.Description("Optional extra string key-value metadata (raw audio keys are rejected)"),
		),
		mcp.WithNumber("max_content_length",
			mcp.Description("Optional maximum content length before truncation"),
		),
		mcp.WithOutputSchema[dto.VoiceTranscriptAppend](),
	)
	s.mcp.AddTool(appendTool, s.handleVoiceTranscriptAppend)

	// voice_history
	historyTool := mcp.NewTool("voice_history",
		mcp.WithDescription("Retrieve namespace-scoped committed voice transcript and assistant-text history in chronological order with pagination and optional summary integration."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("thread_id",
			mcp.Description("Conversation thread identifier (defaults to session_id when omitted)"),
		),
		mcp.WithString("session_id",
			mcp.Description("Optional voice session identifier filter"),
		),
		mcp.WithNumber("last_n",
			mcp.Description("Maximum number of messages to return (default: 20)"),
		),
		mcp.WithBoolean("include_summary",
			mcp.Description("Include thread summary and return unsummarized messages alongside it (default: false)"),
		),
		mcp.WithBoolean("include_non_voice",
			mcp.Description("Include standard non-voice messages from the same thread (default: false)"),
		),
		mcp.WithString("cursor",
			mcp.Description("Pagination cursor from a previous voice_history response"),
		),
		mcp.WithOutputSchema[dto.VoiceHistory](),
	)
	s.mcp.AddTool(historyTool, s.handleVoiceHistory)

	// voice_session_delete
	deleteTool := mcp.NewTool("voice_session_delete",
		mcp.WithDescription("Delete all persisted voice transcript messages belonging to a session within a namespace."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("session_id",
			mcp.Required(),
			mcp.Description("Voice session identifier to delete"),
		),
		mcp.WithOutputSchema[dto.VoiceSessionDelete](),
	)
	s.mcp.AddTool(deleteTool, s.handleVoiceSessionDelete)
}

func (s *Server) handleVoiceTranscriptAppend(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sessionID, err := req.RequireString("session_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	role, err := req.RequireString("role")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	content, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	isFinal, err := req.RequireBool("is_final")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	args := req.GetArguments()
	threadID, errResult := optString(args, "thread_id", "")
	if errResult != nil {
		return errResult, nil
	}
	turnID, errResult := optString(args, "turn_id", "")
	if errResult != nil {
		return errResult, nil
	}
	source, errResult := optString(args, "source", "")
	if errResult != nil {
		return errResult, nil
	}
	provider, errResult := optString(args, "provider", "")
	if errResult != nil {
		return errResult, nil
	}
	model, errResult := optString(args, "model", "")
	if errResult != nil {
		return errResult, nil
	}
	language, errResult := optString(args, "language", "")
	if errResult != nil {
		return errResult, nil
	}
	speakerID, errResult := optString(args, "speaker_id", "")
	if errResult != nil {
		return errResult, nil
	}
	eventType, errResult := optString(args, "event_type", "")
	if errResult != nil {
		return errResult, nil
	}
	seq, errResult := optInt(args, "sequence", 0)
	if errResult != nil {
		return errResult, nil
	}
	durationMS, errResult := optInt(args, "duration_ms", 0)
	if errResult != nil {
		return errResult, nil
	}
	rawTimestamp, errResult := optString(args, "timestamp", "")
	if errResult != nil {
		return errResult, nil
	}

	var ts time.Time
	if strings.TrimSpace(rawTimestamp) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(rawTimestamp))
		if err != nil {
			return mcp.NewToolResultError("parameter \"timestamp\" must be RFC3339 or RFC3339Nano"), nil
		}
		ts = parsed.UTC()
	}

	opts := &conversation.VoiceAppendOpts{}
	if metadata, ok := args["metadata"].(map[string]any); ok {
		opts.Metadata, errResult = toStringMap(metadata, "metadata")
		if errResult != nil {
			return errResult, nil
		}
	}
	maxLen, errResult := optInt(args, "max_content_length", 0)
	if errResult != nil {
		return errResult, nil
	}
	opts.MaxContentLength = maxLen

	record := &types.VoiceTranscriptRecord{
		Role:    role,
		Content: content,
		Metadata: types.VoiceMetadata{
			SessionID:   sessionID,
			TurnID:      turnID,
			SpeakerRole: role,
			SpeakerID:   speakerID,
			Source:      source,
			Provider:    provider,
			Model:       model,
			Language:    language,
			EventType:   eventType,
			Sequence:    int64(seq),
			Timestamp:   ts,
			Duration:    time.Duration(durationMS) * time.Millisecond,
			IsFinal:     isFinal,
		},
	}

	msg, err := s.conversation.AppendVoiceTranscript(ctx, namespace, threadID, record, opts)
	if err != nil {
		return toolError(err)
	}

	return structuredResult(dto.NewVoiceTranscriptAppend(msg))
}

func (s *Server) handleVoiceHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	args := req.GetArguments()
	threadID, errResult := optString(args, "thread_id", "")
	if errResult != nil {
		return errResult, nil
	}
	sessionID, errResult := optString(args, "session_id", "")
	if errResult != nil {
		return errResult, nil
	}
	lastN, errResult := optInt(args, "last_n", 0)
	if errResult != nil {
		return errResult, nil
	}
	includeSummary, errResult := optBool(args, "include_summary", false)
	if errResult != nil {
		return errResult, nil
	}
	includeNonVoice, errResult := optBool(args, "include_non_voice", false)
	if errResult != nil {
		return errResult, nil
	}
	cursor, errResult := optString(args, "cursor", "")
	if errResult != nil {
		return errResult, nil
	}

	result, err := s.conversation.VoiceHistory(ctx, namespace, threadID, &conversation.VoiceHistoryOpts{
		SessionID:       sessionID,
		LastN:           lastN,
		IncludeSummary:  includeSummary,
		IncludeNonVoice: includeNonVoice,
		Cursor:          cursor,
	})
	if err != nil {
		return toolError(err)
	}

	return structuredResult(dto.NewVoiceHistory(result))
}

func (s *Server) handleVoiceSessionDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sessionID, err := req.RequireString("session_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	deleted, err := s.conversation.DeleteVoiceSession(ctx, namespace, sessionID)
	if err != nil {
		return toolError(err)
	}

	return structuredResult(dto.NewVoiceSessionDelete(sessionID, deleted))
}
