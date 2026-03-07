package embedding

import (
	"context"
	"fmt"

	"github.com/petal-labs/iris/core"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/llm"
)

// IrisClient implements the Provider interface using the iris SDK.
type IrisClient struct {
	provider   core.EmbeddingProvider
	model      core.ModelID
	dimensions int
	batchSize  int
}

// Verify IrisClient implements Provider at compile time.
var _ Provider = (*IrisClient)(nil)

// NewIrisClient creates a new Iris embedding client using the iris SDK.
func NewIrisClient(cfg *config.Config) (*IrisClient, error) {
	if cfg.Embedding.Provider == "" {
		return nil, fmt.Errorf("embedding provider is required")
	}

	provider, err := llm.NewEmbeddingProvider(cfg.Embedding.Provider)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding provider: %w", err)
	}

	return &IrisClient{
		provider:   provider,
		model:      core.ModelID(cfg.Embedding.Model),
		dimensions: cfg.Embedding.Dimensions,
		batchSize:  cfg.Embedding.BatchSize,
	}, nil
}

// Embed generates an embedding for a single text input.
func (c *IrisClient) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, ErrEmptyInput
	}

	embeddings, err := c.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}

	if len(embeddings) == 0 {
		return nil, fmt.Errorf("%w: no embeddings returned", ErrProviderFailed)
	}

	return embeddings[0], nil
}

// EmbedBatch generates embeddings for multiple texts in a single call.
func (c *IrisClient) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	if len(texts) > c.batchSize {
		return nil, fmt.Errorf("%w: %d texts exceeds limit of %d", ErrBatchTooLarge, len(texts), c.batchSize)
	}

	// Filter out empty strings and track indices
	nonEmpty := make([]core.EmbeddingInput, 0, len(texts))
	indices := make([]int, 0, len(texts))
	for i, text := range texts {
		if text != "" {
			nonEmpty = append(nonEmpty, core.EmbeddingInput{Text: text})
			indices = append(indices, i)
		}
	}

	if len(nonEmpty) == 0 {
		// All inputs were empty, return zero vectors
		result := make([][]float32, len(texts))
		for i := range result {
			result[i] = make([]float32, c.dimensions)
		}
		return result, nil
	}

	// Create the embedding request
	req := &core.EmbeddingRequest{
		Model: c.model,
		Input: nonEmpty,
	}

	// Set dimensions if specified
	if c.dimensions > 0 {
		req.Dimensions = &c.dimensions
	}

	// Call the iris SDK
	resp, err := c.provider.CreateEmbeddings(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderFailed, err)
	}

	if len(resp.Vectors) != len(nonEmpty) {
		return nil, fmt.Errorf("%w: expected %d embeddings, got %d", ErrProviderFailed, len(nonEmpty), len(resp.Vectors))
	}

	// Map embeddings back to original indices, filling empty inputs with zero vectors
	result := make([][]float32, len(texts))
	dims := c.dimensions
	if dims == 0 && len(resp.Vectors) > 0 && len(resp.Vectors[0].Vector) > 0 {
		dims = len(resp.Vectors[0].Vector)
	}
	for i := range result {
		result[i] = make([]float32, dims)
	}
	for i, idx := range indices {
		if i < len(resp.Vectors) {
			result[idx] = resp.Vectors[i].Vector
		}
	}

	return result, nil
}

// Dimensions returns the configured embedding dimensions.
func (c *IrisClient) Dimensions() int {
	return c.dimensions
}

// Close releases resources. For iris SDK, this is a no-op.
func (c *IrisClient) Close() error {
	return nil
}
