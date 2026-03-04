package knowledge

import (
	"strings"
	"unicode"

	"github.com/petal-labs/cortex/pkg/types"
)

// ChunkStrategy defines the available chunking strategies.
type ChunkStrategy string

const (
	ChunkStrategyFixed     ChunkStrategy = "fixed"
	ChunkStrategySentence  ChunkStrategy = "sentence"
	ChunkStrategyParagraph ChunkStrategy = "paragraph"
	ChunkStrategySemantic  ChunkStrategy = "semantic"
)

// Chunker splits text into chunks for indexing.
type Chunker interface {
	// Chunk splits content into chunks based on the configured strategy.
	Chunk(content string, cfg types.ChunkConfig) []ChunkOutput
}

// ChunkOutput represents a single chunk produced by the chunker.
type ChunkOutput struct {
	Content    string
	Index      int
	TokenCount int
}

// DefaultChunker implements all chunking strategies.
type DefaultChunker struct{}

// NewChunker creates a new default chunker.
func NewChunker() *DefaultChunker {
	return &DefaultChunker{}
}

// Chunk splits content using the specified strategy.
func (c *DefaultChunker) Chunk(content string, cfg types.ChunkConfig) []ChunkOutput {
	strategy := ChunkStrategy(cfg.Strategy)

	switch strategy {
	case ChunkStrategySentence:
		return c.chunkBySentence(content, cfg)
	case ChunkStrategyParagraph:
		return c.chunkByParagraph(content, cfg)
	default:
		return c.chunkByFixed(content, cfg)
	}
}

// chunkByFixed splits content by word count with overlap.
func (c *DefaultChunker) chunkByFixed(content string, cfg types.ChunkConfig) []ChunkOutput {
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 512
	}

	overlap := max(cfg.Overlap, 0)
	if overlap >= maxTokens {
		overlap = maxTokens / 4
	}

	words := tokenize(content)
	if len(words) == 0 {
		return nil
	}

	var chunks []ChunkOutput
	step := maxTokens - overlap
	if step <= 0 {
		step = 1
	}

	for i := 0; i < len(words); i += step {
		end := min(i+maxTokens, len(words))

		chunkWords := words[i:end]
		chunkContent := strings.Join(chunkWords, " ")

		chunks = append(chunks, ChunkOutput{
			Content:    chunkContent,
			Index:      len(chunks),
			TokenCount: len(chunkWords),
		})

		// If we've reached the end, stop
		if end >= len(words) {
			break
		}
	}

	return chunks
}

// chunkBySentence splits content at sentence boundaries, grouping up to maxTokens.
func (c *DefaultChunker) chunkBySentence(content string, cfg types.ChunkConfig) []ChunkOutput {
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 512
	}

	sentences := splitSentences(content)
	if len(sentences) == 0 {
		return nil
	}

	var chunks []ChunkOutput
	var currentChunk []string
	currentTokens := 0

	for _, sentence := range sentences {
		sentenceTokens := countTokens(sentence)

		// If single sentence exceeds max, split it with fixed chunking
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
			fixedChunks := c.chunkByFixed(sentence, cfg)
			for _, fc := range fixedChunks {
				fc.Index = len(chunks)
				chunks = append(chunks, fc)
			}
			continue
		}

		// Check if adding this sentence would exceed the limit
		if currentTokens+sentenceTokens > maxTokens && len(currentChunk) > 0 {
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

// chunkByParagraph splits content on double newlines.
func (c *DefaultChunker) chunkByParagraph(content string, cfg types.ChunkConfig) []ChunkOutput {
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 512
	}

	// Split on paragraph boundaries
	paragraphs := splitParagraphs(content)
	if len(paragraphs) == 0 {
		return nil
	}

	var chunks []ChunkOutput
	var currentChunk []string
	currentTokens := 0

	for _, para := range paragraphs {
		paraTokens := countTokens(para)

		// If single paragraph exceeds max, split with sentence chunking
		if paraTokens > maxTokens {
			// Flush current chunk first
			if len(currentChunk) > 0 {
				chunks = append(chunks, ChunkOutput{
					Content:    strings.Join(currentChunk, "\n\n"),
					Index:      len(chunks),
					TokenCount: currentTokens,
				})
				currentChunk = nil
				currentTokens = 0
			}

			// Split the long paragraph using sentence chunking
			sentenceChunks := c.chunkBySentence(para, cfg)
			for _, sc := range sentenceChunks {
				sc.Index = len(chunks)
				chunks = append(chunks, sc)
			}
			continue
		}

		// Check if adding this paragraph would exceed the limit
		if currentTokens+paraTokens > maxTokens && len(currentChunk) > 0 {
			// Flush current chunk
			chunks = append(chunks, ChunkOutput{
				Content:    strings.Join(currentChunk, "\n\n"),
				Index:      len(chunks),
				TokenCount: currentTokens,
			})
			currentChunk = nil
			currentTokens = 0
		}

		currentChunk = append(currentChunk, para)
		currentTokens += paraTokens
	}

	// Don't forget the last chunk
	if len(currentChunk) > 0 {
		chunks = append(chunks, ChunkOutput{
			Content:    strings.Join(currentChunk, "\n\n"),
			Index:      len(chunks),
			TokenCount: currentTokens,
		})
	}

	return chunks
}

// tokenize splits text into words (simple whitespace tokenization).
// This is a simple approximation; for production, consider tiktoken or similar.
func tokenize(text string) []string {
	var words []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsSpace(r) {
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		} else {
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		words = append(words, current.String())
	}

	return words
}

// countTokens returns an approximate token count for text.
// Simple word-based approximation.
func countTokens(text string) int {
	return len(tokenize(text))
}

// splitSentences splits text into sentences.
// Uses simple heuristics: splits on . ! ? followed by space and capital letter.
func splitSentences(text string) []string {
	var sentences []string
	var current strings.Builder
	runes := []rune(text)

	for i := 0; i < len(runes); i++ {
		current.WriteRune(runes[i])

		// Check for sentence boundary
		if runes[i] == '.' || runes[i] == '!' || runes[i] == '?' {
			// Look ahead for space followed by capital letter or end of text
			if i+1 >= len(runes) {
				// End of text
				sentence := strings.TrimSpace(current.String())
				if sentence != "" {
					sentences = append(sentences, sentence)
				}
				current.Reset()
			} else if i+2 < len(runes) && unicode.IsSpace(runes[i+1]) && unicode.IsUpper(runes[i+2]) {
				// Sentence boundary detected
				sentence := strings.TrimSpace(current.String())
				if sentence != "" {
					sentences = append(sentences, sentence)
				}
				current.Reset()
				// Skip the space
				i++
			}
		}
	}

	// Add remaining text as last sentence
	remaining := strings.TrimSpace(current.String())
	if remaining != "" {
		sentences = append(sentences, remaining)
	}

	return sentences
}

// splitParagraphs splits text on double newlines.
func splitParagraphs(text string) []string {
	// Normalize line endings
	text = strings.ReplaceAll(text, "\r\n", "\n")

	// Split on double newlines
	parts := strings.Split(text, "\n\n")

	var paragraphs []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			paragraphs = append(paragraphs, p)
		}
	}

	return paragraphs
}
