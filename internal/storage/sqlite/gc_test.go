package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/cortex/v2/internal/storage"
	"github.com/petal-labs/cortex/v2/pkg/types"
)

func TestPruneStaleEntitiesPreservesFreshEntities(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()
	ctx := context.Background()
	ns := "prune-ns"
	now := time.Now().UTC()

	fresh := &types.Entity{
		ID:           "fresh",
		Namespace:    ns,
		Name:         "Fresh",
		Type:         types.EntityTypePerson,
		MentionCount: 0,
		LastSeenAt:   now,
	}
	stale := &types.Entity{
		ID:           "stale",
		Namespace:    ns,
		Name:         "Stale",
		Type:         types.EntityTypePerson,
		MentionCount: 0,
		LastSeenAt:   now.Add(-48 * time.Hour),
	}
	active := &types.Entity{
		ID:           "active",
		Namespace:    ns,
		Name:         "Active",
		Type:         types.EntityTypePerson,
		MentionCount: 10,
		LastSeenAt:   now.Add(-48 * time.Hour),
	}
	for _, e := range []*types.Entity{fresh, stale, active} {
		if err := backend.UpsertEntity(ctx, e); err != nil {
			t.Fatalf("UpsertEntity(%s): %v", e.ID, err)
		}
	}

	deleted, err := backend.PruneStaleEntities(ctx, 24*time.Hour, 5)
	if err != nil {
		t.Fatalf("PruneStaleEntities: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if _, err := backend.GetEntityByID(ctx, ns, "fresh"); err != nil {
		t.Fatalf("fresh entity was deleted: %v", err)
	}
	if _, err := backend.GetEntityByID(ctx, ns, "stale"); err != storage.ErrNotFound {
		t.Fatalf("stale entity still present: %v", err)
	}
	if _, err := backend.GetEntityByID(ctx, ns, "active"); err != nil {
		t.Fatalf("high-mention entity was deleted: %v", err)
	}
}
