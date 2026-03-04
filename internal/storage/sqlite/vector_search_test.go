package sqlite

import (
	"context"
	"database/sql"
	"math"
	"testing"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// Helper to create a normalized embedding vector
func normalizeVector(v []float32) []float32 {
	var sum float32
	for _, val := range v {
		sum += val * val
	}
	norm := float32(math.Sqrt(float64(sum)))
	result := make([]float32, len(v))
	for i, val := range v {
		result[i] = val / norm
	}
	return result
}

// Helper to create a test embedding (simple pattern for testing)
func createTestEmbedding(seed float32, dims int) []float32 {
	embedding := make([]float32, dims)
	for i := 0; i < dims; i++ {
		embedding[i] = seed + float32(i)*0.01
	}
	return normalizeVector(embedding)
}

func TestSearchMessages(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	backend := NewWithDB(db)
	ctx := context.Background()

	if err := backend.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	namespace := "test-ns"
	threadID := "thread-1"

	// Create test messages with embeddings
	messages := []*types.Message{
		{ID: "msg-1", Namespace: namespace, ThreadID: threadID, Role: "user", Content: "Hello, how are you?"},
		{ID: "msg-2", Namespace: namespace, ThreadID: threadID, Role: "assistant", Content: "I am doing well, thank you!"},
		{ID: "msg-3", Namespace: namespace, ThreadID: threadID, Role: "user", Content: "Tell me about machine learning"},
	}

	// Embeddings: msg-1 and msg-2 are similar (greetings), msg-3 is different (ML topic)
	embeddings := [][]float32{
		createTestEmbedding(0.1, 128), // Greeting 1
		createTestEmbedding(0.15, 128), // Greeting 2 (similar to 1)
		createTestEmbedding(0.9, 128), // ML topic (different)
	}

	for i, msg := range messages {
		if err := backend.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("failed to append message: %v", err)
		}
		if err := backend.StoreMessageEmbedding(ctx, msg.ID, embeddings[i]); err != nil {
			t.Fatalf("failed to store embedding: %v", err)
		}
	}

	t.Run("basic search", func(t *testing.T) {
		// Search with an embedding similar to greetings
		queryEmbedding := createTestEmbedding(0.12, 128) // Between 0.1 and 0.15

		results, err := backend.SearchMessages(ctx, namespace, queryEmbedding, storage.MessageSearchOpts{TopK: 10})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}

		if len(results) != 3 {
			t.Errorf("expected 3 results, got %d", len(results))
		}

		// The greeting messages should be ranked higher (lower distance)
		// since our query embedding is closer to them
		for i, result := range results {
			t.Logf("Result %d: msg=%s score=%.4f", i, result.Message.ID, result.Score)
		}

		// Verify first two results are the greeting messages
		if results[0].Message.ID != "msg-1" && results[0].Message.ID != "msg-2" {
			t.Errorf("expected first result to be a greeting message, got %s", results[0].Message.ID)
		}
	})

	t.Run("filter by thread", func(t *testing.T) {
		// Create a message in a different thread
		otherMsg := &types.Message{ID: "msg-other", Namespace: namespace, ThreadID: "thread-2", Role: "user", Content: "Other thread"}
		if err := backend.AppendMessage(ctx, otherMsg); err != nil {
			t.Fatalf("failed to append message: %v", err)
		}
		if err := backend.StoreMessageEmbedding(ctx, otherMsg.ID, createTestEmbedding(0.1, 128)); err != nil {
			t.Fatalf("failed to store embedding: %v", err)
		}

		// Search with thread filter
		queryEmbedding := createTestEmbedding(0.12, 128)
		results, err := backend.SearchMessages(ctx, namespace, queryEmbedding, storage.MessageSearchOpts{
			TopK:     10,
			ThreadID: &threadID,
		})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}

		// Should not include the message from thread-2
		for _, r := range results {
			if r.Message.ThreadID != threadID {
				t.Errorf("expected all results to be from %s, got %s", threadID, r.Message.ThreadID)
			}
		}
	})

	t.Run("min score filter", func(t *testing.T) {
		queryEmbedding := createTestEmbedding(0.12, 128)
		results, err := backend.SearchMessages(ctx, namespace, queryEmbedding, storage.MessageSearchOpts{
			TopK:     10,
			MinScore: 0.99, // Very high threshold
		})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}

		// Most results should be filtered out
		t.Logf("Results with high min score: %d", len(results))
	})

	t.Run("topK limit", func(t *testing.T) {
		queryEmbedding := createTestEmbedding(0.12, 128)
		results, err := backend.SearchMessages(ctx, namespace, queryEmbedding, storage.MessageSearchOpts{
			TopK: 2,
		})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("expected 2 results (topK=2), got %d", len(results))
		}
	})
}

