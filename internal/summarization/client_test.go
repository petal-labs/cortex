package summarization

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/pkg/types"
)

func TestComplete(t *testing.T) {
	expectedContent := "This is the summary of the conversation."

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}

		var req CompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}

		if req.Provider != "anthropic" {
			t.Errorf("expected provider anthropic, got %s", req.Provider)
		}
		if req.Model != "claude-sonnet-4-20250514" {
			t.Errorf("expected model claude-sonnet-4-20250514, got %s", req.Model)
		}
		if len(req.Messages) != 2 {
			t.Errorf("expected 2 messages, got %d", len(req.Messages))
		}
		if req.Messages[0].Role != "system" {
			t.Errorf("expected first message role system, got %s", req.Messages[0].Role)
		}
		if req.Messages[1].Role != "user" {
			t.Errorf("expected second message role user, got %s", req.Messages[1].Role)
		}

		resp := CompletionResponse{
			Content: expectedContent,
			Model:   req.Model,
			Usage: &Usage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &config.Config{
		Iris: config.IrisConfig{
			Endpoint: server.URL,
		},
		Summarization: config.SummarizationConfig{
			Provider:  "anthropic",
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 1024,
		},
	}

	client := NewClient(cfg)

	messages := []Message{
		{Role: "system", Content: "Test system prompt"},
		{Role: "user", Content: "Test user message"},
	}

	content, err := client.Complete(context.Background(), messages)
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	if content != expectedContent {
		t.Errorf("expected content %q, got %q", expectedContent, content)
	}
}

func TestSummarizeMessages(t *testing.T) {
	expectedSummary := "Summary: User asked about weather. Assistant provided forecast."

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req CompletionRequest
		json.NewDecoder(r.Body).Decode(&req)

		// Verify system prompt is the summarization prompt
		if req.Messages[0].Content != SystemPrompt {
			t.Errorf("unexpected system prompt")
		}

		// Verify user message contains formatted conversation
		userMsg := req.Messages[1].Content
		if userMsg == "" {
			t.Error("user message should not be empty")
		}

		resp := CompletionResponse{Content: expectedSummary}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &config.Config{
		Iris: config.IrisConfig{
			Endpoint: server.URL,
		},
		Summarization: config.SummarizationConfig{
			Provider:  "anthropic",
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 1024,
		},
	}

	client := NewClient(cfg)

	messages := []*types.Message{
		{
			ID:        "msg-1",
			ThreadID:  "thread-1",
			Namespace: "test",
			Role:      "user",
			Content:   "What's the weather like?",
			CreatedAt: time.Now(),
		},
		{
			ID:        "msg-2",
			ThreadID:  "thread-1",
			Namespace: "test",
			Role:      "assistant",
			Content:   "The weather is sunny with a high of 75°F.",
			CreatedAt: time.Now(),
		},
	}

	summary, err := client.SummarizeMessages(context.Background(), messages)
	if err != nil {
		t.Fatalf("SummarizeMessages failed: %v", err)
	}

	if summary != expectedSummary {
		t.Errorf("expected summary %q, got %q", expectedSummary, summary)
	}
}

func TestSummarizeMessagesEmpty(t *testing.T) {
	cfg := &config.Config{
		Iris: config.IrisConfig{
			Endpoint: "http://localhost:8787",
		},
		Summarization: config.SummarizationConfig{
			Provider:  "anthropic",
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 1024,
		},
	}

	client := NewClient(cfg)

	summary, err := client.SummarizeMessages(context.Background(), []*types.Message{})
	if err != nil {
		t.Fatalf("SummarizeMessages failed: %v", err)
	}

	if summary != "" {
		t.Errorf("expected empty summary for empty messages, got %q", summary)
	}
}

func TestCompleteError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		Iris: config.IrisConfig{
			Endpoint: server.URL,
		},
		Summarization: config.SummarizationConfig{
			Provider:  "anthropic",
			Model:     "claude-sonnet-4-20250514",
			MaxTokens: 1024,
		},
	}

	client := NewClient(cfg)

	_, err := client.Complete(context.Background(), []Message{
		{Role: "user", Content: "test"},
	})

	if err == nil {
		t.Fatal("expected error for failed request")
	}
}

func TestFormatMessages(t *testing.T) {
	messages := []*types.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
		{Role: "user", Content: "How are you?"},
	}

	formatted := formatMessages(messages)

	expected := "[user]: Hello\n\n[assistant]: Hi there!\n\n[user]: How are you?"
	if formatted != expected {
		t.Errorf("expected:\n%s\n\ngot:\n%s", expected, formatted)
	}
}
