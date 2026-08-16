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
