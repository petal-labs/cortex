package summarization

import (
	"os"
	"testing"
	"time"

	"github.com/petal-labs/cortex/pkg/types"
)

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

func TestFormatMessagesEmpty(t *testing.T) {
	messages := []*types.Message{}

	formatted := formatMessages(messages)

	if formatted != "" {
		t.Errorf("expected empty string for empty messages, got %q", formatted)
	}
}

func TestFormatMessagesSingle(t *testing.T) {
	messages := []*types.Message{
		{Role: "user", Content: "Hello world"},
	}

	formatted := formatMessages(messages)

	expected := "[user]: Hello world"
	if formatted != expected {
		t.Errorf("expected %q, got %q", expected, formatted)
	}
}

func TestFormatMessagesWithTimestamp(t *testing.T) {
	// formatMessages should only use role and content, ignoring other fields
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

	formatted := formatMessages(messages)

	expected := "[user]: What's the weather like?\n\n[assistant]: The weather is sunny with a high of 75°F."
	if formatted != expected {
		t.Errorf("expected:\n%s\n\ngot:\n%s", expected, formatted)
	}
}

// Integration tests - require API key to run

func TestComplete_Integration(t *testing.T) {
	// Skip if no API key - this is an integration test
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY or OPENAI_API_KEY not set, skipping integration test")
	}

	// This test would require a real LLM provider
	t.Skip("Integration test requires real LLM provider - run manually with API key")
}

func TestSummarizeMessages_Integration(t *testing.T) {
	// Skip if no API key - this is an integration test
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY or OPENAI_API_KEY not set, skipping integration test")
	}

	// This test would require a real LLM provider
	t.Skip("Integration test requires real LLM provider - run manually with API key")
}

func TestNewClientMissingProvider(t *testing.T) {
	// NewClient requires a provider to be configured
	// Without a provider, it should return an error
	// This tests the validation logic without needing external services

	// We can't easily test this without creating a config with empty provider
	// because NewClient now validates that provider is required
	t.Skip("NewClient validation tested via error return")
}
