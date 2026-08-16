package conformance

import (
	"context"
	"testing"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

func testKnowledge(ctx context.Context, t *testing.T, b storage.Backend, dims int) {
	const ns = "conf-know"

	// helper: create a collection + document + chunks with embeddings
	setupKnowledge := func(t *testing.T, colID, docID string, numChunks int) {
		t.Helper()
		col := &types.Collection{
			ID:        colID,
			Namespace: ns,
			Name:      "collection-" + colID,
			ChunkConfig: types.ChunkConfig{
				Strategy:  "fixed",
				MaxTokens: 512,
				Overlap:   50,
			},
		}
		if err := b.CreateCollection(ctx, col); err != nil {
			t.Fatalf("CreateCollection: %v", err)
		}
		doc := &types.Document{
			ID:           docID,
			Namespace:    ns,
			CollectionID: colID,
			Title:        "Document " + docID,
			Content:      "Full content for " + docID,
		}
		if err := b.InsertDocument(ctx, doc); err != nil {
			t.Fatalf("InsertDocument: %v", err)
		}
		chunks := make([]*types.Chunk, numChunks)
		for i := 0; i < numChunks; i++ {
			chunks[i] = &types.Chunk{
				DocumentID:   docID,
				Namespace:    ns,
				CollectionID: colID,
				Content:      "Chunk content " + string(rune('A'+i)),
				Index:        i,
				TokenCount:   10 + i,
				Embedding:    createTestEmbedding(float32(i)*0.1, dims),
				Metadata:     map[string]string{"position": string(rune('A' + i))},
			}
		}
		if err := b.InsertChunks(ctx, chunks); err != nil {
			t.Fatalf("InsertChunks: %v", err)
		}
		for i, c := range chunks {
			if c.ID == "" {
				t.Errorf("expected chunk %d to have ID", i)
			}
		}
	}

	t.Run("CreateAndGetCollection", func(t *testing.T) {
		col := &types.Collection{
			Namespace: ns,
			Name:      "test-col-crud",
			ChunkConfig: types.ChunkConfig{
				Strategy:  "fixed",
				MaxTokens: 256,
				Overlap:   25,
			},
		}
		if err := b.CreateCollection(ctx, col); err != nil {
			t.Fatalf("CreateCollection: %v", err)
		}
		if col.ID == "" {
			t.Fatal("expected collection ID to be generated")
		}
		got, err := b.GetCollection(ctx, ns, col.ID)
		if err != nil {
			t.Fatalf("GetCollection: %v", err)
		}
		if got.Name != "test-col-crud" {
			t.Errorf("expected name 'test-col-crud', got %q", got.Name)
		}
		if got.ChunkConfig.Strategy != "fixed" {
			t.Errorf("expected strategy 'fixed', got %q", got.ChunkConfig.Strategy)
		}
	})

	t.Run("GetCollectionNotFound", func(t *testing.T) {
		_, err := b.GetCollection(ctx, ns, "nonexistent")
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("CreateCollectionDuplicate", func(t *testing.T) {
		col1 := &types.Collection{ID: "dup-col", Namespace: ns, Name: "dup-name"}
		if err := b.CreateCollection(ctx, col1); err != nil {
			t.Fatalf("CreateCollection: %v", err)
		}
		col2 := &types.Collection{ID: "dup-col2", Namespace: ns, Name: "dup-name"}
		err := b.CreateCollection(ctx, col2)
		if err != storage.ErrAlreadyExists {
			t.Errorf("expected ErrAlreadyExists, got %v", err)
		}
	})

	t.Run("ListCollections", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			if err := b.CreateCollection(ctx, &types.Collection{
				Namespace: ns,
				Name:      "list-col-" + string(rune('A'+i)),
			}); err != nil {
				t.Fatalf("CreateCollection: %v", err)
			}
		}
		cols, _, err := b.ListCollections(ctx, ns, "", 100)
		if err != nil {
			t.Fatalf("ListCollections: %v", err)
		}
		if len(cols) < 3 {
			t.Errorf("expected at least 3 collections, got %d", len(cols))
		}
	})

	t.Run("InsertAndGetDocument", func(t *testing.T) {
		setupKnowledge(t, "col-doc", "doc-crud", 1)
		got, err := b.GetDocument(ctx, ns, "doc-crud")
		if err != nil {
			t.Fatalf("GetDocument: %v", err)
		}
		if got.Title != "Document doc-crud" {
			t.Errorf("expected title 'Document doc-crud', got %q", got.Title)
		}
		// Timestamps must never read back zero — a zero time serializes as
		// the misleading "0001-01-01T00:00:00Z". (pgvector's documents
		// table tracks creation time only; its backend falls UpdatedAt
		// back to CreatedAt.)
		if got.CreatedAt.IsZero() {
			t.Error("expected CreatedAt to be populated")
		}
		if got.UpdatedAt.IsZero() {
			t.Error("expected UpdatedAt to be populated")
		}
	})

	t.Run("GetDocumentNotFound", func(t *testing.T) {
		_, err := b.GetDocument(ctx, ns, "nonexistent")
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("DeleteDocument", func(t *testing.T) {
		setupKnowledge(t, "col-del", "doc-del", 1)
		if err := b.DeleteDocument(ctx, ns, "doc-del"); err != nil {
			t.Fatalf("DeleteDocument: %v", err)
		}
		_, err := b.GetDocument(ctx, ns, "doc-del")
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound after delete, got %v", err)
		}
	})

	t.Run("InsertChunksAndAdjacent", func(t *testing.T) {
		setupKnowledge(t, "col-chunks", "doc-chunks", 3)

		// List all chunks for the document to get IDs.
		// GetAdjacentChunks takes a chunkID and window.
		// We need a chunk ID — get it by searching.
		results, err := b.SearchChunks(ctx, ns, createTestEmbedding(0.0, dims), storage.ChunkSearchOpts{
			TopK:         10,
			CollectionID: strPtr("col-chunks"),
		})
		if err != nil {
			t.Fatalf("SearchChunks: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 search results, got %d", len(results))
		}

		// Get adjacent chunks for the first result with window=1.
		adjacent, err := b.GetAdjacentChunks(ctx, results[0].Chunk.ID, 1)
		if err != nil {
			t.Fatalf("GetAdjacentChunks: %v", err)
		}
		// With 3 chunks and window=1, we should get at least the chunk itself
		// plus one neighbor.
		if len(adjacent) < 1 {
			t.Errorf("expected at least 1 adjacent chunk, got %d", len(adjacent))
		}
	})

	t.Run("InsertChunksEmpty", func(t *testing.T) {
		if err := b.InsertChunks(ctx, []*types.Chunk{}); err != nil {
			t.Errorf("expected no error for empty chunks, got %v", err)
		}
	})

	t.Run("SearchChunks", func(t *testing.T) {
		setupKnowledge(t, "col-search", "doc-search", 3)

		// Query with an embedding similar to chunk 0 (seed 0.0).
		results, err := b.SearchChunks(ctx, ns, createTestEmbedding(0.0, dims), storage.ChunkSearchOpts{
			TopK:         10,
			CollectionID: strPtr("col-search"),
		})
		if err != nil {
			t.Fatalf("SearchChunks: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}
		// Results should be ordered by similarity (descending score).
		for i := 1; i < len(results); i++ {
			if results[i].Score > results[i-1].Score {
				t.Errorf("results not ordered by score: %f > %f at index %d", results[i].Score, results[i-1].Score, i)
			}
		}
	})

	t.Run("SearchChunksCollectionFilter", func(t *testing.T) {
		setupKnowledge(t, "col-filter-a", "doc-filter-a", 2)
		setupKnowledge(t, "col-filter-b", "doc-filter-b", 2)

		results, err := b.SearchChunks(ctx, ns, createTestEmbedding(0.0, dims), storage.ChunkSearchOpts{
			TopK:         100,
			CollectionID: strPtr("col-filter-a"),
		})
		if err != nil {
			t.Fatalf("SearchChunks: %v", err)
		}
		for _, r := range results {
			if r.Chunk.CollectionID != "col-filter-a" {
				t.Errorf("expected results only from col-filter-a, got %s", r.Chunk.CollectionID)
			}
		}
	})

	t.Run("CollectionStats", func(t *testing.T) {
		setupKnowledge(t, "col-stats", "doc-stats", 3)
		stats, err := b.CollectionStats(ctx, ns, "col-stats")
		if err != nil {
			t.Fatalf("CollectionStats: %v", err)
		}
		if stats.DocumentCount != 1 {
			t.Errorf("expected 1 document, got %d", stats.DocumentCount)
		}
		if stats.ChunkCount != 3 {
			t.Errorf("expected 3 chunks, got %d", stats.ChunkCount)
		}
	})

	t.Run("DeleteCollection", func(t *testing.T) {
		setupKnowledge(t, "col-del-coll", "doc-del-coll", 1)
		if err := b.DeleteCollection(ctx, ns, "col-del-coll"); err != nil {
			t.Fatalf("DeleteCollection: %v", err)
		}
		_, err := b.GetCollection(ctx, ns, "col-del-coll")
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound after delete, got %v", err)
		}
	})
}

func strPtr(s string) *string {
	return &s
}
