package knowledge

import (
	"strings"
	"testing"

	"github.com/petal-labs/cortex/pkg/types"
)

func TestChunkerFixedStrategy(t *testing.T) {
	chunker := NewChunker()

	// Create content with 10 words
	content := "one two three four five six seven eight nine ten"

	cfg := types.ChunkConfig{
		Strategy:  "fixed",
		MaxTokens: 4,
		Overlap:   1,
	}

	chunks := chunker.Chunk(content, cfg)

	// With 10 words, max 4, overlap 1, step = 3
	// Chunks: [0:4], [3:7], [6:10]
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(chunks))
	}

	// Verify first chunk
	if chunks[0].Content != "one two three four" {
		t.Errorf("chunk 0 content wrong: %s", chunks[0].Content)
	}
	if chunks[0].Index != 0 {
		t.Errorf("chunk 0 index wrong: %d", chunks[0].Index)
	}
	if chunks[0].TokenCount != 4 {
		t.Errorf("chunk 0 token count wrong: %d", chunks[0].TokenCount)
	}

	// Verify overlap - chunk 1 should start with "four"
	if !strings.HasPrefix(chunks[1].Content, "four") {
		t.Errorf("chunk 1 should start with 'four' (overlap): %s", chunks[1].Content)
	}
}

func TestChunkerFixedStrategyNoOverlap(t *testing.T) {
	chunker := NewChunker()

	content := "one two three four five six"

	cfg := types.ChunkConfig{
		Strategy:  "fixed",
		MaxTokens: 3,
		Overlap:   0,
	}

	chunks := chunker.Chunk(content, cfg)

	// 6 words / 3 = 2 chunks
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks, got %d", len(chunks))
	}

	if chunks[0].Content != "one two three" {
		t.Errorf("chunk 0 content wrong: %s", chunks[0].Content)
	}
	if chunks[1].Content != "four five six" {
		t.Errorf("chunk 1 content wrong: %s", chunks[1].Content)
	}
}

func TestChunkerFixedStrategyDefaultMaxTokens(t *testing.T) {
	chunker := NewChunker()

	// Create content shorter than default (512)
	content := "short content"

	cfg := types.ChunkConfig{
		Strategy:  "fixed",
		MaxTokens: 0, // Should use default
	}

	chunks := chunker.Chunk(content, cfg)

	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk for short content, got %d", len(chunks))
	}
}

func TestChunkerFixedStrategyEmptyContent(t *testing.T) {
	chunker := NewChunker()

	cfg := types.ChunkConfig{
		Strategy:  "fixed",
		MaxTokens: 100,
	}

	chunks := chunker.Chunk("", cfg)

	if chunks != nil {
		t.Errorf("expected nil for empty content, got %d chunks", len(chunks))
	}
}

func TestChunkerSentenceStrategy(t *testing.T) {
	chunker := NewChunker()

	content := "First sentence here. Second sentence follows. Third one too. Fourth is last."

	cfg := types.ChunkConfig{
		Strategy:  "sentence",
		MaxTokens: 10, // Should fit ~2 sentences per chunk
	}

	chunks := chunker.Chunk(content, cfg)

	// Each sentence is ~3 words, so we should get 2 chunks (2 sentences each)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks, got %d", len(chunks))
	}

	// All content should be preserved
	var contentParts []string
	for _, c := range chunks {
		contentParts = append(contentParts, c.Content)
	}
	totalContent := strings.Join(contentParts, " ")

	// Verify all sentences are present
	for _, sentence := range []string{"First sentence", "Second sentence", "Third one", "Fourth"} {
		if !strings.Contains(totalContent, sentence) {
			t.Errorf("missing sentence: %s", sentence)
		}
	}
}

func TestChunkerSentenceStrategyLongSentence(t *testing.T) {
	chunker := NewChunker()

	// Create a very long sentence that exceeds MaxTokens
	words := make([]string, 20)
	for i := range words {
		words[i] = "word"
	}
	longSentence := strings.Join(words, " ") + "."

	content := "Short start. " + longSentence + " Short end."

	cfg := types.ChunkConfig{
		Strategy:  "sentence",
		MaxTokens: 5,
	}

	chunks := chunker.Chunk(content, cfg)

	// The long sentence should be split using fixed chunking
	if len(chunks) < 4 {
		t.Errorf("expected long sentence to be split into multiple chunks, got %d", len(chunks))
	}
}

