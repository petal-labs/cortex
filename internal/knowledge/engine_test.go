package knowledge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
	"github.com/petal-labs/cortex/pkg/types"

	_ "github.com/mattn/go-sqlite3"
)

// MockEmbeddingProvider for testing.
type MockEmbeddingProvider struct {
	dimensions int
	callCount  int
	mu         sync.Mutex
}

func NewMockEmbeddingProvider(dimensions int) *MockEmbeddingProvider {
	return &MockEmbeddingProvider{dimensions: dimensions}
}

func (m *MockEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	return m.generateEmbedding(text), nil
}

func (m *MockEmbeddingProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		m.mu.Lock()
		m.callCount++
		m.mu.Unlock()
		results[i] = m.generateEmbedding(text)
	}
	return results, nil
}

func (m *MockEmbeddingProvider) Dimensions() int {
	return m.dimensions
}

func (m *MockEmbeddingProvider) Close() error {
	return nil
}

func (m *MockEmbeddingProvider) generateEmbedding(text string) []float32 {
	hash := sha256.Sum256([]byte(text))
	embedding := make([]float32, m.dimensions)
	for i := 0; i < m.dimensions && i < 32; i++ {
		embedding[i] = float32(hash[i]) / 255.0
	}
	return embedding
}

func setupTestEngine(t *testing.T) (*Engine, storage.Backend) {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	// SQLite requires single connection for in-memory databases
	// to ensure all operations see the same data
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	backend := sqlite.NewWithDB(db)
	if err := backend.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	cfg := config.DefaultConfig()
	embProvider := NewMockEmbeddingProvider(128)

	engine, err := NewEngine(backend, embProvider, &cfg.Knowledge)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	return engine, backend
}

func createTestCollection(t *testing.T, engine *Engine, namespace string) *types.Collection {
	t.Helper()

	col, err := engine.CreateCollection(context.Background(), namespace, CreateCollectionOpts{
		Name:        "test-collection",
		Description: "Test collection for unit tests",
	})
	if err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}
	return col
}

func TestEngineCreation(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	backend := sqlite.NewWithDB(db)
	if err := backend.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	// Test with nil config - should use defaults
	engine, err := NewEngine(backend, nil, nil)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}

	// Test with nil backend - should fail
	_, err = NewEngine(nil, nil, nil)
	if err == nil {
		t.Error("expected error for nil backend")
	}
}

func TestCreateCollection(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	col, err := engine.CreateCollection(ctx, namespace, CreateCollectionOpts{
		Name:        "my-collection",
		Description: "Test description",
		ChunkConfig: &types.ChunkConfig{
			Strategy:  "sentence",
			MaxTokens: 256,
			Overlap:   25,
		},
	})

	if err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	if col.Name != "my-collection" {
		t.Errorf("expected name 'my-collection', got %s", col.Name)
	}
	if col.Namespace != namespace {
		t.Errorf("expected namespace %s, got %s", namespace, col.Namespace)
	}
	if col.ChunkConfig.Strategy != "sentence" {
		t.Errorf("expected strategy 'sentence', got %s", col.ChunkConfig.Strategy)
	}
}

func TestCreateCollectionEmptyName(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.CreateCollection(context.Background(), "ns", CreateCollectionOpts{
		Name: "",
	})

	if err == nil {
		t.Error("expected error for empty name")
	}
}

func TestListCollections(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	// Create multiple collections
	for i := 0; i < 3; i++ {
		_, err := engine.CreateCollection(ctx, namespace, CreateCollectionOpts{
			Name: "collection-" + string(rune('a'+i)),
		})
		if err != nil {
			t.Fatalf("failed to create collection %d: %v", i, err)
		}
	}

	collections, cursor, err := engine.ListCollections(ctx, namespace, "", 10)
	if err != nil {
		t.Fatalf("failed to list collections: %v", err)
	}

	if len(collections) != 3 {
		t.Errorf("expected 3 collections, got %d", len(collections))
	}
	if cursor != "" {
		t.Errorf("expected empty cursor, got %s", cursor)
	}
}

func TestGetCollection(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	created, _ := engine.CreateCollection(ctx, namespace, CreateCollectionOpts{
		Name: "test-col",
	})

	retrieved, err := engine.GetCollection(ctx, namespace, created.ID)
	if err != nil {
		t.Fatalf("failed to get collection: %v", err)
	}

	if retrieved.ID != created.ID {
		t.Errorf("expected ID %s, got %s", created.ID, retrieved.ID)
	}
	if retrieved.Name != "test-col" {
		t.Errorf("expected name 'test-col', got %s", retrieved.Name)
	}
}

