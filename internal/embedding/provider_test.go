package embedding

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petal-labs/cortex/internal/config"
)

// MockProvider is a test implementation of Provider.
type MockProvider struct {
	dimensions int
	callCount  int
	embedFunc  func(text string) []float32
}

func NewMockProvider(dimensions int) *MockProvider {
	return &MockProvider{
		dimensions: dimensions,
		embedFunc: func(text string) []float32 {
			// Generate deterministic embedding based on text hash
			hash := sha256.Sum256([]byte(text))
			embedding := make([]float32, dimensions)
			for i := 0; i < dimensions && i*4 < len(hash); i++ {
				embedding[i] = float32(hash[i%32]) / 255.0
			}
			return embedding
		},
	}
}

func (m *MockProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, ErrEmptyInput
	}
	m.callCount++
	return m.embedFunc(text), nil
}

func (m *MockProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		if text == "" {
			results[i] = make([]float32, m.dimensions)
		} else {
			m.callCount++
			results[i] = m.embedFunc(text)
		}
	}
	return results, nil
}

func (m *MockProvider) Dimensions() int {
	return m.dimensions
}

func (m *MockProvider) Close() error {
	return nil
}

func (m *MockProvider) CallCount() int {
	return m.callCount
}

func (m *MockProvider) ResetCallCount() {
	m.callCount = 0
}

// Tests

func TestMockProviderEmbed(t *testing.T) {
	provider := NewMockProvider(128)

	embedding, err := provider.Embed(context.Background(), "test text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embedding) != 128 {
		t.Errorf("expected embedding length 128, got %d", len(embedding))
	}

	// Same text should produce same embedding
	embedding2, err := provider.Embed(context.Background(), "test text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i := range embedding {
		if embedding[i] != embedding2[i] {
			t.Errorf("expected deterministic embeddings")
			break
		}
	}
}

func TestMockProviderEmbedEmpty(t *testing.T) {
	provider := NewMockProvider(128)

	_, err := provider.Embed(context.Background(), "")
	if err != ErrEmptyInput {
		t.Errorf("expected ErrEmptyInput, got %v", err)
	}
}

func TestMockProviderEmbedBatch(t *testing.T) {
	provider := NewMockProvider(128)

	texts := []string{"hello", "world", "test"}
	embeddings, err := provider.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embeddings) != 3 {
		t.Errorf("expected 3 embeddings, got %d", len(embeddings))
	}

	for i, emb := range embeddings {
		if len(emb) != 128 {
			t.Errorf("embedding %d has wrong length: %d", i, len(emb))
		}
	}
}