func TestChunkerParagraphStrategy(t *testing.T) {
	chunker := NewChunker()

	content := "First paragraph here.\n\nSecond paragraph follows.\n\nThird paragraph too."

	cfg := types.ChunkConfig{
		Strategy:  "paragraph",
		MaxTokens: 100, // Each paragraph fits
	}

	chunks := chunker.Chunk(content, cfg)

	// All 3 paragraphs might be combined into fewer chunks depending on token count
	if len(chunks) == 0 {
		t.Error("expected at least one chunk")
	}

	// Verify content preservation
	combinedContent := ""
	for _, c := range chunks {
		combinedContent += c.Content + " "
	}

	if !strings.Contains(combinedContent, "First paragraph") {
		t.Error("missing first paragraph")
	}
	if !strings.Contains(combinedContent, "Third paragraph") {
		t.Error("missing third paragraph")
	}
}

func TestChunkerParagraphStrategySmallMaxTokens(t *testing.T) {
	chunker := NewChunker()

	content := "First paragraph.\n\nSecond paragraph.\n\nThird paragraph."

	cfg := types.ChunkConfig{
		Strategy:  "paragraph",
		MaxTokens: 3, // Each paragraph is about 2 words
	}

	chunks := chunker.Chunk(content, cfg)

	// Each paragraph should be its own chunk
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(chunks))
	}
}

func TestChunkerParagraphStrategyLongParagraph(t *testing.T) {
	chunker := NewChunker()

	// Create a long paragraph that will need sentence splitting
	longPara := "First sentence. Second sentence. Third sentence. Fourth sentence. Fifth sentence."

	content := "Short.\n\n" + longPara

	cfg := types.ChunkConfig{
		Strategy:  "paragraph",
		MaxTokens: 5,
	}

	chunks := chunker.Chunk(content, cfg)

	// The long paragraph should be split
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks, got %d", len(chunks))
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"hello world", 2},
		{"one two three", 3},
		{"   spaces   everywhere   ", 2},
		{"", 0},
		{"single", 1},
		{"tab\tseparated", 2},
		{"newline\nseparated", 2},
	}

	for _, tt := range tests {
		words := tokenize(tt.input)
		if len(words) != tt.expected {
			t.Errorf("tokenize(%q) = %d words, expected %d", tt.input, len(words), tt.expected)
		}
	}
}

func TestCountTokens(t *testing.T) {
	count := countTokens("hello world test")
	if count != 3 {
		t.Errorf("expected 3 tokens, got %d", count)
	}
}

func TestSplitSentences(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"One sentence.", 1},
		{"First. Second.", 2},
		{"Hello! How are you? I'm fine.", 3},
		{"Dr. Smith went home. He was tired.", 3}, // Abbreviation handling is basic - "Dr." splits
		{"", 0},
		{"No period", 1},
	}

	for _, tt := range tests {
		sentences := splitSentences(tt.input)
		if len(sentences) != tt.expected {
			t.Errorf("splitSentences(%q) = %d, expected %d", tt.input, len(sentences), tt.expected)
		}
	}
}

func TestSplitParagraphs(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"Single paragraph", 1},
		{"First\n\nSecond", 2},
		{"One\n\nTwo\n\nThree", 3},
		{"Windows\r\n\r\nLine endings", 2},
		{"", 0},
		{"\n\n\n", 0},
	}

	for _, tt := range tests {
		paragraphs := splitParagraphs(tt.input)
		if len(paragraphs) != tt.expected {
			t.Errorf("splitParagraphs(%q) = %d, expected %d", tt.input, len(paragraphs), tt.expected)
		}
	}
}

func TestChunkerDefaultStrategy(t *testing.T) {
	chunker := NewChunker()

	content := "some test content here"

	// Empty strategy should use fixed (default)
	cfg := types.ChunkConfig{
		Strategy:  "",
		MaxTokens: 100,
	}

	chunks := chunker.Chunk(content, cfg)

	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
}

func TestChunkerOverlapExceedsMax(t *testing.T) {
	chunker := NewChunker()

	content := "one two three four five six seven eight"

	cfg := types.ChunkConfig{
		Strategy:  "fixed",
		MaxTokens: 4,
		Overlap:   10, // Exceeds max, should be reduced
	}

	chunks := chunker.Chunk(content, cfg)

	// Should still produce valid chunks
	if len(chunks) == 0 {
		t.Error("expected at least one chunk")
	}
}

func TestChunkerNegativeOverlap(t *testing.T) {
	chunker := NewChunker()

	content := "one two three four"

	cfg := types.ChunkConfig{
		Strategy:  "fixed",
		MaxTokens: 2,
		Overlap:   -5, // Should be treated as 0
	}

	chunks := chunker.Chunk(content, cfg)

	// Should produce 2 chunks with no overlap
	if len(chunks) != 2 {
		t.Errorf("expected 2 chunks, got %d", len(chunks))
	}
}
