package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

func TestCreateCollection(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	col := &types.Collection{
		Namespace:   "test-ns",
		Name:        "test-collection",
		Description: "A test collection",
		ChunkConfig: types.ChunkConfig{
			Strategy:  "fixed",
			MaxTokens: 512,
			Overlap:   50,
		},
	}

	if err := backend.CreateCollection(ctx, col); err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	if col.ID == "" {
		t.Error("expected collection ID to be generated")
	}

	// Verify collection was created
	retrieved, err := backend.GetCollection(ctx, "test-ns", col.ID)
	if err != nil {
		t.Fatalf("failed to get collection: %v", err)
	}

	if retrieved.Name != "test-collection" {
		t.Errorf("expected name 'test-collection', got '%s'", retrieved.Name)
	}
	if retrieved.Description != "A test collection" {
		t.Errorf("expected description 'A test collection', got '%s'", retrieved.Description)
	}
	if retrieved.ChunkConfig.Strategy != "fixed" {
		t.Errorf("expected chunk strategy 'fixed', got '%s'", retrieved.ChunkConfig.Strategy)
	}
}

func TestCreateCollectionDuplicate(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	col := &types.Collection{
		ID:        "col-1",
		Namespace: "test-ns",
		Name:      "test-collection",
	}

	if err := backend.CreateCollection(ctx, col); err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	// Try to create duplicate
	col2 := &types.Collection{
		ID:        "col-1",
		Namespace: "test-ns",
		Name:      "test-collection-2",
	}

	err := backend.CreateCollection(ctx, col2)
	if err != storage.ErrAlreadyExists {
		t.Errorf("expected ErrAlreadyExists, got %v", err)
	}
}

func TestGetCollectionNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	_, err := backend.GetCollection(ctx, "test-ns", "nonexistent")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListCollections(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create multiple collections with different timestamps
	baseTime := time.Now().Unix()
	for i := 0; i < 5; i++ {
		col := &types.Collection{
			Namespace: "test-ns",
			Name:      "collection-" + string(rune('A'+i)),
			CreatedAt: time.Unix(baseTime+int64(i), 0),
		}
		if err := backend.CreateCollection(ctx, col); err != nil {
			t.Fatalf("failed to create collection %d: %v", i, err)
		}
	}

	// List all collections
	collections, _, err := backend.ListCollections(ctx, "test-ns", "", 10)
	if err != nil {
		t.Fatalf("failed to list collections: %v", err)
	}
	if len(collections) != 5 {
		t.Errorf("expected 5 collections, got %d", len(collections))
	}

	// Collections should be ordered by created_at DESC (most recent first)
	if collections[0].Name != "collection-E" {
		t.Errorf("expected first collection to be 'collection-E', got '%s'", collections[0].Name)
	}

	// Test pagination
	collections, cursor, err := backend.ListCollections(ctx, "test-ns", "", 3)
	if err != nil {
		t.Fatalf("failed to list collections with limit: %v", err)
	}
	if len(collections) != 3 {
		t.Errorf("expected 3 collections with limit, got %d", len(collections))
	}
	if cursor == "" {
		t.Error("expected non-empty cursor for pagination")
	}
}

