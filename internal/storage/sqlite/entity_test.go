package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

func TestUpsertEntity(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	entity := &types.Entity{
		Namespace:  "test-ns",
		Name:       "John Smith",
		Type:       types.EntityTypePerson,
		Aliases:    []string{"John", "J. Smith"},
		Summary:    "CEO of Example Corp",
		Attributes: map[string]string{"role": "CEO", "company": "Example Corp"},
		Metadata:   map[string]string{"source": "manual"},
	}

	// Insert entity
	if err := backend.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("failed to upsert entity: %v", err)
	}

	if entity.ID == "" {
		t.Error("expected entity ID to be generated")
	}

	// Retrieve entity
	retrieved, err := backend.GetEntityByID(ctx, "test-ns", entity.ID)
	if err != nil {
		t.Fatalf("failed to get entity: %v", err)
	}

	if retrieved.Name != "John Smith" {
		t.Errorf("expected name 'John Smith', got '%s'", retrieved.Name)
	}
	if retrieved.Type != types.EntityTypePerson {
		t.Errorf("expected type 'person', got '%s'", retrieved.Type)
	}
	if len(retrieved.Aliases) != 2 {
		t.Errorf("expected 2 aliases, got %d", len(retrieved.Aliases))
	}
	if retrieved.Summary != "CEO of Example Corp" {
		t.Errorf("expected summary 'CEO of Example Corp', got '%s'", retrieved.Summary)
	}
	if retrieved.Attributes["role"] != "CEO" {
		t.Errorf("expected attribute role='CEO', got '%s'", retrieved.Attributes["role"])
	}
}

func TestUpsertEntityUpdate(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	entity := &types.Entity{
		Namespace: "test-ns",
		Name:      "Test Entity",
		Type:      types.EntityTypePerson,
		Summary:   "Initial summary",
	}

	if err := backend.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("failed to insert entity: %v", err)
	}

	// Update entity
	entity.Summary = "Updated summary"
	entity.Aliases = []string{"Test", "TE"}
	if err := backend.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("failed to update entity: %v", err)
	}

	// Verify update
	retrieved, err := backend.GetEntityByID(ctx, "test-ns", entity.ID)
	if err != nil {
		t.Fatalf("failed to get entity: %v", err)
	}

	if retrieved.Summary != "Updated summary" {
		t.Errorf("expected summary 'Updated summary', got '%s'", retrieved.Summary)
	}
	if len(retrieved.Aliases) != 2 {
		t.Errorf("expected 2 aliases, got %d", len(retrieved.Aliases))
	}
}

func TestGetEntityByName(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	entity := &types.Entity{
		Namespace: "test-ns",
		Name:      "Acme Corporation",
		Type:      types.EntityTypeOrganization,
	}

	if err := backend.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("failed to upsert entity: %v", err)
	}

	// Test case-insensitive lookup
	retrieved, err := backend.GetEntityByName(ctx, "test-ns", "acme corporation")
	if err != nil {
		t.Fatalf("failed to get entity by name: %v", err)
	}

	if retrieved.Name != "Acme Corporation" {
		t.Errorf("expected name 'Acme Corporation', got '%s'", retrieved.Name)
	}
}

func TestGetEntityByNameNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	_, err := backend.GetEntityByName(ctx, "test-ns", "nonexistent")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestResolveAlias(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	entity := &types.Entity{
		Namespace: "test-ns",
		Name:      "International Business Machines",
		Type:      types.EntityTypeOrganization,
		Aliases:   []string{"IBM", "Big Blue"},
	}

	if err := backend.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("failed to upsert entity: %v", err)
	}

	// Resolve by alias (case-insensitive)
	retrieved, err := backend.ResolveAlias(ctx, "test-ns", "ibm")
	if err != nil {
		t.Fatalf("failed to resolve alias: %v", err)
	}

	if retrieved.Name != "International Business Machines" {
		t.Errorf("expected name 'International Business Machines', got '%s'", retrieved.Name)
	}

	// Resolve by canonical name
	retrieved, err = backend.ResolveAlias(ctx, "test-ns", "international business machines")
	if err != nil {
		t.Fatalf("failed to resolve by canonical name: %v", err)
	}

	if retrieved.ID != entity.ID {
		t.Errorf("expected same entity ID")
	}
}

func TestResolveAliasNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	_, err := backend.ResolveAlias(ctx, "test-ns", "unknown")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListEntities(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create multiple entities
	entities := []struct {
		name      string
		entType   types.EntityType
		mentions  int64
	}{
		{"Alice", types.EntityTypePerson, 10},
		{"Bob", types.EntityTypePerson, 5},
		{"Acme Corp", types.EntityTypeOrganization, 15},
		{"Tech Inc", types.EntityTypeOrganization, 8},
		{"New York", types.EntityTypeLocation, 20},
	}

	baseTime := time.Now().Unix()
	for i, e := range entities {
		entity := &types.Entity{
			Namespace:    "test-ns",
			Name:         e.name,
			Type:         e.entType,
			MentionCount: e.mentions,
			FirstSeenAt:  time.Unix(baseTime+int64(i), 0),
			LastSeenAt:   time.Unix(baseTime+int64(i), 0),
		}
		if err := backend.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("failed to upsert entity: %v", err)
		}
	}

	// List all entities sorted by name
	result, _, err := backend.ListEntities(ctx, "test-ns", storage.EntityListOpts{
		SortBy: types.EntitySortByName,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("failed to list entities: %v", err)
	}
	if len(result) != 5 {
		t.Errorf("expected 5 entities, got %d", len(result))
	}
	// Should be sorted alphabetically
	if result[0].Name != "Acme Corp" {
		t.Errorf("expected first entity 'Acme Corp', got '%s'", result[0].Name)
	}

	// List by type
	personType := types.EntityTypePerson
	result, _, err = backend.ListEntities(ctx, "test-ns", storage.EntityListOpts{
		EntityType: &personType,
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("failed to list by type: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 person entities, got %d", len(result))
	}

	// List sorted by mention count
	result, _, err = backend.ListEntities(ctx, "test-ns", storage.EntityListOpts{
		SortBy: types.EntitySortByMentionCount,
		Limit:  3,
	})
	if err != nil {
		t.Fatalf("failed to list by mentions: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("expected 3 entities, got %d", len(result))
	}
	// New York should be first (20 mentions)
	if result[0].Name != "New York" {
		t.Errorf("expected 'New York' first, got '%s'", result[0].Name)
	}
}

func TestListEntitiesPagination(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create 5 entities
	for i := 0; i < 5; i++ {
		entity := &types.Entity{
			Namespace: "test-ns",
			Name:      "Entity" + string(rune('A'+i)),
			Type:      types.EntityTypeConcept,
		}
		if err := backend.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("failed to upsert entity: %v", err)
		}
	}

	// Get first page
	result, cursor, err := backend.ListEntities(ctx, "test-ns", storage.EntityListOpts{
		SortBy: types.EntitySortByName,
		Limit:  3,
	})
	if err != nil {
		t.Fatalf("failed to list entities: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("expected 3 entities, got %d", len(result))
	}
	if cursor == "" {
		t.Error("expected non-empty cursor")
	}

	// Get second page
	result, _, err = backend.ListEntities(ctx, "test-ns", storage.EntityListOpts{
		SortBy: types.EntitySortByName,
		Limit:  3,
		Cursor: cursor,
	})
	if err != nil {
		t.Fatalf("failed to list second page: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 entities, got %d", len(result))
	}
}

func TestDeleteEntity(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	entity := &types.Entity{
		ID:        "ent-1",
		Namespace: "test-ns",
		Name:      "To Delete",
		Type:      types.EntityTypePerson,
	}

	if err := backend.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("failed to upsert entity: %v", err)
	}

	// Delete entity
	if err := backend.DeleteEntity(ctx, "test-ns", "ent-1"); err != nil {
		t.Fatalf("failed to delete entity: %v", err)
	}

	// Verify it's gone
	_, err := backend.GetEntityByID(ctx, "test-ns", "ent-1")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteEntityNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	err := backend.DeleteEntity(ctx, "test-ns", "nonexistent")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestInsertAndGetMentions(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	entity := &types.Entity{
		ID:        "ent-1",
		Namespace: "test-ns",
		Name:      "Test Entity",
		Type:      types.EntityTypePerson,
	}
	if err := backend.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("failed to upsert entity: %v", err)
	}

	// Insert mentions
	for i := 0; i < 5; i++ {
		mention := &types.EntityMention{
			EntityID:   "ent-1",
			Namespace:  "test-ns",
			SourceType: "conversation",
			SourceID:   "msg-" + string(rune('A'+i)),
			Context:    "The context around the mention",
			Snippet:    "Test Entity",
		}
		if err := backend.InsertMention(ctx, mention); err != nil {
			t.Fatalf("failed to insert mention: %v", err)
		}
	}

	// Get mentions
	mentions, err := backend.GetMentions(ctx, "ent-1", 10)
	if err != nil {
		t.Fatalf("failed to get mentions: %v", err)
	}
	if len(mentions) != 5 {
		t.Errorf("expected 5 mentions, got %d", len(mentions))
	}

	// Verify entity mention count was updated
	updated, err := backend.GetEntityByID(ctx, "test-ns", "ent-1")
	if err != nil {
		t.Fatalf("failed to get entity: %v", err)
	}
	if updated.MentionCount != 5 {
		t.Errorf("expected mention count 5, got %d", updated.MentionCount)
	}
}

func TestUpsertAndGetRelationships(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create two entities
	entity1 := &types.Entity{
		ID:        "ent-1",
		Namespace: "test-ns",
		Name:      "John Smith",
		Type:      types.EntityTypePerson,
	}
	entity2 := &types.Entity{
		ID:        "ent-2",
		Namespace: "test-ns",
		Name:      "Acme Corp",
		Type:      types.EntityTypeOrganization,
	}

	if err := backend.UpsertEntity(ctx, entity1); err != nil {
		t.Fatalf("failed to upsert entity1: %v", err)
	}
	if err := backend.UpsertEntity(ctx, entity2); err != nil {
		t.Fatalf("failed to upsert entity2: %v", err)
	}

	// Create relationship
	rel := &types.EntityRelationship{
		Namespace:      "test-ns",
		SourceEntityID: "ent-1",
		TargetEntityID: "ent-2",
		RelationType:   "works_at",
		Description:    "John works at Acme Corp as CEO",
		Confidence:     0.95,
	}

	if err := backend.UpsertRelationship(ctx, rel); err != nil {
		t.Fatalf("failed to upsert relationship: %v", err)
	}

	// Get outgoing relationships for entity1
	rels, err := backend.GetRelationships(ctx, "test-ns", "ent-1", storage.RelationshipOpts{
		Direction: types.RelationshipDirectionOutgoing,
	})
	if err != nil {
		t.Fatalf("failed to get relationships: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].RelationType != "works_at" {
		t.Errorf("expected relation type 'works_at', got '%s'", rels[0].RelationType)
	}

	// Get incoming relationships for entity2
	rels, err = backend.GetRelationships(ctx, "test-ns", "ent-2", storage.RelationshipOpts{
		Direction: types.RelationshipDirectionIncoming,
	})
	if err != nil {
		t.Fatalf("failed to get incoming relationships: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 incoming relationship, got %d", len(rels))
	}
}

func TestUpsertRelationshipUpdate(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create entities
	entity1 := &types.Entity{ID: "ent-1", Namespace: "test-ns", Name: "A", Type: types.EntityTypePerson}
	entity2 := &types.Entity{ID: "ent-2", Namespace: "test-ns", Name: "B", Type: types.EntityTypePerson}
	backend.UpsertEntity(ctx, entity1)
	backend.UpsertEntity(ctx, entity2)

	// Create initial relationship
	rel := &types.EntityRelationship{
		Namespace:      "test-ns",
		SourceEntityID: "ent-1",
		TargetEntityID: "ent-2",
		RelationType:   "knows",
		MentionCount:   1,
	}
	if err := backend.UpsertRelationship(ctx, rel); err != nil {
		t.Fatalf("failed to create relationship: %v", err)
	}

	// Upsert again (should increment mention count)
	if err := backend.UpsertRelationship(ctx, rel); err != nil {
		t.Fatalf("failed to upsert relationship: %v", err)
	}

	// Get relationship
	rels, err := backend.GetRelationships(ctx, "test-ns", "ent-1", storage.RelationshipOpts{
		Direction: types.RelationshipDirectionOutgoing,
	})
	if err != nil {
		t.Fatalf("failed to get relationships: %v", err)
	}
	if rels[0].MentionCount != 2 {
		t.Errorf("expected mention count 2, got %d", rels[0].MentionCount)
	}
}

func TestRegisterAlias(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	entity := &types.Entity{
		ID:        "ent-1",
		Namespace: "test-ns",
		Name:      "Original Name",
		Type:      types.EntityTypePerson,
	}
	if err := backend.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("failed to upsert entity: %v", err)
	}

	// Register new alias
	if err := backend.RegisterAlias(ctx, "test-ns", "New Alias", "ent-1"); err != nil {
		t.Fatalf("failed to register alias: %v", err)
	}

	// Resolve by new alias
	resolved, err := backend.ResolveAlias(ctx, "test-ns", "new alias")
	if err != nil {
		t.Fatalf("failed to resolve alias: %v", err)
	}
	if resolved.ID != "ent-1" {
		t.Errorf("expected entity ID 'ent-1', got '%s'", resolved.ID)
	}
}

func TestRegisterAliasEntityNotFound(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	err := backend.RegisterAlias(ctx, "test-ns", "alias", "nonexistent")
	if err != storage.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMergeEntities(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Create source and target entities
	source := &types.Entity{
		ID:        "source-ent",
		Namespace: "test-ns",
		Name:      "Source Entity",
		Type:      types.EntityTypePerson,
	}
	target := &types.Entity{
		ID:        "target-ent",
		Namespace: "test-ns",
		Name:      "Target Entity",
		Type:      types.EntityTypePerson,
	}

	if err := backend.UpsertEntity(ctx, source); err != nil {
		t.Fatalf("failed to create source: %v", err)
	}
	if err := backend.UpsertEntity(ctx, target); err != nil {
		t.Fatalf("failed to create target: %v", err)
	}

	// Add mentions to source
	for i := 0; i < 3; i++ {
		mention := &types.EntityMention{
			EntityID:   "source-ent",
			Namespace:  "test-ns",
			SourceType: "conversation",
			SourceID:   "msg-" + string(rune('A'+i)),
		}
		if err := backend.InsertMention(ctx, mention); err != nil {
			t.Fatalf("failed to insert mention: %v", err)
		}
	}

	// Merge source into target
	if err := backend.MergeEntities(ctx, "test-ns", "source-ent", "target-ent"); err != nil {
		t.Fatalf("failed to merge entities: %v", err)
	}

	// Verify source is deleted
	_, err := backend.GetEntityByID(ctx, "test-ns", "source-ent")
	if err != storage.ErrNotFound {
		t.Errorf("expected source to be deleted, got %v", err)
	}

	// Verify mentions moved to target
	mentions, err := backend.GetMentions(ctx, "target-ent", 10)
	if err != nil {
		t.Fatalf("failed to get mentions: %v", err)
	}
	if len(mentions) != 3 {
		t.Errorf("expected 3 mentions on target, got %d", len(mentions))
	}

	// Verify target mention count updated
	merged, err := backend.GetEntityByID(ctx, "test-ns", "target-ent")
	if err != nil {
		t.Fatalf("failed to get target: %v", err)
	}
	if merged.MentionCount != 3 {
		t.Errorf("expected mention count 3, got %d", merged.MentionCount)
	}
}

func TestStoreEntityEmbedding(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	entity := &types.Entity{
		ID:        "ent-1",
		Namespace: "test-ns",
		Name:      "Test",
		Type:      types.EntityTypePerson,
	}
	if err := backend.UpsertEntity(ctx, entity); err != nil {
		t.Fatalf("failed to upsert entity: %v", err)
	}

	// Store embedding
	embedding := make([]float32, 1536)
	for i := range embedding {
		embedding[i] = float32(i) * 0.001
	}

	if err := backend.StoreEntityEmbedding(ctx, "ent-1", embedding); err != nil {
		t.Fatalf("failed to store embedding: %v", err)
	}

	// Verify stored
	var count int
	err := backend.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM entity_embeddings WHERE entity_id = ?", "ent-1",
	).Scan(&count)
	if err != nil {
		t.Fatalf("failed to count embeddings: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 embedding, got %d", count)
	}
}

func TestEnqueueAndDequeueExtraction(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Enqueue items
	for i := 0; i < 5; i++ {
		item := &types.ExtractionQueueItem{
			Namespace:  "test-ns",
			SourceType: "conversation",
			SourceID:   "msg-" + string(rune('A'+i)),
			Content:    "Content to extract from",
		}
		if err := backend.EnqueueExtraction(ctx, item); err != nil {
			t.Fatalf("failed to enqueue: %v", err)
		}
		if item.ID == 0 {
			t.Error("expected item ID to be set")
		}
	}

	// Dequeue batch
	items, err := backend.DequeueExtraction(ctx, 3)
	if err != nil {
		t.Fatalf("failed to dequeue: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}

	// Verify items are marked as processing
	stats, err := backend.GetExtractionQueueStats(ctx)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.ProcessingCount != 3 {
		t.Errorf("expected 3 processing, got %d", stats.ProcessingCount)
	}
	if stats.PendingCount != 2 {
		t.Errorf("expected 2 pending, got %d", stats.PendingCount)
	}
}

func TestCompleteExtraction(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Enqueue item
	item := &types.ExtractionQueueItem{
		Namespace:  "test-ns",
		SourceType: "conversation",
		SourceID:   "msg-1",
		Content:    "Content",
	}
	if err := backend.EnqueueExtraction(ctx, item); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	// Dequeue
	items, err := backend.DequeueExtraction(ctx, 1)
	if err != nil {
		t.Fatalf("failed to dequeue: %v", err)
	}

	// Complete as successful
	if err := backend.CompleteExtraction(ctx, items[0].ID, "completed"); err != nil {
		t.Fatalf("failed to complete: %v", err)
	}

	// Verify stats
	stats, err := backend.GetExtractionQueueStats(ctx)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.ProcessingCount != 0 {
		t.Errorf("expected 0 processing, got %d", stats.ProcessingCount)
	}
}

func TestCompleteExtractionDeadLetter(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// Enqueue item
	item := &types.ExtractionQueueItem{
		Namespace:  "test-ns",
		SourceType: "conversation",
		SourceID:   "msg-1",
		Content:    "Content",
	}
	if err := backend.EnqueueExtraction(ctx, item); err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	// Manually set attempts to 3 to simulate multiple failures
	_, err := backend.db.ExecContext(ctx,
		"UPDATE entity_extraction_queue SET attempts = 3, status = 'processing' WHERE id = ?", item.ID,
	)
	if err != nil {
		t.Fatalf("failed to set attempts: %v", err)
	}

	// Complete as failed - should move to dead_letter since attempts >= 3
	if err := backend.CompleteExtraction(ctx, item.ID, "failed"); err != nil {
		t.Fatalf("failed to complete: %v", err)
	}

	// Verify moved to dead letter
	stats, err := backend.GetExtractionQueueStats(ctx)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	if stats.DeadLetterCount != 1 {
		t.Errorf("expected 1 dead letter, got %d", stats.DeadLetterCount)
	}
}

func TestSearchEntitiesPlaceholder(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()

	// SearchEntities is a placeholder until vec0 integration
	results, err := backend.SearchEntities(ctx, "test-ns", []float32{0.1, 0.2}, storage.EntitySearchOpts{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}
