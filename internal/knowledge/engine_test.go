package knowledge

import (
	"context"
	"crypto/sha256"
	"database/sql"
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
}

func NewMockEmbeddingProvider(dimensions int) *MockEmbeddingProvider {
	return &MockEmbeddingProvider{dimensions: dimensions}
}

func (m *MockEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	m.callCount++
	return m.generateEmbedding(text), nil
}

func (m *MockEmbeddingProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		m.callCount++
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

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

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