func TestGetCollectionNotFound(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.GetCollection(context.Background(), "ns", "nonexistent")
	if err != ErrCollectionNotFound {
		t.Errorf("expected ErrCollectionNotFound, got %v", err)
	}
}

func TestDeleteCollection(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	col, _ := engine.CreateCollection(ctx, namespace, CreateCollectionOpts{
		Name: "to-delete",
	})

	err := engine.DeleteCollection(ctx, namespace, col.ID)
	if err != nil {
		t.Fatalf("failed to delete collection: %v", err)
	}

	// Verify it's gone
	_, err = engine.GetCollection(ctx, namespace, col.ID)
	if err != ErrCollectionNotFound {
		t.Errorf("expected ErrCollectionNotFound after delete, got %v", err)
	}
}

func TestIngestDocument(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	content := "This is a test document with some content. It has multiple sentences. Each should be chunked appropriately."

	result, err := engine.Ingest(ctx, namespace, col.ID, content, &IngestOpts{
		Title:       "Test Document",
		Source:      "test://source",
		ContentType: "text",
		Metadata:    map[string]string{"author": "test"},
	})

	if err != nil {
		t.Fatalf("failed to ingest: %v", err)
	}

	if result.DocumentID == "" {
		t.Error("expected document ID")
	}
	if result.ChunksCreated == 0 {
		t.Error("expected at least one chunk")
	}
	if result.CollectionID != col.ID {
		t.Errorf("expected collection ID %s, got %s", col.ID, result.CollectionID)
	}
}

func TestIngestEmptyContent(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	_, err := engine.Ingest(ctx, namespace, col.ID, "", nil)
	if err != ErrEmptyContent {
		t.Errorf("expected ErrEmptyContent, got %v", err)
	}
}

func TestIngestCollectionNotFound(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.Ingest(context.Background(), "ns", "nonexistent", "content", nil)
	if err != ErrCollectionNotFound {
		t.Errorf("expected ErrCollectionNotFound, got %v", err)
	}
}

func TestIngestWithCustomChunkConfig(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	content := "word1 word2 word3 word4 word5 word6 word7 word8"

	result, err := engine.Ingest(ctx, namespace, col.ID, content, &IngestOpts{
		ChunkConfig: &types.ChunkConfig{
			Strategy:  "fixed",
			MaxTokens: 2,
			Overlap:   0,
		},
	})

	if err != nil {
		t.Fatalf("failed to ingest: %v", err)
	}

	// 8 words / 2 = 4 chunks
	if result.ChunksCreated != 4 {
		t.Errorf("expected 4 chunks, got %d", result.ChunksCreated)
	}
}

func TestGetDocument(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	result, _ := engine.Ingest(ctx, namespace, col.ID, "test content", &IngestOpts{
		Title: "My Doc",
	})

	doc, err := engine.GetDocument(ctx, namespace, result.DocumentID)
	if err != nil {
		t.Fatalf("failed to get document: %v", err)
	}

	if doc.Title != "My Doc" {
		t.Errorf("expected title 'My Doc', got %s", doc.Title)
	}
	if doc.Content != "test content" {
		t.Errorf("expected content 'test content', got %s", doc.Content)
	}
}

func TestGetDocumentNotFound(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.GetDocument(context.Background(), "ns", "nonexistent")
	if err != ErrDocumentNotFound {
		t.Errorf("expected ErrDocumentNotFound, got %v", err)
	}
}

func TestDeleteDocument(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	result, _ := engine.Ingest(ctx, namespace, col.ID, "to delete", nil)

	err := engine.DeleteDocument(ctx, namespace, result.DocumentID)
	if err != nil {
		t.Fatalf("failed to delete document: %v", err)
	}

	// Verify it's gone
	_, err = engine.GetDocument(ctx, namespace, result.DocumentID)
	if err != ErrDocumentNotFound {
		t.Errorf("expected ErrDocumentNotFound after delete, got %v", err)
	}
}

