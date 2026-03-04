package entity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/config"
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

func setupTestEngine(t *testing.T) (*Engine, *sqlite.Backend) {
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

	engine, err := NewEngine(backend, embProvider, &cfg.Entity)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	return engine, backend
}

func TestNewEngine(t *testing.T) {
	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	backend := sqlite.NewWithDB(db)

	// Test with valid backend
	engine, err := NewEngine(backend, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}

	// Test with nil backend
	_, err = NewEngine(nil, nil, nil)
	if err == nil {
		t.Error("expected error for nil backend")
	}
}

func TestCreateEntity(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	result, err := engine.Create(ctx, namespace, "Alice Smith", types.EntityTypePerson, &CreateOpts{
		Aliases:    []string{"Alice", "A. Smith"},
		Summary:    "Software engineer at TechCorp",
		Attributes: map[string]string{"role": "Engineer", "company": "TechCorp"},
	})

	if err != nil {
		t.Fatalf("failed to create entity: %v", err)
	}

	if !result.Created {
		t.Error("expected entity to be created")
	}
	if result.Entity.Name != "Alice Smith" {
		t.Errorf("expected name 'Alice Smith', got %s", result.Entity.Name)
	}
	if result.Entity.Type != types.EntityTypePerson {
		t.Errorf("expected type 'person', got %s", result.Entity.Type)
	}
	if len(result.Entity.Aliases) != 2 {
		t.Errorf("expected 2 aliases, got %d", len(result.Entity.Aliases))
	}
}