func TestSearchChunks(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	backend := NewWithDB(db)
	ctx := context.Background()

	if err := backend.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	namespace := "test-ns"
	collectionID := "col-1"

	// Create collection and document
	col := &types.Collection{
		ID:        collectionID,
		Namespace: namespace,
		Name:      "Test Collection",
	}
	if err := backend.CreateCollection(ctx, col); err != nil {
		t.Fatalf("failed to create collection: %v", err)
	}

	doc := &types.Document{
		ID:           "doc-1",
		Namespace:    namespace,
		CollectionID: collectionID,
		Title:        "Test Document",
		Content:      "Full document content",
	}
	if err := backend.InsertDocument(ctx, doc); err != nil {
		t.Fatalf("failed to insert document: %v", err)
	}

	// Create chunks with different embeddings
	chunks := []*types.Chunk{
		{
			ID:           "chunk-1",
			DocumentID:   "doc-1",
			Namespace:    namespace,
			CollectionID: collectionID,
			Content:      "Introduction to machine learning",
			Index:        0,
			Embedding:    createTestEmbedding(0.2, 128),
			Metadata:     map[string]string{"topic": "ml"},
		},
		{
			ID:           "chunk-2",
			DocumentID:   "doc-1",
			Namespace:    namespace,
			CollectionID: collectionID,
			Content:      "Deep learning neural networks",
			Index:        1,
			Embedding:    createTestEmbedding(0.25, 128), // Similar to chunk-1
			Metadata:     map[string]string{"topic": "dl"},
		},
		{
			ID:           "chunk-3",
			DocumentID:   "doc-1",
			Namespace:    namespace,
			CollectionID: collectionID,
			Content:      "History of ancient civilizations",
			Index:        2,
			Embedding:    createTestEmbedding(0.8, 128), // Different topic
			Metadata:     map[string]string{"topic": "history"},
		},
	}

	if err := backend.InsertChunks(ctx, chunks); err != nil {
		t.Fatalf("failed to insert chunks: %v", err)
	}

	t.Run("basic search", func(t *testing.T) {
		// Search for ML-related content
		queryEmbedding := createTestEmbedding(0.22, 128) // Between chunk-1 and chunk-2

		results, err := backend.SearchChunks(ctx, namespace, queryEmbedding, storage.ChunkSearchOpts{TopK: 10})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}

		if len(results) != 3 {
			t.Errorf("expected 3 results, got %d", len(results))
		}

		// ML chunks should rank higher
		for i, result := range results {
			t.Logf("Result %d: chunk=%s score=%.4f content=%s",
				i, result.Chunk.ID, result.Score, result.Chunk.Content[:20])
		}

		// First two should be ML-related
		if results[0].Chunk.ID != "chunk-1" && results[0].Chunk.ID != "chunk-2" {
			t.Errorf("expected first result to be ML chunk, got %s", results[0].Chunk.ID)
		}
	})

	t.Run("filter by collection", func(t *testing.T) {
		// Create another collection with a chunk
		col2 := &types.Collection{ID: "col-2", Namespace: namespace, Name: "Other Collection"}
		if err := backend.CreateCollection(ctx, col2); err != nil {
			t.Fatalf("failed to create collection: %v", err)
		}

		doc2 := &types.Document{ID: "doc-2", Namespace: namespace, CollectionID: "col-2", Content: "Other doc"}
		if err := backend.InsertDocument(ctx, doc2); err != nil {
			t.Fatalf("failed to insert document: %v", err)
		}

		chunk4 := &types.Chunk{
			ID:           "chunk-4",
			DocumentID:   "doc-2",
			Namespace:    namespace,
			CollectionID: "col-2",
			Content:      "Other collection content",
			Index:        0,
			Embedding:    createTestEmbedding(0.22, 128),
		}
		if err := backend.InsertChunks(ctx, []*types.Chunk{chunk4}); err != nil {
			t.Fatalf("failed to insert chunk: %v", err)
		}

		// Search with collection filter
		queryEmbedding := createTestEmbedding(0.22, 128)
		results, err := backend.SearchChunks(ctx, namespace, queryEmbedding, storage.ChunkSearchOpts{
			TopK:         10,
			CollectionID: &collectionID, // Only col-1
		})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}

		// Should not include chunk-4
		for _, r := range results {
			if r.Chunk.CollectionID != collectionID {
				t.Errorf("expected all results to be from %s, got %s", collectionID, r.Chunk.CollectionID)
			}
		}
	})

	t.Run("metadata filter", func(t *testing.T) {
		queryEmbedding := createTestEmbedding(0.5, 128) // Neutral query
		results, err := backend.SearchChunks(ctx, namespace, queryEmbedding, storage.ChunkSearchOpts{
			TopK:    10,
			Filters: map[string]string{"topic": "ml"},
		})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}

		// Should only include chunk-1
		for _, r := range results {
			if r.Chunk.Metadata["topic"] != "ml" {
				t.Errorf("expected topic=ml, got %s", r.Chunk.Metadata["topic"])
			}
		}
	})

	t.Run("includes document metadata", func(t *testing.T) {
		queryEmbedding := createTestEmbedding(0.22, 128)
		// Filter by collection to get chunks from the test document (not from other collection added above)
		results, err := backend.SearchChunks(ctx, namespace, queryEmbedding, storage.ChunkSearchOpts{
			TopK:         1,
			CollectionID: &collectionID,
		})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}

		if len(results) == 0 {
			t.Fatal("expected at least 1 result")
		}

		if results[0].DocumentTitle != "Test Document" {
			t.Errorf("expected document title 'Test Document', got '%s'", results[0].DocumentTitle)
		}
	})
}

