package knowledge

import (
	"context"
	"math"
	"testing"

	"github.com/petal-labs/cortex/pkg/types"
)

// mockEmbeddingProvider returns embeddings that simulate topic transitions.
type mockSemanticEmbedder struct {
	dimensions int
	// sentenceTopics maps sentence index to a topic vector offset
	// Sentences with similar offsets will have similar embeddings
	sentenceTopics map[int]float32
	callCount      int
}

func newMockSemanticEmbedder(dimensions int) *mockSemanticEmbedder {
	return &mockSemanticEmbedder{
		dimensions:     dimensions,
		sentenceTopics: make(map[int]float32),
	}
}

// SetTopics configures which sentences belong to which topics.
// Topic values create different embedding directions.
func (m *mockSemanticEmbedder) SetTopics(topics map[int]float32) {
	m.sentenceTopics = topics
}

func (m *mockSemanticEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	m.callCount++
	// Generate embedding based on text length (simple heuristic)
	vec := make([]float32, m.dimensions)
	// Use text length to create some variation
	offset := float32(len(text)%10) * 0.1
	for i := range vec {
		vec[i] = offset + float32(i)*0.001
	}
	return vec, nil
}

func (m *mockSemanticEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		// Create embeddings that differ based on topic
		vec := make([]float32, m.dimensions)
		topic := m.sentenceTopics[i]
		for j := range vec {
			// Create distinct directions for different topics
			vec[j] = float32(math.Sin(float64(j)*0.1 + float64(topic)))
		}
		results[i] = vec
		m.callCount++

		_ = text // Avoid unused warning
	}
	return results, nil
}

func (m *mockSemanticEmbedder) Dimensions() int {
	return m.dimensions
}

func (m *mockSemanticEmbedder) Close() error {
	return nil
}

