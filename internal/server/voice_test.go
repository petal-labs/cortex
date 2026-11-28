package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/petal-labs/cortex/v2/internal/config"
	"github.com/petal-labs/cortex/v2/internal/server/dto"
	"github.com/petal-labs/cortex/v2/pkg/types"
)

func callVoiceTool(t *testing.T, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	})
	if err != nil {
		t.Fatalf("handler returned unexpected error: %v", err)
	}
	return res
}

func toolResultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		return ""
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

func TestVoiceMCPTools(t *testing.T) {
	t.Run("disabled by default until opted in", func(t *testing.T) {
		srv := testServer(t, "voice-ns")
		res := callVoiceTool(t, srv.handleVoiceTranscriptAppend, map[string]any{
			"namespace":  "voice-ns",
			"thread_id":  "t-1",
			"session_id": "sess-1",
			"turn_id":    "turn-1",
			"role":       "user",
			"content":    "hello",
			"is_final":   true,
		})
		if !res.IsError {
			t.Fatalf("expected error when voice storage is disabled, got: %s", toolResultText(t, res))
		}
		if !strings.Contains(toolResultText(t, res), "voice transcript storage is disabled") {
			t.Errorf("unexpected error message: %s", toolResultText(t, res))
		}
	})

	t.Run("committed append, partial/audio rejection, history, and session delete", func(t *testing.T) {
		srv := testServer(t, "voice-ns")
		srv.conversation.SetVoiceConfig(&config.VoiceConfig{TranscriptStorageEnabled: true})
		srv.conversation.SetAllowedNamespaces([]string{"voice-ns"})

		// Disallowed namespace rejected.
		badNS := callVoiceTool(t, srv.handleVoiceTranscriptAppend, map[string]any{
			"namespace":  "other-ns",
			"session_id": "sess-1",
			"role":       "user",
			"content":    "hello",
			"is_final":   true,
		})
		if !badNS.IsError {
			t.Fatal("expected error for disallowed namespace")
		}

		// Partial (is_final=false) rejected.
		partial := callVoiceTool(t, srv.handleVoiceTranscriptAppend, map[string]any{
			"namespace":  "voice-ns",
			"thread_id":  "t-voice",
			"session_id": "sess-1",
			"turn_id":    "turn-1",
			"role":       "user",
			"content":    "hel...",
			"is_final":   false,
		})
		if !partial.IsError {
			t.Fatal("expected error for partial transcript")
		}

		// Raw audio metadata rejected.
		rawAudio := callVoiceTool(t, srv.handleVoiceTranscriptAppend, map[string]any{
			"namespace":  "voice-ns",
			"thread_id":  "t-voice",
			"session_id": "sess-1",
			"turn_id":    "turn-1",
			"role":       "user",
			"content":    "hello",
			"is_final":   true,
			"metadata":   map[string]any{"raw_audio": "AAAA"},
		})
		if !rawAudio.IsError {
			t.Fatal("expected error for raw audio metadata")
		}

		// Committed user transcript + assistant reply succeed.
		app1 := callVoiceTool(t, srv.handleVoiceTranscriptAppend, map[string]any{
			"namespace":  "voice-ns",
			"thread_id":  "t-voice",
			"session_id": "sess-1",
			"turn_id":    "turn-1",
			"role":       "user",
			"content":    "Book a meeting tomorrow",
			"is_final":   true,
			"source":     types.VoiceSourceSTT,
			"provider":   "elevenlabs",
			"model":      "scribe_v2",
			"language":   "en",
			"sequence":   float64(1),
		})
		if app1.IsError {
			t.Fatalf("expected append 1 to succeed, got: %s", toolResultText(t, app1))
		}
		var appendResp dto.VoiceTranscriptAppend
		if err := json.Unmarshal([]byte(toolResultText(t, app1)), &appendResp); err != nil {
			t.Fatalf("unmarshal append response: %v", err)
		}
		if appendResp.SchemaVersion != dto.SchemaVersion {
			t.Errorf("schema_version = %d, want %d", appendResp.SchemaVersion, dto.SchemaVersion)
		}
		if appendResp.Metadata[types.VoiceMetaSessionID] != "sess-1" || appendResp.Metadata[types.VoiceMetaTurnID] != "turn-1" {
			t.Errorf("missing voice correlation metadata: %+v", appendResp.Metadata)
		}

		app2 := callVoiceTool(t, srv.handleVoiceTranscriptAppend, map[string]any{
			"namespace":  "voice-ns",
			"thread_id":  "t-voice",
			"session_id": "sess-1",
			"turn_id":    "turn-1",
			"role":       "assistant",
			"content":    "Sure, what time works best?",
			"is_final":   true,
			"source":     types.VoiceSourceAgent,
			"sequence":   float64(2),
		})
		if app2.IsError {
			t.Fatalf("expected append 2 to succeed, got: %s", toolResultText(t, app2))
		}

		// Fetch voice history.
		histRes := callVoiceTool(t, srv.handleVoiceHistory, map[string]any{
			"namespace":  "voice-ns",
			"thread_id":  "t-voice",
			"session_id": "sess-1",
			"last_n":     float64(10),
		})
		if histRes.IsError {
			t.Fatalf("voice_history failed: %s", toolResultText(t, histRes))
		}
		var histDTO dto.VoiceHistory
		if err := json.Unmarshal([]byte(toolResultText(t, histRes)), &histDTO); err != nil {
			t.Fatalf("unmarshal history: %v", err)
		}
		if len(histDTO.Messages) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(histDTO.Messages))
		}
		if histDTO.Messages[0].Role != "user" || histDTO.Messages[1].Role != "assistant" {
			t.Errorf("unexpected chronological ordering: %+v", histDTO.Messages)
		}

		// Delete voice session.
		delRes := callVoiceTool(t, srv.handleVoiceSessionDelete, map[string]any{
			"namespace":  "voice-ns",
			"session_id": "sess-1",
		})
		if delRes.IsError {
			t.Fatalf("voice_session_delete failed: %s", toolResultText(t, delRes))
		}
		var delDTO dto.VoiceSessionDelete
		if err := json.Unmarshal([]byte(toolResultText(t, delRes)), &delDTO); err != nil {
			t.Fatalf("unmarshal delete: %v", err)
		}
		if delDTO.DeletedCount != 2 {
			t.Errorf("expected deleted_count=2, got %d", delDTO.DeletedCount)
		}

		// History should now be empty.
		histAfter := callVoiceTool(t, srv.handleVoiceHistory, map[string]any{
			"namespace":  "voice-ns",
			"thread_id":  "t-voice",
			"session_id": "sess-1",
		})
		var histAfterDTO dto.VoiceHistory
		if err := json.Unmarshal([]byte(toolResultText(t, histAfter)), &histAfterDTO); err != nil {
			t.Fatalf("unmarshal history after delete: %v", err)
		}
		if len(histAfterDTO.Messages) != 0 {
			t.Errorf("expected 0 messages after session delete, got %d", len(histAfterDTO.Messages))
		}
	})
}
