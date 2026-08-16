package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

func testEntity(ctx context.Context, t *testing.T, b storage.Backend, dims int) {
	const ns = "conf-entity"

	t.Run("UpsertAndGetByID", func(t *testing.T) {
		entity := &types.Entity{
			Namespace:  ns,
			Name:       "John Smith",
			Type:       types.EntityTypePerson,
			Aliases:    []string{"John", "J. Smith"},
			Summary:    "CEO of Example Corp",
			Attributes: map[string]string{"role": "CEO", "company": "Example Corp"},
			Metadata:   map[string]string{"source": "manual"},
		}
		if err := b.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("UpsertEntity: %v", err)
		}
		if entity.ID == "" {
			t.Fatal("expected entity ID to be generated")
		}

		got, err := b.GetEntityByID(ctx, ns, entity.ID)
		if err != nil {
			t.Fatalf("GetEntityByID: %v", err)
		}
		if got.Name != "John Smith" {
			t.Errorf("expected name 'John Smith', got %q", got.Name)
		}
		if got.Type != types.EntityTypePerson {
			t.Errorf("expected type 'person', got %s", got.Type)
		}
		if len(got.Aliases) != 2 {
			t.Errorf("expected 2 aliases, got %d", len(got.Aliases))
		}
		if got.Attributes["role"] != "CEO" {
			t.Errorf("expected attribute role=CEO, got %v", got.Attributes["role"])
		}
	})

	t.Run("UpsertUpdate", func(t *testing.T) {
		entity := &types.Entity{
			Namespace: ns,
			Name:      "Update Me",
			Type:      types.EntityTypePerson,
			Summary:   "Initial",
		}
		if err := b.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("UpsertEntity: %v", err)
		}
		entity.Summary = "Updated"
		entity.Aliases = []string{"Upd", "UM"}
		if err := b.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("UpsertEntity update: %v", err)
		}
		got, err := b.GetEntityByID(ctx, ns, entity.ID)
		if err != nil {
			t.Fatalf("GetEntityByID: %v", err)
		}
		if got.Summary != "Updated" {
			t.Errorf("expected summary 'Updated', got %q", got.Summary)
		}
		if len(got.Aliases) != 2 {
			t.Errorf("expected 2 aliases, got %d", len(got.Aliases))
		}
	})

	t.Run("GetByName", func(t *testing.T) {
		entity := &types.Entity{
			Namespace: ns,
			Name:      "Acme Corporation",
			Type:      types.EntityTypeOrganization,
		}
		if err := b.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("UpsertEntity: %v", err)
		}
		// Case-insensitive lookup.
		got, err := b.GetEntityByName(ctx, ns, "acme corporation")
		if err != nil {
			t.Fatalf("GetEntityByName: %v", err)
		}
		if got.Name != "Acme Corporation" {
			t.Errorf("expected 'Acme Corporation', got %q", got.Name)
		}
	})

	t.Run("GetEntityByIDNotFound", func(t *testing.T) {
		_, err := b.GetEntityByID(ctx, ns, "nonexistent")
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("RegisterAliasAndResolve", func(t *testing.T) {
		entity := &types.Entity{
			ID:        "ent-alias",
			Namespace: ns,
			Name:      "Original Name",
			Type:      types.EntityTypePerson,
		}
		if err := b.UpsertEntity(ctx, entity); err != nil {
			t.Fatalf("UpsertEntity: %v", err)
		}
		if err := b.RegisterAlias(ctx, ns, "New Alias", "ent-alias"); err != nil {
			t.Fatalf("RegisterAlias: %v", err)
		}
		// Resolve case-insensitively.
		got, err := b.ResolveAlias(ctx, ns, "new alias")
		if err != nil {
			t.Fatalf("ResolveAlias: %v", err)
		}
		if got.ID != "ent-alias" {
			t.Errorf("expected entity ID 'ent-alias', got %s", got.ID)
		}
	})

	t.Run("RegisterAliasEntityNotFound", func(t *testing.T) {
		err := b.RegisterAlias(ctx, ns, "orphan-alias", "nonexistent")
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("ListEntities", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			if err := b.UpsertEntity(ctx, &types.Entity{
				ID:        "ent-list-" + string(rune('A'+i)),
				Namespace: ns,
				Name:      "List Entity " + string(rune('A'+i)),
				Type:      types.EntityTypeConcept,
			}); err != nil {
				t.Fatalf("UpsertEntity: %v", err)
			}
		}
		entities, _, err := b.ListEntities(ctx, ns, storage.EntityListOpts{
			SortBy: types.EntitySortByName,
			Limit:  100,
		})
		if err != nil {
			t.Fatalf("ListEntities: %v", err)
		}
		if len(entities) < 3 {
			t.Errorf("expected at least 3 entities, got %d", len(entities))
		}
	})

	t.Run("DeleteEntity", func(t *testing.T) {
		if err := b.UpsertEntity(ctx, &types.Entity{
			ID:        "ent-delete",
			Namespace: ns,
			Name:      "Delete Me",
			Type:      types.EntityTypePerson,
		}); err != nil {
			t.Fatalf("UpsertEntity: %v", err)
		}
		if err := b.DeleteEntity(ctx, ns, "ent-delete"); err != nil {
			t.Fatalf("DeleteEntity: %v", err)
		}
		_, err := b.GetEntityByID(ctx, ns, "ent-delete")
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound after delete, got %v", err)
		}
	})

	t.Run("Mentions", func(t *testing.T) {
		if err := b.UpsertEntity(ctx, &types.Entity{
			ID:        "ent-mentions",
			Namespace: ns,
			Name:      "Mentioned Entity",
			Type:      types.EntityTypePerson,
		}); err != nil {
			t.Fatalf("UpsertEntity: %v", err)
		}
		for i := 0; i < 3; i++ {
			if err := b.InsertMention(ctx, &types.EntityMention{
				EntityID:   "ent-mentions",
				Namespace:  ns,
				SourceType: "conversation",
				SourceID:   "msg-" + string(rune('A'+i)),
				Context:    "surrounding text",
				Snippet:    "Mentioned Entity",
			}); err != nil {
				t.Fatalf("InsertMention %d: %v", i, err)
			}
		}
		mentions, err := b.GetMentions(ctx, "ent-mentions", 10)
		if err != nil {
			t.Fatalf("GetMentions: %v", err)
		}
		if len(mentions) != 3 {
			t.Errorf("expected 3 mentions, got %d", len(mentions))
		}
	})

	t.Run("Relationships", func(t *testing.T) {
		if err := b.UpsertEntity(ctx, &types.Entity{ID: "rel-src", Namespace: ns, Name: "Source", Type: types.EntityTypePerson}); err != nil {
			t.Fatalf("UpsertEntity: %v", err)
		}
		if err := b.UpsertEntity(ctx, &types.Entity{ID: "rel-tgt", Namespace: ns, Name: "Target", Type: types.EntityTypeOrganization}); err != nil {
			t.Fatalf("UpsertEntity: %v", err)
		}
		rel := &types.EntityRelationship{
			Namespace:      ns,
			SourceEntityID: "rel-src",
			TargetEntityID: "rel-tgt",
			RelationType:   "works_at",
			MentionCount:   1,
		}
		if err := b.UpsertRelationship(ctx, rel); err != nil {
			t.Fatalf("UpsertRelationship: %v", err)
		}

		// Upsert again should increment mention count.
		if err := b.UpsertRelationship(ctx, rel); err != nil {
			t.Fatalf("UpsertRelationship update: %v", err)
		}

		outgoing, err := b.GetRelationships(ctx, ns, "rel-src", storage.RelationshipOpts{
			Direction: types.RelationshipDirectionOutgoing,
		})
		if err != nil {
			t.Fatalf("GetRelationships outgoing: %v", err)
		}
		if len(outgoing) != 1 {
			t.Fatalf("expected 1 outgoing relationship, got %d", len(outgoing))
		}
		if outgoing[0].RelationType != "works_at" {
			t.Errorf("expected 'works_at', got %q", outgoing[0].RelationType)
		}
		if outgoing[0].MentionCount != 2 {
			t.Errorf("expected mention count 2, got %d", outgoing[0].MentionCount)
		}

		incoming, err := b.GetRelationships(ctx, ns, "rel-tgt", storage.RelationshipOpts{
			Direction: types.RelationshipDirectionIncoming,
		})
		if err != nil {
			t.Fatalf("GetRelationships incoming: %v", err)
		}
		if len(incoming) != 1 {
			t.Errorf("expected 1 incoming relationship, got %d", len(incoming))
		}
	})

	t.Run("MergeEntities", func(t *testing.T) {
		if err := b.UpsertEntity(ctx, &types.Entity{ID: "merge-src", Namespace: ns, Name: "Source Entity", Type: types.EntityTypePerson}); err != nil {
			t.Fatalf("UpsertEntity: %v", err)
		}
		if err := b.UpsertEntity(ctx, &types.Entity{ID: "merge-tgt", Namespace: ns, Name: "Target Entity", Type: types.EntityTypePerson}); err != nil {
			t.Fatalf("UpsertEntity: %v", err)
		}
		for i := 0; i < 3; i++ {
			if err := b.InsertMention(ctx, &types.EntityMention{
				EntityID:   "merge-src",
				Namespace:  ns,
				SourceType: "conversation",
				SourceID:   "merge-msg-" + string(rune('A'+i)),
			}); err != nil {
				t.Fatalf("InsertMention: %v", err)
			}
		}

		if err := b.MergeEntities(ctx, ns, "merge-src", "merge-tgt"); err != nil {
			t.Fatalf("MergeEntities: %v", err)
		}

		// Source should be deleted.
		_, err := b.GetEntityByID(ctx, ns, "merge-src")
		if err != storage.ErrNotFound {
			t.Errorf("expected ErrNotFound for merged source, got %v", err)
		}

		// Target should have the mentions.
		tgt, err := b.GetEntityByID(ctx, ns, "merge-tgt")
		if err != nil {
			t.Fatalf("GetEntityByID: %v", err)
		}
		if tgt.MentionCount < 3 {
			t.Errorf("expected mention count >= 3 after merge, got %d", tgt.MentionCount)
		}
	})

	t.Run("StoreEmbeddingAndSearch", func(t *testing.T) {
		entities := []*types.Entity{
			{ID: "ent-search-1", Namespace: ns, Name: "OpenAI", Type: types.EntityTypeOrganization, Summary: "AI research lab"},
			{ID: "ent-search-2", Namespace: ns, Name: "Google DeepMind", Type: types.EntityTypeOrganization, Summary: "AI research lab"},
			{ID: "ent-search-3", Namespace: ns, Name: "Paris", Type: types.EntityTypeLocation, Summary: "City in France"},
		}
		embeddings := [][]float32{
			createTestEmbedding(0.1, dims),
			createTestEmbedding(0.15, dims),
			createTestEmbedding(0.9, dims),
		}
		for i, e := range entities {
			if err := b.UpsertEntity(ctx, e); err != nil {
				t.Fatalf("UpsertEntity: %v", err)
			}
			if err := b.StoreEntityEmbedding(ctx, e.ID, embeddings[i]); err != nil {
				t.Fatalf("StoreEntityEmbedding: %v", err)
			}
		}

		// Search with an embedding similar to the org entities.
		results, err := b.SearchEntities(ctx, ns, createTestEmbedding(0.12, dims), storage.EntitySearchOpts{TopK: 10})
		if err != nil {
			t.Fatalf("SearchEntities: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}
		// The org entities should rank higher than the location.
		top := results[0].Entity.ID
		if top != "ent-search-1" && top != "ent-search-2" {
			t.Errorf("expected first result to be an org entity, got %s", top)
		}
	})

	t.Run("SearchEntitiesTypeFilter", func(t *testing.T) {
		orgType := types.EntityTypeOrganization
		results, err := b.SearchEntities(ctx, ns, createTestEmbedding(0.12, dims), storage.EntitySearchOpts{
			TopK:       10,
			EntityType: &orgType,
		})
		if err != nil {
			t.Fatalf("SearchEntities: %v", err)
		}
		for _, r := range results {
			if r.Entity.Type != types.EntityTypeOrganization {
				t.Errorf("expected only organizations, got %s", r.Entity.Type)
			}
		}
	})

	t.Run("ExtractionQueue", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			if err := b.EnqueueExtraction(ctx, &types.ExtractionQueueItem{
				Namespace:  ns,
				SourceType: "conversation",
				SourceID:   "extract-msg-" + string(rune('A'+i)),
				Content:    "Content to extract from",
			}); err != nil {
				t.Fatalf("EnqueueExtraction: %v", err)
			}
		}

		items, err := b.DequeueExtraction(ctx, 3)
		if err != nil {
			t.Fatalf("DequeueExtraction: %v", err)
		}
		if len(items) != 3 {
			t.Fatalf("expected 3 items, got %d", len(items))
		}

		stats, err := b.GetExtractionQueueStats(ctx)
		if err != nil {
			t.Fatalf("GetExtractionQueueStats: %v", err)
		}
		if stats.ProcessingCount != 3 {
			t.Errorf("expected 3 processing, got %d", stats.ProcessingCount)
		}
		if stats.PendingCount != 2 {
			t.Errorf("expected 2 pending, got %d", stats.PendingCount)
		}

		// Complete one item.
		if err := b.CompleteExtraction(ctx, items[0].ID, "completed"); err != nil {
			t.Fatalf("CompleteExtraction: %v", err)
		}

		// Requeue one in-flight item with a future retry: it returns to
		// pending but is not eligible until next_retry_at elapses.
		future := time.Now().Add(1 * time.Hour)
		if err := b.RequeueExtraction(ctx, items[1].ID, future, true); err != nil {
			t.Fatalf("RequeueExtraction: %v", err)
		}

		requeued, err := b.DequeueExtraction(ctx, 10)
		if err != nil {
			t.Fatalf("DequeueExtraction after requeue: %v", err)
		}
		for _, it := range requeued {
			if it.ID == items[1].ID {
				t.Error("expected requeued item to be ineligible during backoff")
			}
		}

		// Requeue without counting the failure (shutdown path): immediately
		// eligible again.
		if err := b.RequeueExtraction(ctx, items[1].ID, time.Time{}, false); err != nil {
			t.Fatalf("RequeueExtraction (shutdown): %v", err)
		}

		requeued, err = b.DequeueExtraction(ctx, 10)
		if err != nil {
			t.Fatalf("DequeueExtraction after shutdown requeue: %v", err)
		}
		found := false
		for _, it := range requeued {
			if it.ID == items[1].ID {
				found = true
			}
		}
		if !found {
			t.Error("expected shutdown-requeued item to be immediately eligible")
		}
	})
}
