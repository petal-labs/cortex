package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/petal-labs/cortex/internal/config"
)

// IrisClient implements the Provider interface using the Iris embedding service.
type IrisClient struct {
	httpClient *http.Client
	endpoint   string
	provider   string
	model      string
	dimensions int
	batchSize  int
}

// Verify IrisClient implements Provider at compile time.
var _ Provider = (*IrisClient)(nil)

// NewIrisClient creates a new Iris embedding client.
func NewIrisClient(cfg *config.Config) (*IrisClient, error) {
	if cfg.Iris.Endpoint == "" {
		return nil, fmt.Errorf("iris endpoint is required")
	}

	return &IrisClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		endpoint:   cfg.Iris.Endpoint,
		provider:   cfg.Embedding.Provider,
		model:      cfg.Embedding.Model,
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

	// Filter out empty strings
	nonEmpty := make([]string, 0, len(texts))
	indices := make([]int, 0, len(texts))
	for i, text := range texts {
		if text != "" {
			nonEmpty = append(nonEmpty, text)
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
	req := EmbeddingRequest{
		Provider: c.provider,
		Model:    c.model,
		Input:    nonEmpty,
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send request to Iris
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/embeddings", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProviderFailed, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d: %s", ErrProviderFailed, resp.StatusCode, string(body))
	}

	var embeddingResp EmbeddingResponse
	if err := json.Unmarshal(body, &embeddingResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(embeddingResp.Embeddings) != len(nonEmpty) {
		return nil, fmt.Errorf("%w: expected %d embeddings, got %d", ErrProviderFailed, len(nonEmpty), len(embeddingResp.Embeddings))
	}

	// Map embeddings back to original indices, filling empty inputs with zero vectors
	result := make([][]float32, len(texts))
	for i := range result {
		result[i] = make([]float32, c.dimensions)
	}
	for i, idx := range indices {
		result[idx] = embeddingResp.Embeddings[i]
	}

	return result, nil
}

// Dimensions returns the configured embedding dimensions.
func (c *IrisClient) Dimensions() int {
	return c.dimensions
}

// Close releases resources. For HTTP client, this is a no-op.
func (c *IrisClient) Close() error {
	return nil
}
