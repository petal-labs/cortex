package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	lru "github.com/hashicorp/golang-lru/v2"
)

// CachedProvider wraps a Provider with an LRU cache for embeddings.
// This reduces redundant API calls for repeated queries.
type CachedProvider struct {
	provider  Provider
	cache     *lru.Cache[string, []float32]
	cacheSize int
}

// Verify CachedProvider implements Provider at compile time.
var _ Provider = (*CachedProvider)(nil)

// NewCachedProvider creates a new cached embedding provider.
// cacheSize specifies the maximum number of embeddings to cache.
func NewCachedProvider(provider Provider, cacheSize int) (*CachedProvider, error) {
	if cacheSize <= 0 {
		cacheSize = 1000 // Default cache size
	}

	cache, err := lru.New[string, []float32](cacheSize)
	if err != nil {
		return nil, err
	}

	return &CachedProvider{
		provider:  provider,
		cache:     cache,
		cacheSize: cacheSize,
	}, nil
}

// Embed generates an embedding, using the cache if available.
func (c *CachedProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, ErrEmptyInput
	}

	key := hashText(text)

	// Check cache first
	if cached, ok := c.cache.Get(key); ok {
		// Return a copy to prevent mutation
		return copyEmbedding(cached), nil
	}

	// Cache miss - call the underlying provider
	embedding, err := c.provider.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	// Cache the result
	c.cache.Add(key, copyEmbedding(embedding))

	return embedding, nil
}

// EmbedBatch generates embeddings for multiple texts, using cache where available.
// Non-cached texts are fetched from the underlying provider in a single batch.
func (c *CachedProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	results := make([][]float32, len(texts))
	uncached := make([]string, 0)
	uncachedIndices := make([]int, 0)

	// Check cache for each text
	for i, text := range texts {
		if text == "" {
			results[i] = make([]float32, c.provider.Dimensions())
			continue
		}

		key := hashText(text)
		if cached, ok := c.cache.Get(key); ok {
			results[i] = copyEmbedding(cached)
		} else {
			uncached = append(uncached, text)
			uncachedIndices = append(uncachedIndices, i)
		}
	}

	// If all were cached, return early
	if len(uncached) == 0 {
		return results, nil
	}

	// Fetch uncached embeddings from provider
	embeddings, err := c.provider.EmbedBatch(ctx, uncached)
	if err != nil {
		return nil, err
	}

	// Store in results and cache
	for i, idx := range uncachedIndices {
		results[idx] = embeddings[i]
		key := hashText(uncached[i])
		c.cache.Add(key, copyEmbedding(embeddings[i]))
	}

	return results, nil
}

// Dimensions returns the embedding dimensions from the underlying provider.
func (c *CachedProvider) Dimensions() int {
	return c.provider.Dimensions()
}

// Close closes the underlying provider.
func (c *CachedProvider) Close() error {
	c.cache.Purge()
	return c.provider.Close()
}

// CacheStats returns cache statistics.
func (c *CachedProvider) CacheStats() (size int, capacity int) {
	return c.cache.Len(), c.cacheSize
}

// ClearCache removes all entries from the cache.
func (c *CachedProvider) ClearCache() {
	c.cache.Purge()
}

// hashText creates a cache key from the input text using SHA-256.
func hashText(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// copyEmbedding creates a copy of an embedding slice.
// This prevents cache mutations if the caller modifies the returned slice.
func copyEmbedding(embedding []float32) []float32 {
	result := make([]float32, len(embedding))
	copy(result, embedding)
	return result
}
