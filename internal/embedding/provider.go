package embedding

import (
	"context"
	"errors"
)

// Common errors returned by embedding providers.
var (
	ErrEmptyInput     = errors.New("empty input text")
	ErrBatchTooLarge  = errors.New("batch size exceeds limit")
	ErrProviderFailed = errors.New("embedding provider failed")
)

// Provider defines the interface for embedding generation.
// Implementations may call external services (Iris, OpenAI) or provide local embeddings.
type Provider interface {
	// Embed generates an embedding vector for a single text input.
	// Returns a float32 slice of the configured dimensions.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch generates embeddings for multiple texts in a single call.
	// This is more efficient than calling Embed repeatedly for bulk operations.
	// Implementations should respect configured batch size limits.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the embedding vector dimensions.
	Dimensions() int

	// Close releases any resources held by the provider.
	Close() error
}

// EmbeddingRequest represents a request to generate embeddings.
type EmbeddingRequest struct {
	Provider string   `json:"provider"` // e.g., "openai", "anthropic", "cohere"
	Model    string   `json:"model"`    // e.g., "text-embedding-3-small"
	Input    []string `json:"input"`    // Text inputs to embed
}

// EmbeddingResponse represents the response from an embedding request.
type EmbeddingResponse struct {
	Embeddings [][]float32 `json:"embeddings"` // One embedding per input
	Model      string      `json:"model"`      // Model used
	Usage      UsageInfo   `json:"usage"`      // Token usage information
}

// UsageInfo contains token usage information for billing/monitoring.
type UsageInfo struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}