func TestDeleteCollection(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	col := &types.Collection{
		ID:        "col-1",
		Namespace: "test-ns",
		Name:      "test-collection",
	}
	if err := backend.CreateCollection(ctx, col); err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	// Delete collection
	if err := backend.DeleteCollection(ctx, "test-ns", "col-1"); err != nil {
		t.Fatalf("failed to delete collection: %v", err)
	}

	// Verify it's gone
	_, err := backend.GetCollection(ctx, "test-ns", "col-1")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteCollectionNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	err := backend.DeleteCollection(ctx, "test-ns", "nonexistent")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestInsertDocument(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create collection first
	col := &types.Collection{
		ID:        "col-1",
		Namespace: "test-ns",
		Name:      "test-collection",
	}
	if err := backend.CreateCollection(ctx, col); err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	doc := &types.Document{
		Namespace:    "test-ns",
		CollectionID: "col-1",
		Title:        "Test Document",
		Content:      "This is the document content.",
		ContentType:  "text",
		Source:       "test-source",
		Metadata: map[string]string{
			"author": "test-author",
			"tags":   "tag1,tag2",
		},
	}

	if err := backend.InsertDocument(ctx, doc); err != nil {
		t.Fatalf("failed to insert document: %v", err)
	}

	if doc.ID == "" {
		t.Error("expected document ID to be generated")
	}

	// Retrieve document
	retrieved, err := backend.GetDocument(ctx, "test-ns", doc.ID)
	if err != nil {
		t.Fatalf("failed to get document: %v", err)
	}

	if retrieved.Title != "Test Document" {
		t.Errorf("expected title 'Test Document', got '%s'", retrieved.Title)
	}
	if retrieved.Content != "This is the document content." {
		t.Errorf("expected content 'This is the document content.', got '%s'", retrieved.Content)
	}
	if retrieved.Source != "test-source" {
		t.Errorf("expected source 'test-source', got '%s'", retrieved.Source)
	}
	if retrieved.Metadata == nil {
		t.Fatal("expected metadata to be present")
	}
	if retrieved.Metadata["author"] != "test-author" {
		t.Errorf("expected author 'test-author', got '%v'", retrieved.Metadata["author"])
	}
}

func TestGetDocumentNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	_, err := backend.GetDocument(ctx, "test-ns", "nonexistent")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteDocument(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create collection and document
	col := &types.Collection{
		ID:        "col-1",
		Namespace: "test-ns",
		Name:      "test-collection",
	}
	if err := backend.CreateCollection(ctx, col); err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	doc := &types.Document{
		ID:           "doc-1",
		Namespace:    "test-ns",
		CollectionID: "col-1",
		Title:        "Test Document",
		Content:      "Content here.",
	}
	if err := backend.InsertDocument(ctx, doc); err != nil {
		t.Fatalf("failed to insert document: %v", err)
	}

	// Delete document
	if err := backend.DeleteDocument(ctx, "test-ns", "doc-1"); err != nil {
		t.Fatalf("failed to delete document: %v", err)
	}

	// Verify it's gone
	_, err := backend.GetDocument(ctx, "test-ns", "doc-1")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteDocumentNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	err := backend.DeleteDocument(ctx, "test-ns", "nonexistent")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestInsertChunks(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create collection and document
	col := &types.Collection{
		ID:        "col-1",
		Namespace: "test-ns",
		Name:      "test-collection",
	}
	if err := backend.CreateCollection(ctx, col); err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	doc := &types.Document{
		ID:           "doc-1",
		Namespace:    "test-ns",
		CollectionID: "col-1",
		Title:        "Test Document",
		Content:      "Full document content.",
	}
	if err := backend.InsertDocument(ctx, doc); err != nil {
		t.Fatalf("failed to insert document: %v", err)
	}

	// Create chunks with embeddings
	chunks := []*types.Chunk{
		{
			DocumentID:   "doc-1",
			Namespace:    "test-ns",
			CollectionID: "col-1",
			Content:      "First chunk content",
			Index:        0,
			TokenCount:   10,
			Embedding:    make([]float32, 1536),
			Metadata:     map[string]string{"position": "start"},
		},
		{
			DocumentID:   "doc-1",
			Namespace:    "test-ns",
			CollectionID: "col-1",
			Content:      "Second chunk content",
			Index:        1,
			TokenCount:   12,
			Embedding:    make([]float32, 1536),
		},
		{
			DocumentID:   "doc-1",
			Namespace:    "test-ns",
			CollectionID: "col-1",
			Content:      "Third chunk content",
			Index:        2,
			TokenCount:   11,
			Embedding:    make([]float32, 1536),
		},
	}

	// Set some embedding values
	for i := range chunks[0].Embedding {
		chunks[0].Embedding[i] = float32(i) * 0.001
	}

	if err := backend.InsertChunks(ctx, chunks); err != nil {
		t.Fatalf("failed to insert chunks: %v", err)
	}

	// Verify chunks have IDs
	for i, chunk := range chunks {
		if chunk.ID == "" {
			t.Errorf("expected chunk %d to have ID", i)
		}
	}

	// Verify chunks in database
	var count int
	err := backend.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks WHERE document_id = ?", "doc-1").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count chunks: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 chunks, got %d", count)
	}

	// Verify embeddings
	err = backend.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunk_embeddings").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count embeddings: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 embeddings, got %d", count)
	}
}

func TestInsertChunksEmpty(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Insert empty chunks should be a no-op
	err := backend.InsertChunks(ctx, []*types.Chunk{})
	if err != nil {
		t.Errorf("expected no error for empty chunks, got %v", err)
	}
}