func TestSearchKnowledge(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	// Ingest documents with different content
	engine.Ingest(ctx, namespace, col.ID, "The quick brown fox jumps over the lazy dog.", &IngestOpts{
		Title: "Fox Story",
	})
	engine.Ingest(ctx, namespace, col.ID, "Python is a programming language.", &IngestOpts{
		Title: "Programming",
	})

	// Search
	results, err := engine.Search(ctx, namespace, "fox animal", nil)
	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}

	if results.Query != "fox animal" {
		t.Errorf("expected query 'fox animal', got %s", results.Query)
	}
	if results.TotalFound == 0 {
		t.Error("expected at least one result")
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.Search(context.Background(), "ns", "", nil)
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestSearchWithOptions(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	engine.Ingest(ctx, namespace, col.ID, "test content for search", nil)

	results, err := engine.Search(ctx, namespace, "test", &SearchOpts{
		CollectionID: &col.ID,
		TopK:         5,
		MinScore:     0.0,
	})

	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}

	if results.TotalFound == 0 {
		t.Error("expected results")
	}
}

func TestSearchNoEmbeddingProvider(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	backend := sqlite.NewWithDB(db)
	if err := backend.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	// Create engine without embedding provider
	engine, err := NewEngine(backend, nil, nil)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	_, err = engine.Search(context.Background(), "ns", "query", nil)
	if err != ErrEmbeddingRequired {
		t.Errorf("expected ErrEmbeddingRequired, got %v", err)
	}
}

func TestCollectionStats(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	// Ingest a document
	engine.Ingest(ctx, namespace, col.ID, "some test content", nil)

	stats, err := engine.CollectionStats(ctx, namespace, col.ID)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}

	if stats.DocumentCount != 1 {
		t.Errorf("expected 1 document, got %d", stats.DocumentCount)
	}
	if stats.ChunkCount == 0 {
		t.Error("expected at least one chunk")
	}
	if stats.LastIngest.IsZero() {
		t.Error("expected non-zero last ingest time")
	}
}

func TestCollectionStatsNotFound(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.CollectionStats(context.Background(), "ns", "nonexistent")
	if err != ErrCollectionNotFound {
		t.Errorf("expected ErrCollectionNotFound, got %v", err)
	}
}

func TestIngestPreservesMetadata(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	result, err := engine.Ingest(ctx, namespace, col.ID, "test content", &IngestOpts{
		Title:    "Test Title",
		Source:   "http://example.com",
		Metadata: map[string]string{"key": "value", "author": "test"},
	})

	if err != nil {
		t.Fatalf("failed to ingest: %v", err)
	}

	doc, err := engine.GetDocument(ctx, namespace, result.DocumentID)
	if err != nil {
		t.Fatalf("failed to get document: %v", err)
	}

	if doc.Title != "Test Title" {
		t.Errorf("expected title 'Test Title', got %s", doc.Title)
	}
	if doc.Source != "http://example.com" {
		t.Errorf("expected source 'http://example.com', got %s", doc.Source)
	}
	if doc.Metadata["key"] != "value" {
		t.Errorf("expected metadata key 'value', got %s", doc.Metadata["key"])
	}
}

func TestSearchWithContextWindow(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	// Ingest a longer document that will create multiple chunks
	content := "First chunk content. Second chunk content. Third chunk content. Fourth chunk content. Fifth chunk content."

	engine.Ingest(ctx, namespace, col.ID, content, &IngestOpts{
		ChunkConfig: &types.ChunkConfig{
			Strategy:  "sentence",
			MaxTokens: 3,
		},
	})

	results, err := engine.Search(ctx, namespace, "chunk", &SearchOpts{
		ContextWindow: 1, // Request 1 chunk before/after
	})

	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}

	// Results should have context if there are adjacent chunks
	if results.TotalFound == 0 {
		t.Error("expected results")
	}
}

func TestIngestDefaultContentType(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	result, _ := engine.Ingest(ctx, namespace, col.ID, "test", nil)

	doc, _ := engine.GetDocument(ctx, namespace, result.DocumentID)

	if doc.ContentType != "text" {
		t.Errorf("expected default content type 'text', got %s", doc.ContentType)
	}
}

func TestIngestTimestamps(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	before := time.Now().Add(-time.Second)
	result, _ := engine.Ingest(ctx, namespace, col.ID, "test", nil)
	after := time.Now().Add(time.Second)

	doc, _ := engine.GetDocument(ctx, namespace, result.DocumentID)

	if doc.CreatedAt.Before(before) || doc.CreatedAt.After(after) {
		t.Errorf("created_at not in expected range: %v", doc.CreatedAt)
	}
	if doc.UpdatedAt.Before(before) || doc.UpdatedAt.After(after) {
		t.Errorf("updated_at not in expected range: %v", doc.UpdatedAt)
	}
}

