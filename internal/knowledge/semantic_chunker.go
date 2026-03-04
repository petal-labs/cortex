package knowledge

import (
	"context"
	"math"
	"strings"

	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/pkg/types"
)

// DefaultSimilarityThreshold is the cosine similarity threshold for detecting breakpoints.
// When similarity between adjacent windows drops below this, a new chunk boundary is created.
const DefaultSimilarityThreshold = 0.5

// DefaultWindowSize is the number of sentences to include in each embedding window.
const DefaultWindowSize = 3

// SemanticChunker uses embedding similarity to find natural topic boundaries.
type SemanticChunker struct {
	embedding           embedding.Provider
	similarityThreshold float64
	windowSize          int
	fallbackChunker     *DefaultChunker
}

// SemanticChunkerOption configures the semantic chunker.
type SemanticChunkerOption func(*SemanticChunker)

// WithSimilarityThreshold sets the similarity threshold for breakpoint detection.
func WithSimilarityThreshold(threshold float64) SemanticChunkerOption {
	return func(c *SemanticChunker) {
		c.similarityThreshold = threshold
	}
}

// WithWindowSize sets the number of sentences per embedding window.
func WithWindowSize(size int) SemanticChunkerOption {
	return func(c *SemanticChunker) {
		c.windowSize = size
	}
}

// NewSemanticChunker creates a semantic chunker with the given embedding provider.
func NewSemanticChunker(emb embedding.Provider, opts ...SemanticChunkerOption) *SemanticChunker {
	c := &SemanticChunker{
		embedding:           emb,
		similarityThreshold: DefaultSimilarityThreshold,
		windowSize:          DefaultWindowSize,
		fallbackChunker:     NewChunker(),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Chunk splits content into semantically coherent chunks.
// It embeds sliding windows of sentences and identifies breakpoints where
// the cosine similarity between adjacent windows drops below the threshold.
func (c *SemanticChunker) Chunk(ctx context.Context, content string, cfg types.ChunkConfig) ([]ChunkOutput, error) {
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 512
	}

	// Split into sentences
	sentences := splitSentences(content)
	if len(sentences) == 0 {
		return nil, nil
	}

	// If too few sentences for semantic chunking, fall back to sentence chunking
	if len(sentences) < c.windowSize*2 {
		return c.fallbackChunker.chunkBySentence(content, cfg), nil
	}

	// Create sliding windows of sentences
	windows := c.createWindows(sentences)
	if len(windows) < 2 {
		return c.fallbackChunker.chunkBySentence(content, cfg), nil
	}

	// Embed all windows
	windowTexts := make([]string, len(windows))
	for i, w := range windows {
		windowTexts[i] = strings.Join(w, " ")
	}

	embeddings, err := c.embedding.EmbedBatch(ctx, windowTexts)
	if err != nil {
		// Fall back to sentence chunking on embedding failure
		return c.fallbackChunker.chunkBySentence(content, cfg), nil
	}

	// Find breakpoints where similarity drops
	breakpoints := c.findBreakpoints(embeddings)

	// Convert breakpoints to sentence indices
	// Each breakpoint at window index i corresponds to a break after sentence (i + windowSize - 1)
	sentenceBreakpoints := make([]int, len(breakpoints))
	for i, bp := range breakpoints {
		sentenceBreakpoints[i] = bp + c.windowSize - 1
	}

	// Group sentences into chunks based on breakpoints
	chunks := c.groupSentences(sentences, sentenceBreakpoints, maxTokens, cfg)

	return chunks, nil
}

// createWindows creates overlapping windows of sentences for embedding.
func (c *SemanticChunker) createWindows(sentences []string) [][]string {
	if len(sentences) < c.windowSize {
		return [][]string{sentences}
	}

	var windows [][]string
	for i := 0; i <= len(sentences)-c.windowSize; i++ {
		window := make([]string, c.windowSize)
		copy(window, sentences[i:i+c.windowSize])
		windows = append(windows, window)
	}

	return windows
}

// findBreakpoints identifies indices where similarity drops below threshold.
func (c *SemanticChunker) findBreakpoints(embeddings [][]float32) []int {
	if len(embeddings) < 2 {
		return nil
	}

	var breakpoints []int

	for i := 0; i < len(embeddings)-1; i++ {
		similarity := cosineSimilarity(embeddings[i], embeddings[i+1])
		if similarity < c.similarityThreshold {
			breakpoints = append(breakpoints, i+1) // Break before window i+1
		}
	}

	return breakpoints
}

// groupSentences groups sentences into chunks based on breakpoints, respecting maxTokens.
func (c *SemanticChunker) groupSentences(sentences []string, breakpoints []int, maxTokens int, cfg types.ChunkConfig) []ChunkOutput {
	var chunks []ChunkOutput
	var currentChunk []string
	currentTokens := 0

	breakpointSet := make(map[int]bool)
	for _, bp := range breakpoints {
		breakpointSet[bp] = true
	}

	for i, sentence := range sentences {
		sentenceTokens := countTokens(sentence)

		// Check if we should break here (semantic boundary)
		isBreakpoint := breakpointSet[i]

		// If single sentence exceeds max, use fixed chunking for it
		if sentenceTokens > maxTokens {
			// Flush current chunk first
			if len(currentChunk) > 0 {
				chunks = append(chunks, ChunkOutput{
					Content:    strings.Join(currentChunk, " "),
					Index:      len(chunks),
					TokenCount: currentTokens,
				})
				currentChunk = nil
				currentTokens = 0
			}

			// Split the long sentence using fixed chunking
			fixedChunks := c.fallbackChunker.chunkByFixed(sentence, cfg)
			for _, fc := range fixedChunks {
				fc.Index = len(chunks)
				chunks = append(chunks, fc)
			}
			continue
		}

		// Check if we should start a new chunk
		shouldBreak := isBreakpoint || (currentTokens+sentenceTokens > maxTokens && len(currentChunk) > 0)

		if shouldBreak && len(currentChunk) > 0 {
			// Flush current chunk
			chunks = append(chunks, ChunkOutput{
				Content:    strings.Join(currentChunk, " "),
				Index:      len(chunks),
				TokenCount: currentTokens,
			})
			currentChunk = nil
			currentTokens = 0
		}

		currentChunk = append(currentChunk, sentence)
		currentTokens += sentenceTokens
	}

	// Don't forget the last chunk
	if len(currentChunk) > 0 {
		chunks = append(chunks, ChunkOutput{
			Content:    strings.Join(currentChunk, " "),
			Index:      len(chunks),
			TokenCount: currentTokens,
		})
	}

	return chunks
}

// cosineSimilarity calculates the cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64

	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}