func TestGetAdjacentChunks(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create collection and document
	col := &types.Collection{
		ID:        "col-1",
		Namespace: "test-ns",
		Name:      "test-collection",
	}
	if err := backend.CreateCollection(ctx, col); err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	doc := &types.Document{
		ID:           "doc-1",
		Namespace:    "test-ns",
		CollectionID: "col-1",
		Title:        "Test Document",
		Content:      "Full content.",
	}
	if err := backend.InsertDocument(ctx, doc); err != nil {
		t.Fatalf("failed to insert document: %v", err)
	}

	// Create 5 chunks
	chunks := make([]*types.Chunk, 5)
	for i := 0; i < 5; i++ {
		chunks[i] = &types.Chunk{
			DocumentID:   "doc-1",
			Namespace:    "test-ns",
			CollectionID: "col-1",
			Content:      "Chunk " + string(rune('A'+i)),
			Index:        i,
			TokenCount:   10,
		}
	}

	if err := backend.InsertChunks(ctx, chunks); err != nil {
		t.Fatalf("failed to insert chunks: %v", err)
	}

	// Get adjacent chunks for middle chunk (index 2) with window 1
	adjacent, err := backend.GetAdjacentChunks(ctx, chunks[2].ID, 1)
	if err != nil {
		t.Fatalf("failed to get adjacent chunks: %v", err)
	}

	// Should get chunks 1, 2, 3
	if len(adjacent) != 3 {
		t.Errorf("expected 3 adjacent chunks, got %d", len(adjacent))
	}

	// Verify they're in order
	for i, chunk := range adjacent {
		expectedIndex := i + 1 // indices 1, 2, 3
		if chunk.Index != expectedIndex {
			t.Errorf("expected chunk at position %d to have index %d, got %d", i, expectedIndex, chunk.Index)
		}
	}
}

func TestGetAdjacentChunksNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	_, err := backend.GetAdjacentChunks(ctx, "nonexistent", 1)
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCollectionStats(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create collection
	col := &types.Collection{
		ID:        "col-1",
		Namespace: "test-ns",
		Name:      "test-collection",
	}
	if err := backend.CreateCollection(ctx, col); err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	// Create documents
	for i := 0; i < 3; i++ {
		doc := &types.Document{
			Namespace:    "test-ns",
			CollectionID: "col-1",
			Title:        "Doc " + string(rune('A'+i)),
			Content:      "Content here.",
		}
		if err := backend.InsertDocument(ctx, doc); err != nil {
			t.Fatalf("failed to insert document: %v", err)
		}

		// Create chunks for each document
		chunks := []*types.Chunk{
			{
				DocumentID:   doc.ID,
				Namespace:    "test-ns",
				CollectionID: "col-1",
				Content:      "Chunk 1",
				Index:        0,
				TokenCount:   100,
			},
			{
				DocumentID:   doc.ID,
				Namespace:    "test-ns",
				CollectionID: "col-1",
				Content:      "Chunk 2",
				Index:        1,
				TokenCount:   150,
			},
		}
		if err := backend.InsertChunks(ctx, chunks); err != nil {
			t.Fatalf("failed to insert chunks: %v", err)
		}
	}

	// Get stats
	stats, err := backend.CollectionStats(ctx, "test-ns", "col-1")
	if err != nil {
		t.Fatalf("failed to get collection stats: %v", err)
	}

	if stats.DocumentCount != 3 {
		t.Errorf("expected 3 documents, got %d", stats.DocumentCount)
	}
	if stats.ChunkCount != 6 {
		t.Errorf("expected 6 chunks, got %d", stats.ChunkCount)
	}
	// 3 docs * 2 chunks each * (100 + 150) tokens = 750 tokens
	if stats.TotalTokens != 750 {
		t.Errorf("expected 750 total tokens, got %d", stats.TotalTokens)
	}
	if stats.LastIngest.IsZero() {
		t.Error("expected LastIngest to be set")
	}
}

func TestCollectionStatsNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	_, err := backend.CollectionStats(ctx, "test-ns", "nonexistent")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCollectionStatsEmpty(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create empty collection
	col := &types.Collection{
		ID:        "col-1",
		Namespace: "test-ns",
		Name:      "test-collection",
	}
	if err := backend.CreateCollection(ctx, col); err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	stats, err := backend.CollectionStats(ctx, "test-ns", "col-1")
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}

	if stats.DocumentCount != 0 {
		t.Errorf("expected 0 documents, got %d", stats.DocumentCount)
	}
	if stats.ChunkCount != 0 {
		t.Errorf("expected 0 chunks, got %d", stats.ChunkCount)
	}
	if stats.TotalTokens != 0 {
		t.Errorf("expected 0 tokens, got %d", stats.TotalTokens)
	}
}

func TestSearchChunksPlaceholder(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// SearchChunks is a placeholder until vec0 integration
	results, err := backend.SearchChunks(ctx, "test-ns", []float32{0.1, 0.2}, storage.ChunkSearchOpts{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}