func TestSearchEntities(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	backend := NewWithDB(db)
	ctx := context.Background()

	if err := backend.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	namespace := "test-ns"

	// Create entities with different types and embeddings
	entities := []*types.Entity{
		{
			ID:        "entity-1",
			Namespace: namespace,
			Name:      "OpenAI",
			Type:      types.EntityTypeOrganization,
			Summary:   "AI research company",
		},
		{
			ID:        "entity-2",
			Namespace: namespace,
			Name:      "Google DeepMind",
			Type:      types.EntityTypeOrganization,
			Summary:   "AI research lab",
		},
		{
			ID:        "entity-3",
			Namespace: namespace,
			Name:      "Paris",
			Type:      types.EntityTypeLocation,
			Summary:   "Capital of France",
		},
	}

	// Similar embeddings for AI companies, different for location
	embeddings := [][]float32{
		createTestEmbedding(0.3, 128), // AI company 1
		createTestEmbedding(0.35, 128), // AI company 2 (similar)
		createTestEmbedding(0.9, 128), // Location (different)
	}

	for i, entity := range entities {
		if err := backend.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("failed to upsert entity: %v", err)
		}
		if err := backend.StoreEntityEmbedding(ctx, entity.ID, embeddings[i]); err != nil {
			t.Fatalf("failed to store embedding: %v", err)
		}
	}

	t.Run("basic search", func(t *testing.T) {
		// Search for AI companies
		queryEmbedding := createTestEmbedding(0.32, 128)

		results, err := backend.SearchEntities(ctx, namespace, queryEmbedding, storage.EntitySearchOpts{TopK: 10})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}

		if len(results) != 3 {
			t.Errorf("expected 3 results, got %d", len(results))
		}

		// AI companies should rank higher
		for i, result := range results {
			t.Logf("Result %d: entity=%s score=%.4f type=%s",
				i, result.Entity.Name, result.Score, result.Entity.Type)
		}

		// First two should be AI companies
		if results[0].Entity.Type != types.EntityTypeOrganization {
			t.Errorf("expected first result to be organization, got %s", results[0].Entity.Type)
		}
	})

	t.Run("filter by type", func(t *testing.T) {
		queryEmbedding := createTestEmbedding(0.5, 128)
		orgType := types.EntityTypeOrganization
		results, err := backend.SearchEntities(ctx, namespace, queryEmbedding, storage.EntitySearchOpts{
			TopK:       10,
			EntityType: &orgType,
		})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}

		// Should only include organizations
		for _, r := range results {
			if r.Entity.Type != types.EntityTypeOrganization {
				t.Errorf("expected organization, got %s", r.Entity.Type)
			}
		}

		if len(results) != 2 {
			t.Errorf("expected 2 organizations, got %d", len(results))
		}
	})

	t.Run("min score filter", func(t *testing.T) {
		queryEmbedding := createTestEmbedding(0.32, 128)
		results, err := backend.SearchEntities(ctx, namespace, queryEmbedding, storage.EntitySearchOpts{
			TopK:     10,
			MinScore: 0.999, // Very high threshold
		})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}

		// Most results should be filtered out
		t.Logf("Results with high min score: %d", len(results))
	})
}

func TestVectorEncoding(t *testing.T) {
	// Test the binary encoding/decoding of vectors
	original := []float32{1.0, 2.5, -3.14, 0.0, 100.99}

	encoded := encodeVectorBinary(original)
	decoded := decodeVectorBinary(encoded)

	if len(decoded) != len(original) {
		t.Fatalf("length mismatch: expected %d, got %d", len(original), len(decoded))
	}

	for i := range original {
		if math.Abs(float64(decoded[i]-original[i])) > 1e-6 {
			t.Errorf("value mismatch at %d: expected %f, got %f", i, original[i], decoded[i])
		}
	}
}

func TestSearchEmptyResults(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	backend := NewWithDB(db)
	ctx := context.Background()

	if err := backend.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	namespace := "test-ns"
	queryEmbedding := createTestEmbedding(0.5, 128)

	t.Run("empty message search", func(t *testing.T) {
		results, err := backend.SearchMessages(ctx, namespace, queryEmbedding, storage.MessageSearchOpts{TopK: 10})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})

	t.Run("empty chunk search", func(t *testing.T) {
		results, err := backend.SearchChunks(ctx, namespace, queryEmbedding, storage.ChunkSearchOpts{TopK: 10})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})

	t.Run("empty entity search", func(t *testing.T) {
		results, err := backend.SearchEntities(ctx, namespace, queryEmbedding, storage.EntitySearchOpts{TopK: 10})
		if err != nil {
			t.Fatalf("failed to search: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})
}