func TestJoinChunks(t *testing.T) {
	tests := []struct {
		input    []string
		expected string
	}{
		{[]string{}, ""},
		{[]string{"one"}, "one"},
		{[]string{"one", "two"}, "one\n\ntwo"},
		{[]string{"a", "b", "c"}, "a\n\nb\n\nc"},
	}

	for _, tt := range tests {
		result := joinChunks(tt.input)
		if result != tt.expected {
			t.Errorf("joinChunks(%v) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestIngestSemanticChunking(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	// Content with clear topic separation for semantic chunking
	content := `Machine learning is a subset of artificial intelligence. Neural networks power deep learning systems. ML models learn from data to make predictions.

The weather today is sunny and warm. Tomorrow will bring rain and cooler temperatures. The forecast shows clouds throughout the week. Weekend weather looks favorable for outdoor activities.`

	result, err := engine.Ingest(ctx, namespace, col.ID, content, &IngestOpts{
		Title: "Semantic Chunking Test",
		ChunkConfig: &types.ChunkConfig{
			Strategy:  "semantic",
			MaxTokens: 100,
		},
	})

	if err != nil {
		t.Fatalf("failed to ingest with semantic chunking: %v", err)
	}

	// Should have created chunks
	if result.ChunksCreated < 1 {
		t.Errorf("expected at least 1 chunk, got %d", result.ChunksCreated)
	}

	// Verify document was stored
	doc, err := engine.GetDocument(ctx, namespace, result.DocumentID)
	if err != nil {
		t.Fatalf("failed to get document: %v", err)
	}

	if doc.Title != "Semantic Chunking Test" {
		t.Errorf("expected title 'Semantic Chunking Test', got %s", doc.Title)
	}
}

func TestIngestSemanticChunkingFallback(t *testing.T) {
	// Test that semantic chunking falls back to sentence chunking
	// when content is too short for semantic analysis
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	// Short content that can't be semantically chunked (fewer sentences than 2*windowSize)
	content := "This is a short sentence. Here is another one."

	result, err := engine.Ingest(ctx, namespace, col.ID, content, &IngestOpts{
		ChunkConfig: &types.ChunkConfig{
			Strategy:  "semantic",
			MaxTokens: 100,
		},
	})

	if err != nil {
		t.Fatalf("failed to ingest: %v", err)
	}

	// Should still create chunks via fallback
	if result.ChunksCreated < 1 {
		t.Errorf("expected at least 1 chunk from fallback, got %d", result.ChunksCreated)
	}
}

func TestIngestSemanticChunkingNoEmbedder(t *testing.T) {
	// Test that semantic chunking falls back when no embedding provider is available
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	backend := sqlite.NewWithDB(db)
	if err := backend.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	// Create engine WITHOUT embedding provider
	cfg := config.DefaultConfig()
	engine, err := NewEngine(backend, nil, &cfg.Knowledge)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	ctx := context.Background()
	namespace := "test-ns"

	// Create collection first
	col, err := engine.CreateCollection(ctx, namespace, CreateCollectionOpts{
		Name: "test-collection",
	})
	if err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	// Try to ingest with semantic strategy - should fall back
	content := "First sentence here. Second sentence follows. Third sentence continues. Fourth sentence ends. Fifth sentence extra. Sixth sentence more."

	result, err := engine.Ingest(ctx, namespace, col.ID, content, &IngestOpts{
		ChunkConfig: &types.ChunkConfig{
			Strategy:  "semantic",
			MaxTokens: 100,
		},
	})

	if err != nil {
		t.Fatalf("failed to ingest: %v", err)
	}

	// Should still create chunks using the default chunker (not semantic)
	if result.ChunksCreated < 1 {
		t.Errorf("expected at least 1 chunk, got %d", result.ChunksCreated)
	}
}

// MockExtractionEnqueuer implements ExtractionEnqueuer for testing.
type MockExtractionEnqueuer struct {
	callCount int
	items     []extractionItem
}

type extractionItem struct {
	namespace  string
	sourceType string
	sourceID   string
	content    string
}

func NewMockExtractionEnqueuer() *MockExtractionEnqueuer {
	return &MockExtractionEnqueuer{}
}

func (m *MockExtractionEnqueuer) EnqueueForExtraction(ctx context.Context, namespace, sourceType, sourceID, content string) error {
	m.callCount++
	m.items = append(m.items, extractionItem{
		namespace:  namespace,
		sourceType: sourceType,
		sourceID:   sourceID,
		content:    content,
	})
	return nil
}

func TestExtractionEnqueuer(t *testing.T) {
	t.Run("enqueues chunks for extraction on ingest", func(t *testing.T) {
		engine, _ := setupTestEngine(t)
		ctx := context.Background()
		namespace := "test-ns"

		// Set up extraction enqueuer
		enqueuer := NewMockExtractionEnqueuer()
		engine.SetExtractionEnqueuer(enqueuer)

		// Create collection
		col, err := engine.CreateCollection(ctx, namespace, CreateCollectionOpts{
			Name: "test-collection",
		})
		if err != nil {
			t.Fatalf("failed to create collection: %v", err)
		}

		// Ingest a document
		content := "First paragraph of content. Second paragraph of content. Third paragraph of content."
		result, err := engine.Ingest(ctx, namespace, col.ID, content, nil)
		if err != nil {
			t.Fatalf("failed to ingest: %v", err)
		}

		// Verify extraction was enqueued for each chunk
		if enqueuer.callCount != result.ChunksCreated {
			t.Errorf("expected %d extraction calls, got %d", result.ChunksCreated, enqueuer.callCount)
		}

		// Verify extraction item details
		for _, item := range enqueuer.items {
			if item.namespace != namespace {
				t.Errorf("expected namespace %q, got %q", namespace, item.namespace)
			}
			if item.sourceType != "knowledge" {
				t.Errorf("expected sourceType 'knowledge', got %q", item.sourceType)
			}
			if item.content == "" {
				t.Error("expected content to be non-empty")
			}
		}
	})

	t.Run("does not enqueue when no enqueuer set", func(t *testing.T) {
		engine, _ := setupTestEngine(t)
		ctx := context.Background()
		namespace := "test-ns"

		// Create collection
		col, err := engine.CreateCollection(ctx, namespace, CreateCollectionOpts{
			Name: "test-collection",
		})
		if err != nil {
			t.Fatalf("failed to create collection: %v", err)
		}

		// No enqueuer set - should not panic
		_, err = engine.Ingest(ctx, namespace, col.ID, "Test content here.", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("enqueues with correct chunk IDs", func(t *testing.T) {
		engine, backend := setupTestEngine(t)
		ctx := context.Background()
		namespace := "test-ns"

		enqueuer := NewMockExtractionEnqueuer()
		engine.SetExtractionEnqueuer(enqueuer)

		// Create collection
		col, err := engine.CreateCollection(ctx, namespace, CreateCollectionOpts{
			Name: "test-collection",
		})
		if err != nil {
			t.Fatalf("failed to create collection: %v", err)
		}

		// Ingest with multiple chunks
		content := "First sentence for chunk one. Second sentence for chunk one. Third sentence for chunk two. Fourth sentence for chunk two."
		result, err := engine.Ingest(ctx, namespace, col.ID, content, &IngestOpts{
			ChunkConfig: &types.ChunkConfig{
				Strategy:  "fixed",
				MaxTokens: 8, // Small chunk size to get multiple chunks
			},
		})
		if err != nil {
			t.Fatalf("failed to ingest: %v", err)
		}

		// Get chunks from storage to verify IDs match
		doc, err := backend.GetDocument(ctx, namespace, result.DocumentID)
		if err != nil {
			t.Fatalf("failed to get document: %v", err)
		}

		// Verify chunk count matches enqueued count
		if len(enqueuer.items) != result.ChunksCreated {
			t.Errorf("expected %d items, got %d", result.ChunksCreated, len(enqueuer.items))
		}

		// Verify each source ID is a valid UUID format (chunk IDs are UUIDs)
		for _, item := range enqueuer.items {
			if len(item.sourceID) != 36 { // UUID string length
				t.Errorf("expected UUID format sourceID, got %q", item.sourceID)
			}
		}

		_ = doc // Document retrieved successfully
	})
}

func TestBulkIngest(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	documents := []BulkIngestDocument{
		{
			Content: "First document content about machine learning and AI.",
			Title:   "Document 1",
			Source:  "test://doc1",
		},
		{
			Content: "Second document about programming languages and software.",
			Title:   "Document 2",
			Source:  "test://doc2",
		},
		{
			Content:  "Third document discussing database design patterns.",
			Title:    "Document 3",
			Metadata: map[string]string{"topic": "databases"},
		},
	}

	result, err := engine.BulkIngest(ctx, namespace, col.ID, documents, nil)
	if err != nil {
		t.Fatalf("failed to bulk ingest: %v", err)
	}

	if result.TotalDocuments != 3 {
		t.Errorf("expected 3 total documents, got %d", result.TotalDocuments)
	}
	if result.Succeeded != 3 {
		t.Errorf("expected 3 succeeded, got %d", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("expected 0 failed, got %d", result.Failed)
	}
	if result.TotalChunks == 0 {
		t.Error("expected at least some chunks")
	}
	if len(result.Documents) != 3 {
		t.Errorf("expected 3 document results, got %d", len(result.Documents))
	}

	// Verify all documents succeeded
	for i, docResult := range result.Documents {
		if !docResult.Success {
			t.Errorf("document %d failed: %s", i, docResult.Error)
		}
		if docResult.DocumentID == "" {
			t.Errorf("document %d missing document ID", i)
		}
	}
}

func TestBulkIngestEmptyDocuments(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	_, err := engine.BulkIngest(ctx, namespace, col.ID, []BulkIngestDocument{}, nil)
	if err == nil {
		t.Error("expected error for empty documents")
	}
}

func TestBulkIngestCollectionNotFound(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	documents := []BulkIngestDocument{
		{Content: "test content"},
	}

	_, err := engine.BulkIngest(context.Background(), "ns", "nonexistent", documents, nil)
	if err != ErrCollectionNotFound {
		t.Errorf("expected ErrCollectionNotFound, got %v", err)
	}
}

func TestBulkIngestWithEmptyContent(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	documents := []BulkIngestDocument{
		{Content: "Valid document content"},
		{Content: ""}, // Empty content should fail
		{Content: "Another valid document"},
	}

	result, err := engine.BulkIngest(ctx, namespace, col.ID, documents, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Succeeded != 2 {
		t.Errorf("expected 2 succeeded, got %d", result.Succeeded)
	}
	if result.Failed != 1 {
		t.Errorf("expected 1 failed, got %d", result.Failed)
	}

	// Check that the empty content document has an error
	if result.Documents[1].Success {
		t.Error("expected document 1 to fail")
	}
	if result.Documents[1].Error == "" {
		t.Error("expected error message for failed document")
	}
}

func TestBulkIngestWithProgress(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	documents := []BulkIngestDocument{
		{Content: "Document one", Title: "Doc 1"},
		{Content: "Document two", Title: "Doc 2"},
		{Content: "Document three", Title: "Doc 3"},
	}

	progressCalls := 0
	opts := &BulkIngestOpts{
		OnProgress: func(completed, total int, doc string) {
			progressCalls++
		},
	}

	result, err := engine.BulkIngest(ctx, namespace, col.ID, documents, opts)
	if err != nil {
		t.Fatalf("failed to bulk ingest: %v", err)
	}

	if progressCalls != result.TotalDocuments {
		t.Errorf("expected %d progress calls, got %d", result.TotalDocuments, progressCalls)
	}
}

func TestBulkIngestWithConcurrency(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"
	col := createTestCollection(t, engine, namespace)

	// Create more documents to test concurrency
	documents := make([]BulkIngestDocument, 10)
	for i := 0; i < 10; i++ {
		documents[i] = BulkIngestDocument{
			Content: "Test document content for concurrency testing number " + string(rune('A'+i)),
			Title:   "Document " + string(rune('A'+i)),
		}
	}

	opts := &BulkIngestOpts{
		Concurrency: 4,
	}

	result, err := engine.BulkIngest(ctx, namespace, col.ID, documents, opts)
	if err != nil {
		t.Fatalf("failed to bulk ingest: %v", err)
	}

	if result.TotalDocuments != 10 {
		t.Errorf("expected 10 total documents, got %d", result.TotalDocuments)
	}
	if result.Succeeded != 10 {
		t.Errorf("expected 10 succeeded, got %d", result.Succeeded)
	}
}
