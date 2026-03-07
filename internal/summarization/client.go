package summarization

import (
	"context"
	"fmt"
	"strings"

	"github.com/petal-labs/iris/core"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/llm"
	"github.com/petal-labs/cortex/pkg/types"
)

// SystemPrompt is the default prompt for conversation summarization.
const SystemPrompt = `You are a conversation summarizer. Create a concise summary that preserves:
- Key decisions and conclusions
- Important facts, numbers, and data points
- Action items and commitments
- User preferences and requirements
- Technical details relevant to ongoing work

Omit pleasantries, filler, and redundant exchanges. Write in present tense as a reference document, not a narrative.`

// Client provides LLM completion capabilities via the iris SDK.
type Client struct {
	client    *core.Client
	model     core.ModelID
	maxTokens int
}

// NewClient creates a new summarization client using the iris SDK.
func NewClient(cfg *config.Config) (*Client, error) {
	if cfg.Summarization.Provider == "" {
		return nil, fmt.Errorf("summarization provider is required")
	}

	provider, err := llm.NewProvider(cfg.Summarization.Provider)
	if err != nil {
		return nil, fmt.Errorf("failed to create summarization provider: %w", err)
	}

	client := llm.NewClient(provider)

	return &Client{
		client:    client,
		model:     core.ModelID(cfg.Summarization.Model),
		maxTokens: cfg.Summarization.MaxTokens,
	}, nil
}

// Complete sends a completion request using the iris SDK and returns the response.
func (c *Client) Complete(ctx context.Context, systemPrompt, userMessage string) (string, error) {
	builder := c.client.Chat(c.model).
		System(systemPrompt).
		User(userMessage)

	if c.maxTokens > 0 {
		builder = builder.MaxTokens(c.maxTokens)
	}

	resp, err := builder.GetResponse(ctx)
	if err != nil {
		return "", fmt.Errorf("completion request failed: %w", err)
	}

	return resp.Output, nil
}

// SummarizeMessages summarizes a list of conversation messages.
func (c *Client) SummarizeMessages(ctx context.Context, messages []*types.Message) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	formatted := formatMessages(messages)
	userMessage := fmt.Sprintf("Summarize this conversation:\n\n%s", formatted)

	return c.Complete(ctx, SystemPrompt, userMessage)
}

// formatMessages formats messages into a readable conversation transcript.
func formatMessages(messages []*types.Message) string {
	var sb strings.Builder

	for _, msg := range messages {
		// Format: [role]: content
		fmt.Fprintf(&sb, "[%s]: %s\n\n", msg.Role, msg.Content)
	}

	return strings.TrimSpace(sb.String())
}