func TestCreateEntityDuplicate(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	// Create first time
	result1, _ := engine.Create(ctx, namespace, "Bob Jones", types.EntityTypePerson, nil)
	if !result1.Created {
		t.Error("expected entity to be created")
	}

	// Create again with same name
	result2, err := engine.Create(ctx, namespace, "Bob Jones", types.EntityTypePerson, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result2.Created {
		t.Error("expected entity to already exist")
	}
	if result2.Entity.ID != result1.Entity.ID {
		t.Error("expected same entity ID")
	}
}

func TestCreateEntityEmptyName(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.Create(context.Background(), "ns", "", types.EntityTypePerson, nil)
	if err != ErrEmptyName {
		t.Errorf("expected ErrEmptyName, got %v", err)
	}
}

func TestCreateEntityInvalidType(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.Create(context.Background(), "ns", "Test", "invalid_type", nil)
	if err == nil {
		t.Error("expected error for invalid type")
	}
}

func TestGetEntity(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	created, _ := engine.Create(ctx, namespace, "Charlie", types.EntityTypePerson, nil)

	entity, err := engine.Get(ctx, namespace, created.Entity.ID)
	if err != nil {
		t.Fatalf("failed to get entity: %v", err)
	}

	if entity.Name != "Charlie" {
		t.Errorf("expected name 'Charlie', got %s", entity.Name)
	}
}

func TestGetEntityNotFound(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.Get(context.Background(), "ns", "nonexistent")
	if err != ErrEntityNotFound {
		t.Errorf("expected ErrEntityNotFound, got %v", err)
	}
}

func TestGetByName(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	engine.Create(ctx, namespace, "Diana Prince", types.EntityTypePerson, nil)

	entity, err := engine.GetByName(ctx, namespace, "Diana Prince")
	if err != nil {
		t.Fatalf("failed to get by name: %v", err)
	}

	if entity.Name != "Diana Prince" {
		t.Errorf("expected name 'Diana Prince', got %s", entity.Name)
	}
}

func TestResolve(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	result, _ := engine.Create(ctx, namespace, "Edward Norton", types.EntityTypePerson, &CreateOpts{
		Aliases: []string{"Ed Norton", "E. Norton"},
	})

	// Resolve by canonical name
	entity, err := engine.Resolve(ctx, namespace, "Edward Norton")
	if err != nil {
		t.Fatalf("failed to resolve by name: %v", err)
	}
	if entity.ID != result.Entity.ID {
		t.Error("expected same entity")
	}

	// Resolve by alias
	entity, err = engine.Resolve(ctx, namespace, "Ed Norton")
	if err != nil {
		t.Fatalf("failed to resolve by alias: %v", err)
	}
	if entity.ID != result.Entity.ID {
		t.Error("expected same entity by alias")
	}
}

func TestResolveNotFound(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.Resolve(context.Background(), "ns", "Unknown Person")
	if err != ErrEntityNotFound {
		t.Errorf("expected ErrEntityNotFound, got %v", err)
	}
}

func TestUpdateEntity(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	created, _ := engine.Create(ctx, namespace, "Frank", types.EntityTypePerson, &CreateOpts{
		Attributes: map[string]string{"role": "Developer"},
	})

	newSummary := "Senior developer at BigCorp"
	updated, err := engine.Update(ctx, namespace, created.Entity.ID, UpdateOpts{
		Summary:    &newSummary,
		Attributes: map[string]string{"level": "Senior"},
	})

	if err != nil {
		t.Fatalf("failed to update: %v", err)
	}

	if updated.Summary != newSummary {
		t.Errorf("expected summary '%s', got '%s'", newSummary, updated.Summary)
	}
	if updated.Attributes["role"] != "Developer" {
		t.Error("expected existing attribute to be preserved")
	}
	if updated.Attributes["level"] != "Senior" {
		t.Error("expected new attribute to be added")
	}
}

func TestUpdateEntityNotFound(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	_, err := engine.Update(context.Background(), "ns", "nonexistent", UpdateOpts{})
	if err != ErrEntityNotFound {
		t.Errorf("expected ErrEntityNotFound, got %v", err)
	}
}

func TestDeleteEntity(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	created, _ := engine.Create(ctx, namespace, "George", types.EntityTypePerson, nil)

	err := engine.Delete(ctx, namespace, created.Entity.ID)
	if err != nil {
		t.Fatalf("failed to delete: %v", err)
	}

	// Verify it's gone
	_, err = engine.Get(ctx, namespace, created.Entity.ID)
	if err != ErrEntityNotFound {
		t.Errorf("expected ErrEntityNotFound after delete, got %v", err)
	}
}

func TestListEntities(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	// Create multiple entities
	engine.Create(ctx, namespace, "Apple Inc", types.EntityTypeOrganization, nil)
	engine.Create(ctx, namespace, "Bob Builder", types.EntityTypePerson, nil)
	engine.Create(ctx, namespace, "Microsoft", types.EntityTypeOrganization, nil)

	// List all
	result, err := engine.List(ctx, namespace, nil)
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}
	if result.Count != 3 {
		t.Errorf("expected 3 entities, got %d", result.Count)
	}

	// List by type
	orgType := types.EntityTypeOrganization
	result2, err := engine.List(ctx, namespace, &ListOpts{
		EntityType: &orgType,
	})
	if err != nil {
		t.Fatalf("failed to list by type: %v", err)
	}
	if result2.Count != 2 {
		t.Errorf("expected 2 organizations, got %d", result2.Count)
	}
}

func TestSearchEntities(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	// Create entities with summaries
	engine.Create(ctx, namespace, "TechCorp", types.EntityTypeOrganization, &CreateOpts{
		Summary: "A technology company specializing in AI and machine learning",
	})
	engine.Create(ctx, namespace, "FoodMart", types.EntityTypeOrganization, &CreateOpts{
		Summary: "A grocery store chain in the midwest",
	})

	// Search
	result, err := engine.Search(ctx, namespace, "artificial intelligence company", nil)
	if err != nil {
		t.Fatalf("failed to search: %v", err)
	}

	if result.TotalFound == 0 {
		t.Error("expected at least one result")
	}
}

func TestSearchEntitiesNoProvider(t *testing.T) {
	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	backend := sqlite.NewWithDB(db)
	backend.Migrate(context.Background())

	// Create engine without embedding provider
	engine, _ := NewEngine(backend, nil, nil)

	_, err := engine.Search(context.Background(), "ns", "query", nil)
	if err != ErrEmbeddingRequired {
		t.Errorf("expected ErrEmbeddingRequired, got %v", err)
	}
}