func TestSemanticChunker(t *testing.T) {
	t.Run("chunks with semantic boundaries", func(t *testing.T) {
		emb := newMockSemanticEmbedder(128)

		// Set up topics: first 3 windows have topic 0, next 3 have topic 1
		// This should create a breakpoint between them
		emb.SetTopics(map[int]float32{
			0: 0,
			1: 0,
			2: 0,
			3: 0,
			4: 3.14, // Topic change!
			5: 3.14,
			6: 3.14,
			7: 3.14,
		})

		chunker := NewSemanticChunker(emb, WithSimilarityThreshold(0.5), WithWindowSize(2))

		// Create content with clear topic separation
		content := `Introduction to machine learning. Neural networks are powerful. Deep learning is a subset of ML.

		The weather today is sunny. It will rain tomorrow. The forecast shows clouds. Weekend looks good.`

		cfg := types.ChunkConfig{
			Strategy:  "semantic",
			MaxTokens: 100,
		}

		chunks, err := chunker.Chunk(context.Background(), content, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should have at least 2 chunks due to the topic change
		if len(chunks) < 2 {
			t.Errorf("expected at least 2 chunks, got %d", len(chunks))
		}
	})

	t.Run("falls back for short content", func(t *testing.T) {
		emb := newMockSemanticEmbedder(128)
		chunker := NewSemanticChunker(emb, WithWindowSize(3))

		// Content too short for semantic chunking
		content := "Just two sentences. Not enough for windows."

		cfg := types.ChunkConfig{
			Strategy:  "semantic",
			MaxTokens: 100,
		}

		chunks, err := chunker.Chunk(context.Background(), content, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should fall back and produce at least 1 chunk
		if len(chunks) == 0 {
			t.Error("expected at least 1 chunk")
		}
	})

	t.Run("respects maxTokens", func(t *testing.T) {
		emb := newMockSemanticEmbedder(128)
		// All same topic so no semantic breaks
		emb.SetTopics(map[int]float32{0: 0, 1: 0, 2: 0, 3: 0, 4: 0, 5: 0})

		chunker := NewSemanticChunker(emb)

		// Long content that should be split by token limit
		content := `First sentence has many words. Second sentence also has words. Third sentence continues.
		Fourth sentence here. Fifth sentence too. Sixth sentence ends this.
		Seventh starts new paragraph. Eighth follows. Ninth comes next. Tenth finishes.`

		cfg := types.ChunkConfig{
			Strategy:  "semantic",
			MaxTokens: 15, // Very small to force splits
		}

		chunks, err := chunker.Chunk(context.Background(), content, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should have multiple chunks due to token limit
		if len(chunks) < 2 {
			t.Errorf("expected multiple chunks, got %d", len(chunks))
		}

		// Each chunk should respect token limit (approximately)
		for i, chunk := range chunks {
			if chunk.TokenCount > cfg.MaxTokens*2 { // Allow some flexibility
				t.Errorf("chunk %d has %d tokens, exceeds limit", i, chunk.TokenCount)
			}
		}
	})

	t.Run("handles single long sentence", func(t *testing.T) {
		emb := newMockSemanticEmbedder(128)
		chunker := NewSemanticChunker(emb)

		// Single very long sentence
		longSentence := "This is a very long sentence that contains many words and should be split into multiple chunks because it exceeds the maximum token limit that we have configured for this test case which is intentionally set very low to trigger the fixed chunking fallback mechanism."

		cfg := types.ChunkConfig{
			Strategy:  "semantic",
			MaxTokens: 10,
		}

		chunks, err := chunker.Chunk(context.Background(), longSentence, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should have multiple chunks
		if len(chunks) < 2 {
			t.Errorf("expected multiple chunks for long sentence, got %d", len(chunks))
		}
	})
}

func TestCosineSimilarity(t *testing.T) {
	t.Run("identical vectors", func(t *testing.T) {
		a := []float32{1, 2, 3}
		b := []float32{1, 2, 3}

		sim := cosineSimilarity(a, b)
		if sim < 0.999 {
			t.Errorf("expected ~1.0, got %f", sim)
		}
	})

	t.Run("orthogonal vectors", func(t *testing.T) {
		a := []float32{1, 0, 0}
		b := []float32{0, 1, 0}

		sim := cosineSimilarity(a, b)
		if sim > 0.001 {
			t.Errorf("expected ~0.0, got %f", sim)
		}
	})

	t.Run("opposite vectors", func(t *testing.T) {
		a := []float32{1, 2, 3}
		b := []float32{-1, -2, -3}

		sim := cosineSimilarity(a, b)
		if sim > -0.999 {
			t.Errorf("expected ~-1.0, got %f", sim)
		}
	})

	t.Run("empty vectors", func(t *testing.T) {
		a := []float32{}
		b := []float32{}

		sim := cosineSimilarity(a, b)
		if sim != 0 {
			t.Errorf("expected 0 for empty vectors, got %f", sim)
		}
	})

	t.Run("different length vectors", func(t *testing.T) {
		a := []float32{1, 2, 3}
		b := []float32{1, 2}

		sim := cosineSimilarity(a, b)
		if sim != 0 {
			t.Errorf("expected 0 for different length vectors, got %f", sim)
		}
	})
}

func TestCreateWindows(t *testing.T) {
	emb := newMockSemanticEmbedder(128)
	chunker := NewSemanticChunker(emb, WithWindowSize(3))

	sentences := []string{"S1", "S2", "S3", "S4", "S5"}
	windows := chunker.createWindows(sentences)

	// With 5 sentences and window size 3, should get 3 windows
	// [S1,S2,S3], [S2,S3,S4], [S3,S4,S5]
	expectedCount := 3
	if len(windows) != expectedCount {
		t.Errorf("expected %d windows, got %d", expectedCount, len(windows))
	}

	// Verify window contents
	if len(windows) >= 1 && windows[0][0] != "S1" {
		t.Error("first window should start with S1")
	}
	if len(windows) >= 3 && windows[2][2] != "S5" {
		t.Error("last window should end with S5")
	}
}

func TestFindBreakpoints(t *testing.T) {
	emb := newMockSemanticEmbedder(128)
	chunker := NewSemanticChunker(emb, WithSimilarityThreshold(0.5))

	// Create embeddings where there's a clear break between index 1 and 2
	embeddings := [][]float32{
		{1, 0, 0}, // Topic A
		{1, 0, 0}, // Topic A (similar to above)
		{0, 1, 0}, // Topic B (orthogonal = different topic)
		{0, 1, 0}, // Topic B
	}

	breakpoints := chunker.findBreakpoints(embeddings)

	// Should find breakpoint at index 2 (between Topic A and Topic B)
	if len(breakpoints) != 1 {
		t.Errorf("expected 1 breakpoint, got %d", len(breakpoints))
	}

	if len(breakpoints) > 0 && breakpoints[0] != 2 {
		t.Errorf("expected breakpoint at index 2, got %d", breakpoints[0])
	}
}
