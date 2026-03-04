package summarization

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/petal-labs/cortex/internal/config"
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

// Client provides LLM completion capabilities via Iris.
type Client struct {
	endpoint   string
	provider   string
	model      string
	maxTokens  int
	httpClient *http.Client
}

// Message represents a chat message for the LLM.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CompletionRequest is the request body for Iris completions endpoint.
type CompletionRequest struct {
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

// CompletionResponse is the response from Iris completions endpoint.
type CompletionResponse struct {
	Content string `json:"content"`
	Model   string `json:"model,omitempty"`
	Usage   *Usage `json:"usage,omitempty"`
}

// Usage contains token usage information.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// NewClient creates a new summarization client.
func NewClient(cfg *config.Config) *Client {
	return &Client{
		endpoint:  cfg.Iris.Endpoint,
		provider:  cfg.Summarization.Provider,
		model:     cfg.Summarization.Model,
		maxTokens: cfg.Summarization.MaxTokens,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Complete sends a completion request to Iris and returns the response.
func (c *Client) Complete(ctx context.Context, messages []Message) (string, error) {
	req := CompletionRequest{
		Provider:  c.provider,
		Model:     c.model,
		Messages:  messages,
		MaxTokens: c.maxTokens,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("completion request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var completionResp CompletionResponse
	if err := json.Unmarshal(respBody, &completionResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	return completionResp.Content, nil
}

// SummarizeMessages summarizes a list of conversation messages.
func (c *Client) SummarizeMessages(ctx context.Context, messages []*types.Message) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	formatted := formatMessages(messages)

	llmMessages := []Message{
		{Role: "system", Content: SystemPrompt},
		{Role: "user", Content: fmt.Sprintf("Summarize this conversation:\n\n%s", formatted)},
	}

	return c.Complete(ctx, llmMessages)
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