func TestAddAlias(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	created, _ := engine.Create(ctx, namespace, "International Business Machines", types.EntityTypeOrganization, nil)

	err := engine.AddAlias(ctx, namespace, created.Entity.ID, "IBM")
	if err != nil {
		t.Fatalf("failed to add alias: %v", err)
	}

	// Should be resolvable by alias
	entity, err := engine.Resolve(ctx, namespace, "IBM")
	if err != nil {
		t.Fatalf("failed to resolve by new alias: %v", err)
	}
	if entity.ID != created.Entity.ID {
		t.Error("expected same entity")
	}
}

func TestRecordMention(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	created, _ := engine.Create(ctx, namespace, "Hannah", types.EntityTypePerson, nil)

	err := engine.RecordMention(ctx, namespace, created.Entity.ID, "conversation", "msg-123", &MentionOpts{
		Context: "Hannah mentioned in conversation about project planning",
		Snippet: "Hannah",
	})

	if err != nil {
		t.Fatalf("failed to record mention: %v", err)
	}

	// Check mention count increased
	entity, _ := engine.Get(ctx, namespace, created.Entity.ID)
	if entity.MentionCount != 1 {
		t.Errorf("expected mention count 1, got %d", entity.MentionCount)
	}

	// Get mentions
	mentions, err := engine.GetMentions(ctx, created.Entity.ID, 10)
	if err != nil {
		t.Fatalf("failed to get mentions: %v", err)
	}
	if len(mentions) != 1 {
		t.Errorf("expected 1 mention, got %d", len(mentions))
	}
}

func TestRecordMentionEntityNotFound(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	err := engine.RecordMention(context.Background(), "ns", "nonexistent", "conversation", "msg-1", nil)
	if err != ErrEntityNotFound {
		t.Errorf("expected ErrEntityNotFound, got %v", err)
	}
}

func TestAddRelationship(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	alice, _ := engine.Create(ctx, namespace, "Alice", types.EntityTypePerson, nil)
	corp, _ := engine.Create(ctx, namespace, "TechCorp", types.EntityTypeOrganization, nil)

	rel, err := engine.AddRelationship(ctx, namespace, alice.Entity.ID, corp.Entity.ID, "works_at", &RelationshipOpts{
		Description: "Alice is a senior engineer at TechCorp",
		Confidence:  0.95,
	})

	if err != nil {
		t.Fatalf("failed to add relationship: %v", err)
	}

	if rel.RelationType != "works_at" {
		t.Errorf("expected relation type 'works_at', got %s", rel.RelationType)
	}
	if rel.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", rel.Confidence)
	}
}

func TestGetRelationships(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	alice, _ := engine.Create(ctx, namespace, "Alice", types.EntityTypePerson, nil)
	corp1, _ := engine.Create(ctx, namespace, "Corp1", types.EntityTypeOrganization, nil)
	corp2, _ := engine.Create(ctx, namespace, "Corp2", types.EntityTypeOrganization, nil)

	engine.AddRelationship(ctx, namespace, alice.Entity.ID, corp1.Entity.ID, "works_at", nil)
	engine.AddRelationship(ctx, namespace, alice.Entity.ID, corp2.Entity.ID, "advises", nil)

	rels, err := engine.GetRelationships(ctx, namespace, alice.Entity.ID, nil)
	if err != nil {
		t.Fatalf("failed to get relationships: %v", err)
	}

	if len(rels) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(rels))
	}
}

