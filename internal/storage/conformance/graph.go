package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

func testGraph(ctx context.Context, t *testing.T, b storage.Backend, dims int) {
	const ns = "conformance-graph"
	now := time.Now().UTC()

	// 1. Collection GraphEnabled opt-in persistence (default false, opt-in true).
	colDefault := &types.Collection{
		ID:          "col-graph-default",
		Namespace:   ns,
		Name:        "default-col",
		ChunkConfig: types.DefaultChunkConfig(),
		CreatedAt:   now,
	}
	if err := b.CreateCollection(ctx, colDefault); err != nil {
		t.Fatalf("CreateCollection default: %v", err)
	}
	gotDefault, err := b.GetCollection(ctx, ns, colDefault.ID)
	if err != nil {
		t.Fatalf("GetCollection default: %v", err)
	}
	if gotDefault.GraphEnabled {
		t.Error("expected default collection GraphEnabled == false")
	}

	colOptIn := &types.Collection{
		ID:           "col-graph-optin",
		Namespace:    ns,
		Name:         "graph-col",
		ChunkConfig:  types.DefaultChunkConfig(),
		GraphEnabled: true,
		CreatedAt:    now.Add(time.Second),
	}
	if err := b.CreateCollection(ctx, colOptIn); err != nil {
		t.Fatalf("CreateCollection opt-in: %v", err)
	}
	gotOptIn, err := b.GetCollection(ctx, ns, colOptIn.ID)
	if err != nil {
		t.Fatalf("GetCollection opt-in: %v", err)
	}
	if !gotOptIn.GraphEnabled {
		t.Error("expected opt-in collection GraphEnabled == true")
	}

	// 2. Seed entities, relationships, documents, chunks, mentions.
	e1 := &types.Entity{
		ID:           "g-ent-1",
		Namespace:    ns,
		Name:         "Checkout Gateway",
		Type:         types.EntityTypeProduct,
		Aliases:      []string{"CGW"},
		Summary:      "Customer checkout API gateway",
		MentionCount: 2,
		FirstSeenAt:  now,
		LastSeenAt:   now,
	}
	e2 := &types.Entity{
		ID:           "g-ent-2",
		Namespace:    ns,
		Name:         "PaymentsDB",
		Type:         types.EntityTypeProduct,
		Aliases:      []string{"PDB"},
		Summary:      "Transactional payments database",
		MentionCount: 2,
		FirstSeenAt:  now,
		LastSeenAt:   now,
	}
	e3 := &types.Entity{
		ID:           "g-ent-3",
		Namespace:    ns,
		Name:         "Priya Nair",
		Type:         types.EntityTypePerson,
		Summary:      "PaymentsDB on-call lead",
		MentionCount: 1,
		FirstSeenAt:  now,
		LastSeenAt:   now,
	}
	for _, e := range []*types.Entity{e1, e2, e3} {
		if err := b.UpsertEntity(ctx, e); err != nil {
			t.Fatalf("UpsertEntity %s: %v", e.ID, err)
		}
	}

	r1 := &types.EntityRelationship{
		ID:             "g-rel-1",
		Namespace:      ns,
		SourceEntityID: e1.ID,
		TargetEntityID: e2.ID,
		RelationType:   "persists_in",
		Description:    "Checkout Gateway persists orders in PaymentsDB",
		Confidence:     0.95,
		MentionCount:   3,
		FirstSeenAt:    now,
		LastSeenAt:     now,
	}
	r2 := &types.EntityRelationship{
		ID:             "g-rel-2",
		Namespace:      ns,
		SourceEntityID: e3.ID,
		TargetEntityID: e2.ID,
		RelationType:   "manages",
		Description:    "Priya Nair manages PaymentsDB production clusters",
		Confidence:     0.92,
		MentionCount:   1,
		FirstSeenAt:    now,
		LastSeenAt:     now,
	}
	for _, r := range []*types.EntityRelationship{r1, r2} {
		if err := b.UpsertRelationship(ctx, r); err != nil {
			t.Fatalf("UpsertRelationship %s: %v", r.ID, err)
		}
	}

	doc := &types.Document{
		ID:           "g-doc-1",
		Namespace:    ns,
		CollectionID: colOptIn.ID,
		Title:        "Payments Runbook",
		Content:      "Checkout Gateway persists to PaymentsDB managed by Priya Nair.",
		ContentType:  "text",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := b.InsertDocument(ctx, doc); err != nil {
		t.Fatalf("InsertDocument: %v", err)
	}
	chunks := []*types.Chunk{
		{
			ID:           "g-chk-1",
			DocumentID:   doc.ID,
			Namespace:    ns,
			CollectionID: colOptIn.ID,
			Content:      "Checkout Gateway persists to PaymentsDB.",
			Embedding:    createTestEmbedding(0.1, dims),
			Index:        0,
			Metadata:     map[string]string{"env": "prod"},
		},
		{
			ID:           "g-chk-2",
			DocumentID:   doc.ID,
			Namespace:    ns,
			CollectionID: colOptIn.ID,
			Content:      "Priya Nair manages PaymentsDB.",
			Embedding:    createTestEmbedding(0.2, dims),
			Index:        1,
			Metadata:     map[string]string{"env": "staging"},
		},
	}
	if err := b.InsertChunks(ctx, chunks); err != nil {
		t.Fatalf("InsertChunks: %v", err)
	}

	mentions := []*types.EntityMention{
		{ID: "g-m-1", EntityID: e1.ID, Namespace: ns, SourceType: "knowledge", SourceID: "g-chk-1", CreatedAt: now},
		{ID: "g-m-2", EntityID: e2.ID, Namespace: ns, SourceType: "knowledge", SourceID: "g-chk-1", CreatedAt: now},
		{ID: "g-m-3", EntityID: e2.ID, Namespace: ns, SourceType: "knowledge", SourceID: "g-chk-2", CreatedAt: now.Add(time.Second)},
		{ID: "g-m-4", EntityID: e3.ID, Namespace: ns, SourceType: "knowledge", SourceID: "g-chk-2", CreatedAt: now},
	}
	for _, m := range mentions {
		if err := b.InsertMention(ctx, m); err != nil {
			t.Fatalf("InsertMention %s: %v", m.ID, err)
		}
	}

	// 3. Test GetRelationshipsBatch (respects maxPerEntity cap).
	rels, err := b.GetRelationshipsBatch(ctx, ns, []string{e2.ID}, 1)
	if err != nil {
		t.Fatalf("GetRelationshipsBatch: %v", err)
	}
	if len(rels) != 1 || rels[0].ID != r1.ID {
		t.Errorf("GetRelationshipsBatch(max=1) = %+v, want [%s]", rels, r1.ID)
	}

	allRels, err := b.GetRelationshipsBatch(ctx, ns, []string{e2.ID}, 10)
	if err != nil {
		t.Fatalf("GetRelationshipsBatch(max=10): %v", err)
	}
	if len(allRels) != 2 {
		t.Errorf("GetRelationshipsBatch(max=10) returned %d rels, want 2", len(allRels))
	}

	// 4. Test GetMentionsByEntityIDs & GetMentionsBySourceIDs.
	byEnt, err := b.GetMentionsByEntityIDs(ctx, ns, []string{e2.ID}, "knowledge", 10)
	if err != nil {
		t.Fatalf("GetMentionsByEntityIDs: %v", err)
	}
	if len(byEnt) != 2 {
		t.Errorf("GetMentionsByEntityIDs(e2) = %d, want 2", len(byEnt))
	}

	bySrc, err := b.GetMentionsBySourceIDs(ctx, ns, "knowledge", []string{"g-chk-1"})
	if err != nil {
		t.Fatalf("GetMentionsBySourceIDs: %v", err)
	}
	if len(bySrc) != 2 {
		t.Errorf("GetMentionsBySourceIDs(g-chk-1) = %d, want 2", len(bySrc))
	}

	// 5. Test GetChunksByIDs with collection & metadata filters.
	hydrated, err := b.GetChunksByIDs(ctx, ns, []string{"g-chk-1", "g-chk-2"}, &colOptIn.ID, map[string]string{"env": "prod"})
	if err != nil {
		t.Fatalf("GetChunksByIDs: %v", err)
	}
	if len(hydrated) != 1 || hydrated[0].Chunk.ID != "g-chk-1" {
		t.Errorf("GetChunksByIDs filtered = %+v, want [g-chk-1]", hydrated)
	}

	// 6. Test GetEntitiesByIDs.
	ents, err := b.GetEntitiesByIDs(ctx, ns, []string{e1.ID, e3.ID})
	if err != nil {
		t.Fatalf("GetEntitiesByIDs: %v", err)
	}
	if len(ents) != 2 {
		t.Errorf("GetEntitiesByIDs = %d, want 2", len(ents))
	}

	// 7. Test FindLexicalEntityMatches (multi-word name & alias).
	lex, err := b.FindLexicalEntityMatches(ctx, ns, "Who manages PDB behind Checkout Gateway?", 10)
	if err != nil {
		t.Fatalf("FindLexicalEntityMatches: %v", err)
	}
	if len(lex) < 2 {
		t.Errorf("FindLexicalEntityMatches returned %d entities, want >= 2 (CGW + PDB)", len(lex))
	}

	// 8. Test FindEntityCandidatesByPrefix.
	pref, err := b.FindEntityCandidatesByPrefix(ctx, ns, "pay", 10)
	if err != nil {
		t.Fatalf("FindEntityCandidatesByPrefix: %v", err)
	}
	if len(pref) != 1 || pref[0].ID != e2.ID {
		t.Errorf("FindEntityCandidatesByPrefix('pay') = %+v, want [%s]", pref, e2.ID)
	}

	// 9. Test SearchRelationshipsFTS (Variant B).
	ftsRels, err := b.SearchRelationshipsFTS(ctx, ns, "Who manages PaymentsDB production clusters?", 5)
	if err != nil {
		t.Fatalf("SearchRelationshipsFTS: %v", err)
	}
	if len(ftsRels) == 0 {
		t.Errorf("SearchRelationshipsFTS returned 0 hits, want >= 1")
	}
}