func TestCachedProviderEmbed(t *testing.T) {
	mock := NewMockProvider(128)
	cached, err := NewCachedProvider(mock, 10)
	if err != nil {
		t.Fatalf("failed to create cached provider: %v", err)
	}

	// First call should hit the underlying provider
	emb1, err := cached.Embed(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.CallCount() != 1 {
		t.Errorf("expected 1 call, got %d", mock.CallCount())
	}

	// Second call should be cached
	emb2, err := cached.Embed(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.CallCount() != 1 {
		t.Errorf("expected still 1 call, got %d", mock.CallCount())
	}

	// Verify same embedding returned
	for i := range emb1 {
		if emb1[i] != emb2[i] {
			t.Error("cached embedding differs from original")
			break
		}
	}

	// Different text should hit provider
	_, err = cached.Embed(context.Background(), "different")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.CallCount() != 2 {
		t.Errorf("expected 2 calls, got %d", mock.CallCount())
	}
}

func TestCachedProviderEmbedBatch(t *testing.T) {
	mock := NewMockProvider(128)
	cached, err := NewCachedProvider(mock, 10)
	if err != nil {
		t.Fatalf("failed to create cached provider: %v", err)
	}

	// First batch
	texts := []string{"a", "b", "c"}
	_, err = cached.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.CallCount() != 3 {
		t.Errorf("expected 3 calls, got %d", mock.CallCount())
	}

	// Second batch with some cached
	texts2 := []string{"a", "d", "c"}
	_, err = cached.EmbedBatch(context.Background(), texts2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only "d" should be new
	if mock.CallCount() != 4 {
		t.Errorf("expected 4 calls (1 new), got %d", mock.CallCount())
	}
}

func TestCachedProviderStats(t *testing.T) {
	mock := NewMockProvider(128)
	cached, err := NewCachedProvider(mock, 100)
	if err != nil {
		t.Fatalf("failed to create cached provider: %v", err)
	}

	size, capacity := cached.CacheStats()
	if size != 0 || capacity != 100 {
		t.Errorf("initial stats wrong: size=%d, capacity=%d", size, capacity)
	}

	cached.Embed(context.Background(), "test1")
	cached.Embed(context.Background(), "test2")

	size, capacity = cached.CacheStats()
	if size != 2 || capacity != 100 {
		t.Errorf("after embeds stats wrong: size=%d, capacity=%d", size, capacity)
	}

	cached.ClearCache()
	size, _ = cached.CacheStats()
	if size != 0 {
		t.Errorf("after clear size should be 0, got %d", size)
	}
}

func TestCachedProviderImmutability(t *testing.T) {
	mock := NewMockProvider(128)
	cached, err := NewCachedProvider(mock, 10)
	if err != nil {
		t.Fatalf("failed to create cached provider: %v", err)
	}

	// Get embedding
	emb1, _ := cached.Embed(context.Background(), "test")
	original := emb1[0]

	// Mutate the returned embedding
	emb1[0] = 999.0

	// Get again - should not be affected by mutation
	emb2, _ := cached.Embed(context.Background(), "test")
	if emb2[0] != original {
		t.Errorf("cache was mutated: expected %f, got %f", original, emb2[0])
	}
}

func TestIrisClientCreation(t *testing.T) {
	cfg := config.DefaultConfig()

	client, err := NewIrisClient(cfg)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	if client.Dimensions() != 1536 {
		t.Errorf("expected 1536 dimensions, got %d", client.Dimensions())
	}
}

func TestIrisClientEmptyEndpoint(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Iris.Endpoint = ""

	_, err := NewIrisClient(cfg)
	if err == nil {
		t.Error("expected error for empty endpoint")
	}
}

func TestIrisClientMockServer(t *testing.T) {
	// Create a mock Iris server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Return mock embeddings
		response := `{
			"embeddings": [[0.1, 0.2, 0.3, 0.4]],
			"model": "test-model",
			"usage": {"prompt_tokens": 10, "total_tokens": 10}
		}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(response))
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.Iris.Endpoint = server.URL
	cfg.Embedding.Dimensions = 4

	client, err := NewIrisClient(cfg)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	embedding, err := client.Embed(context.Background(), "test text")
	if err != nil {
		t.Fatalf("failed to embed: %v", err)
	}

	expected := []float32{0.1, 0.2, 0.3, 0.4}
	if len(embedding) != len(expected) {
		t.Fatalf("expected %d dimensions, got %d", len(expected), len(embedding))
	}

	for i, v := range expected {
		if embedding[i] != v {
			t.Errorf("embedding[%d] = %f, expected %f", i, embedding[i], v)
		}
	}
}

func TestIrisClientBatchMockServer(t *testing.T) {
	// Create a mock Iris server for batch requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return embeddings for each input
		response := `{
			"embeddings": [[0.1, 0.2], [0.3, 0.4], [0.5, 0.6]],
			"model": "test-model",
			"usage": {"prompt_tokens": 30, "total_tokens": 30}
		}`
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(response))
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.Iris.Endpoint = server.URL
	cfg.Embedding.Dimensions = 2
	cfg.Embedding.BatchSize = 10

	client, err := NewIrisClient(cfg)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	texts := []string{"a", "b", "c"}
	embeddings, err := client.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("failed to embed batch: %v", err)
	}

	if len(embeddings) != 3 {
		t.Fatalf("expected 3 embeddings, got %d", len(embeddings))
	}
}

func TestIrisClientBatchTooLarge(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Embedding.BatchSize = 2

	client, err := NewIrisClient(cfg)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	texts := []string{"a", "b", "c"} // 3 texts but batch size is 2
	_, err = client.EmbedBatch(context.Background(), texts)
	if err == nil {
		t.Error("expected error for batch too large")
	}
}

func TestIrisClientEmptyInput(t *testing.T) {
	cfg := config.DefaultConfig()

	client, err := NewIrisClient(cfg)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	_, err = client.Embed(context.Background(), "")
	if err != ErrEmptyInput {
		t.Errorf("expected ErrEmptyInput, got %v", err)
	}
}

func TestHashText(t *testing.T) {
	hash1 := hashText("hello")
	hash2 := hashText("hello")
	hash3 := hashText("world")

	if hash1 != hash2 {
		t.Error("same text should produce same hash")
	}

	if hash1 == hash3 {
		t.Error("different text should produce different hash")
	}

	// Verify it's a valid hex string
	if len(hash1) != 64 { // SHA-256 produces 32 bytes = 64 hex chars
		t.Errorf("expected 64 char hex string, got %d", len(hash1))
	}
}