func TestMergeEntities(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	// Create two entities representing same person
	alice1, _ := engine.Create(ctx, namespace, "Alice Smith", types.EntityTypePerson, &CreateOpts{
		Aliases:    []string{"Alice"},
		Attributes: map[string]string{"email": "alice@example.com"},
	})
	alice2, _ := engine.Create(ctx, namespace, "A. Smith", types.EntityTypePerson, &CreateOpts{
		Attributes: map[string]string{"phone": "555-1234"},
	})

	// Record mentions for both
	engine.RecordMention(ctx, namespace, alice1.Entity.ID, "conversation", "msg-1", nil)
	engine.RecordMention(ctx, namespace, alice2.Entity.ID, "conversation", "msg-2", nil)

	// Merge alice2 into alice1
	result, err := engine.Merge(ctx, namespace, alice2.Entity.ID, alice1.Entity.ID)
	if err != nil {
		t.Fatalf("failed to merge: %v", err)
	}

	// Check merged entity
	if result.KeptEntity.Name != "Alice Smith" {
		t.Errorf("expected name 'Alice Smith', got %s", result.KeptEntity.Name)
	}

	// Check attributes merged
	if result.KeptEntity.Attributes["email"] != "alice@example.com" {
		t.Error("expected email attribute to be preserved")
	}
	if result.KeptEntity.Attributes["phone"] != "555-1234" {
		t.Error("expected phone attribute to be merged")
	}

	// Check source entity is gone
	_, err = engine.Get(ctx, namespace, alice2.Entity.ID)
	if err != ErrEntityNotFound {
		t.Errorf("expected source entity to be deleted, got %v", err)
	}
}

func TestMergeSelfError(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	entity, _ := engine.Create(ctx, namespace, "Test", types.EntityTypePerson, nil)

	_, err := engine.Merge(ctx, namespace, entity.Entity.ID, entity.Entity.ID)
	if err != ErrSelfMerge {
		t.Errorf("expected ErrSelfMerge, got %v", err)
	}
}

func TestExtractionQueue(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	// Enqueue
	err := engine.EnqueueExtraction(ctx, namespace, "conversation", "msg-123", "Alice talked to Bob about the project.")
	if err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	// Dequeue
	items, err := engine.DequeueExtraction(ctx, 10)
	if err != nil {
		t.Fatalf("failed to dequeue: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item, got %d", len(items))
	}

	// Complete
	err = engine.CompleteExtraction(ctx, items[0].ID, "completed")
	if err != nil {
		t.Fatalf("failed to complete: %v", err)
	}

	// Stats
	stats, err := engine.ExtractionQueueStats(ctx)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.PendingCount != 0 {
		t.Errorf("expected 0 pending, got %d", stats.PendingCount)
	}
}

func TestExtractionQueueInvalidStatus(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	err := engine.CompleteExtraction(context.Background(), 1, "invalid")
	if err == nil {
		t.Error("expected error for invalid status")
	}
}

func TestQuery(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	alice, _ := engine.Create(ctx, namespace, "Alice Query", types.EntityTypePerson, nil)
	corp, _ := engine.Create(ctx, namespace, "QueryCorp", types.EntityTypeOrganization, nil)

	engine.RecordMention(ctx, namespace, alice.Entity.ID, "conversation", "msg-1", &MentionOpts{
		Context: "Alice mentioned in meeting",
	})
	engine.AddRelationship(ctx, namespace, alice.Entity.ID, corp.Entity.ID, "works_at", nil)

	result, err := engine.Query(ctx, namespace, "Alice Query", 10)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}

	if !result.Found {
		t.Error("expected entity to be found")
	}
	if result.Entity.Name != "Alice Query" {
		t.Errorf("expected name 'Alice Query', got %s", result.Entity.Name)
	}
	if len(result.Mentions) != 1 {
		t.Errorf("expected 1 mention, got %d", len(result.Mentions))
	}
	if len(result.Relationships) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(result.Relationships))
	}
}

func TestQueryNotFound(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	result, err := engine.Query(context.Background(), "ns", "Unknown", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Found {
		t.Error("expected entity to not be found")
	}
}

func TestEntityTimestamps(t *testing.T) {
	engine, backend := setupTestEngine(t)
	defer backend.Close()

	ctx := context.Background()
	namespace := "test-ns"

	before := time.Now().Add(-time.Second)
	created, _ := engine.Create(ctx, namespace, "Timestamp Test", types.EntityTypePerson, nil)
	after := time.Now().Add(time.Second)

	if created.Entity.FirstSeenAt.Before(before) || created.Entity.FirstSeenAt.After(after) {
		t.Error("first_seen_at not in expected range")
	}
	if created.Entity.LastSeenAt.Before(before) || created.Entity.LastSeenAt.After(after) {
		t.Error("last_seen_at not in expected range")
	}
}
