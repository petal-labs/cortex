package embedding

import (
	"context"
	"fmt"
	"time"

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
	timeout    time.Duration
	retry      core.RetryPolicy
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
		timeout:    cfg.Embedding.Timeout,
		retry:      core.DefaultRetryPolicy(),
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

// EmbedBatch generates embeddings for multiple texts, automatically
// splitting inputs that exceed the configured batch size into sub-batches
// and concatenating the results. A document that produces more chunks than
// batch_size no longer fails the whole embed — it is processed in
// batch_size-sized provider calls.
func (c *IrisClient) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	batchSize := c.batchSize
	if batchSize <= 0 {
		batchSize = len(texts) // no configured limit: single call
	}

	// Cortex calls the embedding provider directly rather than through a
	// core.Client, so iris's own execution timeout does not apply here. Impose
	// our own deadline so a hung provider call fails fast with a legible
	// context.DeadlineExceeded instead of blocking until the caller cancels.
	// Only applied when the caller supplied no deadline of its own, so a tighter
	// caller budget still wins; timeout <= 0 disables it (unbounded). Applied
	// once around all sub-batches so the total is bounded by a single
	// deadline rather than one per sub-batch.
	if c.timeout > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.timeout)
			defer cancel()
		}
	}

	if len(texts) <= batchSize {
		return c.embedSingleBatch(ctx, texts)
	}

	result := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		part, err := c.embedSingleBatch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		result = append(result, part...)
	}
	return result, nil
}

// embedSingleBatch sends one provider request for texts (which the caller
// has already capped at the batch size) and maps the response back to the
// input positions.
func (c *IrisClient) embedSingleBatch(ctx context.Context, texts []string) ([][]float32, error) {
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

	// Call the iris SDK with retry on transient failures.
	// Cortex calls the embedding provider directly rather than through a
	// core.Client, so iris's own retry logic does not apply here. We replicate
	// it using the same RetryPolicy the core.Client uses, honoring context
	// cancellation between attempts.
	retry := c.retry
	if retry == nil {
		retry = core.DefaultRetryPolicy()
	}
	var resp *core.EmbeddingResponse
	var err error
retryLoop:
	for attempt := 0; ; attempt++ {
		resp, err = c.provider.CreateEmbeddings(ctx, req)
		if err == nil {
			break
		}
		delay, shouldRetry := retry.NextDelay(attempt, err)
		if !shouldRetry {
			break
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			break retryLoop
		}
	}
	if err != nil {
		// Wrap both sentinels so callers can distinguish a timeout
		// (errors.Is(err, context.DeadlineExceeded)) from other provider
		// failures while still matching ErrProviderFailed. Surface the
		// typed classification (kind, status, code, request_id) for
		// alerting — e.g. a 401 means stop and fix configuration, a 429
		// means back off.
		llm.LogProviderError(ctx, "embedding", err)
		return nil, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}

	if len(resp.Vectors) != len(nonEmpty) {
		return nil, fmt.Errorf("%w: expected %d embeddings, got %d", ErrProviderFailed, len(nonEmpty), len(resp.Vectors))
	}

	// Validate per-vector dimensions against the configured value. A
	// wrong-model response (e.g. 768-dim vectors against a 1536-dim schema)
	// would otherwise surface as an opaque downstream insert failure or
	// silent index corruption.
	for i, v := range resp.Vectors {
		if c.dimensions > 0 {
			if len(v.Vector) != c.dimensions {
				return nil, fmt.Errorf("%w: embedding %d has %d dimensions, expected %d (check embedding model and dimensions config)",
					ErrProviderFailed, i, len(v.Vector), c.dimensions)
			}
		} else if len(v.Vector) != len(resp.Vectors[0].Vector) {
			// dimensions unset: infer from the first vector, but reject
			// inconsistent lengths so we never return ragged output.
			return nil, fmt.Errorf("%w: inconsistent embedding dimensions: vector %d has %d dimensions, vector 0 has %d",
				ErrProviderFailed, i, len(v.Vector), len(resp.Vectors[0].Vector))
		}
	}

	// Map embeddings back to original indices, filling empty inputs with
	// zero vectors. Each vector is placed using its .Index field (iris
	// exposes it precisely because response order is not guaranteed) rather
	// than its position in the response slice, with bounds and duplicate
	// validation so an out-of-order or malformed response can never attach
	// a vector to the wrong text.
	result := make([][]float32, len(texts))
	dims := c.dimensions
	if dims == 0 && len(resp.Vectors) > 0 && len(resp.Vectors[0].Vector) > 0 {
		dims = len(resp.Vectors[0].Vector)
	}
	for i := range result {
		result[i] = make([]float32, dims)
	}
	seen := make([]bool, len(indices))
	for pos, v := range resp.Vectors {
		if v.Index < 0 || v.Index >= len(indices) {
			return nil, fmt.Errorf("%w: embedding at response position %d has index %d out of range [0, %d)",
				ErrProviderFailed, pos, v.Index, len(indices))
		}
		if seen[v.Index] {
			return nil, fmt.Errorf("%w: embedding at response position %d duplicates index %d",
				ErrProviderFailed, pos, v.Index)
		}
		seen[v.Index] = true
		result[indices[v.Index]] = v.Vector
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
