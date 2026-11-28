package graphrag

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/petal-labs/cortex/v2/internal/storage"
	"github.com/petal-labs/cortex/v2/pkg/types"
)

// Retriever executes zero-LLM-at-query-time HippoRAG-style graph retrieval
// using Variant B (FTS relationship seeding + pure-Go bipartite PPR).
type Retriever struct {
	store GraphStore
}

// NewRetriever creates a Retriever backed by store (any storage.Backend).
func NewRetriever(store GraphStore) *Retriever {
	return &Retriever{store: store}
}

// Search runs parallel multi-signal seeding, bounded 1-2 hop local bipartite
// graph expansion, and pure-Go Personalized PageRank. It performs zero LLM calls.
func (r *Retriever) Search(ctx context.Context, namespace, query string, queryEmbedding []float32, cfg SearchConfig) (*SearchResult, error) {
	totalStart := time.Now()
	cfg = normalizeConfig(cfg)

	// ------------------------------------------------------------------
	// Stage 1: Parallel Seeding (chunks + entities + lexical + FTS rels)
	// ------------------------------------------------------------------
	seedStart := time.Now()

	var (
		seedChunks   []*types.ChunkResult
		seedEntities []*types.EntityResult
		lexicalEnts  []*types.Entity
		seedRels     []*types.RelationshipResult
		chunkErr     error
		entityErr    error
		lexicalErr   error
		relErr       error
		wg           sync.WaitGroup
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		seedChunks, chunkErr = r.store.HybridSearchChunks(ctx, namespace, query, queryEmbedding, storage.HybridChunkSearchOpts{
			TopK:         cfg.SeedChunksK,
			CollectionID: cfg.CollectionID,
			Filters:      cfg.Filters,
			Alpha:        0.5,
			RRFConstant:  cfg.RRFConstant,
		})
	}()

	go func() {
		defer wg.Done()
		seedEntities, entityErr = r.store.HybridSearchEntities(ctx, namespace, query, queryEmbedding, storage.HybridEntitySearchOpts{
			TopK:        cfg.SeedEntitiesK,
			Alpha:       0.5,
			RRFConstant: cfg.RRFConstant,
		})
	}()

	go func() {
		defer wg.Done()
		lexicalEnts, lexicalErr = r.store.FindLexicalEntityMatches(ctx, namespace, query, cfg.SeedEntitiesK)
	}()

	if cfg.RelationshipMode == RelationshipModeFTS {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seedRels, relErr = r.store.SearchRelationshipsFTS(ctx, namespace, query, cfg.SeedRelationshipsK)
		}()
	}

	wg.Wait()
	seedDur := time.Since(seedStart)

	if chunkErr != nil {
		return nil, fmt.Errorf("graphrag seed chunks failed: %w", chunkErr)
	}
	if entityErr != nil {
		return nil, fmt.Errorf("graphrag seed entities failed: %w", entityErr)
	}
	_ = lexicalErr
	if relErr != nil {
		return nil, fmt.Errorf("graphrag seed relationships failed: %w", relErr)
	}

	// ------------------------------------------------------------------
	// Stage 2: Bounded Local Bipartite Graph Expansion
	// ------------------------------------------------------------------
	expandStart := time.Now()

	entitySeedWeights := make(map[string]float64, len(seedEntities)+len(lexicalEnts)+len(seedRels)*2)
	entityInfo := make(map[string]*types.Entity, len(seedEntities)+len(lexicalEnts))
	lexicalSet := make(map[string]bool, len(lexicalEnts))

	// Use sharp rank decay (1 / (2 + rank)) for PPR teleport probabilities so top
	// query anchors dominate over noisy tail candidates.
	const teleportK = 2.0

	for _, e := range lexicalEnts {
		if e == nil {
			continue
		}
		lexicalSet[e.ID] = true
		entitySeedWeights[e.ID] += cfg.LexicalAnchorWeight / (teleportK + 1.0)
		entityInfo[e.ID] = e
	}

	topEntitySet := make(map[string]bool, min(len(seedEntities), 5))
	for i, er := range seedEntities {
		if er == nil || er.Entity == nil {
			continue
		}
		rank := i + 1
		if er.Rank > 0 {
			rank = er.Rank
		}
		if i < 5 {
			topEntitySet[er.Entity.ID] = true
		}
		entitySeedWeights[er.Entity.ID] += 1.0 / (teleportK + float64(rank))
		entityInfo[er.Entity.ID] = er.Entity
	}

	relMap := make(map[string]*types.EntityRelationship, len(seedRels)*4)
	topRelScore := 0.0
	if len(seedRels) > 0 && seedRels[0] != nil {
		topRelScore = seedRels[0].Score
	}
	for i, sr := range seedRels {
		if sr == nil || sr.Relationship == nil {
			continue
		}
		rel := sr.Relationship
		relMap[rel.ID] = rel
		if topRelScore > 0 && sr.Score < topRelScore*0.65 && i >= 3 {
			continue
		}
		rank := i + 1
		if sr.Rank > 0 {
			rank = sr.Rank
		}
		anchored := lexicalSet[rel.SourceEntityID] || lexicalSet[rel.TargetEntityID] ||
			topEntitySet[rel.SourceEntityID] || topEntitySet[rel.TargetEntityID]
		coherence := 0.25
		if anchored {
			coherence = 1.0
		}
		w := cfg.FactBoostWeight * coherence * (1.0 / (teleportK + float64(rank)))
		entitySeedWeights[rel.SourceEntityID] += w
		entitySeedWeights[rel.TargetEntityID] += w
	}

	seedChunkMap := make(map[string]*types.ChunkResult, len(seedChunks))
	seedChunkIDs := make([]string, 0, len(seedChunks))
	for _, cr := range seedChunks {
		if cr == nil || cr.Chunk == nil {
			continue
		}
		seedChunkMap[cr.Chunk.ID] = cr
		seedChunkIDs = append(seedChunkIDs, cr.Chunk.ID)
	}

	chunkMentions, err := r.store.GetMentionsBySourceIDs(ctx, namespace, "knowledge", seedChunkIDs)
	if err != nil {
		return nil, fmt.Errorf("graphrag chunk mentions lookup failed: %w", err)
	}

	visitedEntities := make(map[string]bool, len(entitySeedWeights)+len(chunkMentions))
	frontier := make([]string, 0, len(entitySeedWeights)+len(chunkMentions))
	addFrontier := func(id string) {
		if id == "" || visitedEntities[id] {
			return
		}
		if len(visitedEntities) >= cfg.MaxNodes/2 {
			return
		}
		visitedEntities[id] = true
		frontier = append(frontier, id)
	}

	sortedSeeds := make([]string, 0, len(entitySeedWeights))
	for id := range entitySeedWeights {
		sortedSeeds = append(sortedSeeds, id)
	}
	sort.Slice(sortedSeeds, func(i, j int) bool {
		if entitySeedWeights[sortedSeeds[i]] == entitySeedWeights[sortedSeeds[j]] {
			return sortedSeeds[i] < sortedSeeds[j]
		}
		return entitySeedWeights[sortedSeeds[i]] > entitySeedWeights[sortedSeeds[j]]
	})
	for _, id := range sortedSeeds {
		addFrontier(id)
	}
	for _, m := range chunkMentions {
		addFrontier(m.EntityID)
	}

	// k-hop entity relationship expansion with bounded per-hop frontier width.
	const maxFrontierPerHop = 40
	maxEntities := cfg.MaxNodes / 2
	if len(frontier) > maxFrontierPerHop {
		frontier = frontier[:maxFrontierPerHop]
	}
	for hop := 0; hop < cfg.MaxHops && len(frontier) > 0 && len(visitedEntities) < maxEntities; hop++ {
		rels, err := r.store.GetRelationshipsBatch(ctx, namespace, frontier, cfg.MaxDegree)
		if err != nil {
			return nil, fmt.Errorf("graphrag hop %d relationships failed: %w", hop+1, err)
		}

		nextScores := make(map[string]float64)
		for _, rel := range rels {
			relMap[rel.ID] = rel
			w := rel.Confidence * (1.0 + math.Log(float64(max(rel.MentionCount, 1))))
			for _, endpoint := range []string{rel.SourceEntityID, rel.TargetEntityID} {
				if !visitedEntities[endpoint] {
					nextScores[endpoint] += w
				}
			}
		}

		rankedNext := make([]string, 0, len(nextScores))
		for id := range nextScores {
			rankedNext = append(rankedNext, id)
		}
		sort.Slice(rankedNext, func(i, j int) bool {
			if nextScores[rankedNext[i]] == nextScores[rankedNext[j]] {
				return rankedNext[i] < rankedNext[j]
			}
			return nextScores[rankedNext[i]] > nextScores[rankedNext[j]]
		})

		nextFrontier := make([]string, 0, min(len(rankedNext), maxFrontierPerHop))
		for _, endpoint := range rankedNext {
			if len(visitedEntities) >= maxEntities || len(nextFrontier) >= maxFrontierPerHop {
				break
			}
			visitedEntities[endpoint] = true
			nextFrontier = append(nextFrontier, endpoint)
		}
		frontier = nextFrontier
	}

	// Second pass: reinforce relationship seeds that bridge into our expanded subgraph.
	for i, sr := range seedRels {
		if sr == nil || sr.Relationship == nil {
			continue
		}
		rel := sr.Relationship
		if !visitedEntities[rel.SourceEntityID] && !visitedEntities[rel.TargetEntityID] {
			continue
		}
		if lexicalSet[rel.SourceEntityID] || lexicalSet[rel.TargetEntityID] ||
			topEntitySet[rel.SourceEntityID] || topEntitySet[rel.TargetEntityID] {
			continue
		}
		rank := i + 1
		if sr.Rank > 0 {
			rank = sr.Rank
		}
		w := cfg.FactBoostWeight * 0.75 * (1.0 / (teleportK + float64(rank)))
		entitySeedWeights[rel.SourceEntityID] += w
		entitySeedWeights[rel.TargetEntityID] += w
	}

	allEntityIDs := make([]string, 0, len(visitedEntities))
	for id := range visitedEntities {
		allEntityIDs = append(allEntityIDs, id)
	}
	entityMentions, err := r.store.GetMentionsByEntityIDs(ctx, namespace, allEntityIDs, "knowledge", cfg.MaxDegree)
	if err != nil {
		return nil, fmt.Errorf("graphrag entity mentions lookup failed: %w", err)
	}

	missingEntityIDs := make([]string, 0)
	for _, id := range allEntityIDs {
		if _, ok := entityInfo[id]; !ok {
			missingEntityIDs = append(missingEntityIDs, id)
		}
	}
	if len(missingEntityIDs) > 0 {
		fetched, err := r.store.GetEntitiesByIDs(ctx, namespace, missingEntityIDs)
		if err != nil {
			return nil, fmt.Errorf("graphrag entity metadata fetch failed: %w", err)
		}
		for _, e := range fetched {
			entityInfo[e.ID] = e
		}
	}

	expandDur := time.Since(expandStart)

	// ------------------------------------------------------------------
	// Stage 3: Build Bipartite Graph & Run Pure-Go Personalized PageRank
	// ------------------------------------------------------------------
	pprStart := time.Now()

	g := NewGraph(len(allEntityIDs) + len(seedChunkIDs) + len(entityMentions))

	for _, eid := range allEntityIDs {
		g.AddNode(NodeEntity, eid)
	}
	for _, cid := range seedChunkIDs {
		g.AddNode(NodeChunk, cid)
	}

	for _, rel := range relMap {
		u, okU := g.Lookup(NodeEntity, rel.SourceEntityID)
		v, okV := g.Lookup(NodeEntity, rel.TargetEntityID)
		if !okU || !okV {
			continue
		}
		conf := rel.Confidence
		if conf <= 0 {
			conf = 0.5
		}
		mentions := rel.MentionCount
		if mentions < 1 {
			mentions = 1
		}
		w := conf * (1.0 + math.Log(float64(mentions)))
		if rel.RelationType == "synonym_of" {
			w *= cfg.SynonymWeight
		}
		g.AddUndirectedEdge(u, v, w)
	}

	entityChunkWeight := func(entityID string) float64 {
		mc := int64(1)
		if e, ok := entityInfo[entityID]; ok && e.MentionCount > 1 {
			mc = e.MentionCount
		}
		return cfg.EntityChunkEdgeScale / math.Sqrt(float64(mc))
	}

	addMentionEdge := func(m *types.EntityMention) {
		if m == nil || m.SourceType != "knowledge" || m.SourceID == "" {
			return
		}
		u, ok := g.Lookup(NodeEntity, m.EntityID)
		if !ok {
			return
		}
		if g.NodeCount() >= cfg.MaxNodes {
			if _, exists := g.Lookup(NodeChunk, m.SourceID); !exists {
				return
			}
		}
		v := g.AddNode(NodeChunk, m.SourceID)
		g.AddUndirectedEdge(u, v, entityChunkWeight(m.EntityID))
	}

	for _, m := range chunkMentions {
		addMentionEdge(m)
	}
	for _, m := range entityMentions {
		addMentionEdge(m)
	}

	personalization := make(map[int]float64, len(entitySeedWeights)+len(seedChunks))

	entityMassSum := 0.0
	for _, w := range entitySeedWeights {
		entityMassSum += w
	}

	chunkSeedWeights := make(map[string]float64, len(seedChunks))
	chunkMassSum := 0.0
	for i, cr := range seedChunks {
		if cr == nil || cr.Chunk == nil {
			continue
		}
		rank := i + 1
		if cr.Rank > 0 {
			rank = cr.Rank
		}
		w := 1.0 / (teleportK + float64(rank))
		chunkSeedWeights[cr.Chunk.ID] = w
		chunkMassSum += w
	}

	entTeleport := cfg.EntityTeleportWeight
	chkTeleport := 1.0 - entTeleport
	if entityMassSum == 0 {
		entTeleport = 0
		chkTeleport = 1.0
	} else if chunkMassSum == 0 {
		entTeleport = 1.0
		chkTeleport = 0
	}

	if entityMassSum > 0 {
		for eid, w := range entitySeedWeights {
			if idx, ok := g.Lookup(NodeEntity, eid); ok {
				personalization[idx] += entTeleport * (w / entityMassSum)
			}
		}
	}
	if chunkMassSum > 0 {
		for cid, w := range chunkSeedWeights {
			if idx, ok := g.Lookup(NodeChunk, cid); ok {
				personalization[idx] += chkTeleport * (w / chunkMassSum)
			}
		}
	}

	scores, pprIters := RunPPR(g, personalization, cfg.PPR)
	pprDur := time.Since(pprStart)

	// ------------------------------------------------------------------
	// Stage 4: Rank Chunks by Bipartite PPR Score & Hydrate Results
	// ------------------------------------------------------------------
	hydrateStart := time.Now()

	pprChunks := make([]scoredNode, 0)
	pprEntities := make([]scoredNode, 0)
	entityScoreMap := make(map[string]float64)

	for idx, node := range g.Nodes() {
		sc := scores[idx]
		if sc <= 0 {
			continue
		}
		if node.Kind == NodeChunk {
			pprChunks = append(pprChunks, scoredNode{id: node.ID, score: sc})
		} else {
			pprEntities = append(pprEntities, scoredNode{id: node.ID, score: sc})
			entityScoreMap[node.ID] = sc
		}
	}

	sort.Slice(pprChunks, func(i, j int) bool {
		if pprChunks[i].score == pprChunks[j].score {
			return pprChunks[i].id < pprChunks[j].id
		}
		return pprChunks[i].score > pprChunks[j].score
	})

	rankedChunks := pprChunks
	if len(rankedChunks) == 0 {
		for _, cr := range seedChunks {
			if cr == nil || cr.Chunk == nil {
				continue
			}
			rankedChunks = append(rankedChunks, scoredNode{id: cr.Chunk.ID, score: cr.Score})
		}
	}

	candidateLimit := cfg.TopK
	if cfg.CollectionID != nil || len(cfg.Filters) > 0 {
		candidateLimit = cfg.TopK * 3
	}
	if candidateLimit > len(rankedChunks) {
		candidateLimit = len(rankedChunks)
	}

	missingChunkIDs := make([]string, 0)
	for _, rc := range rankedChunks[:candidateLimit] {
		if _, ok := seedChunkMap[rc.id]; !ok {
			missingChunkIDs = append(missingChunkIDs, rc.id)
		}
	}

	hydratedMap := make(map[string]*types.ChunkResult, len(missingChunkIDs))
	if len(missingChunkIDs) > 0 {
		fetched, err := r.store.GetChunksByIDs(ctx, namespace, missingChunkIDs, cfg.CollectionID, cfg.Filters)
		if err != nil {
			return nil, fmt.Errorf("graphrag chunk hydration failed: %w", err)
		}
		for _, cr := range fetched {
			if cr != nil && cr.Chunk != nil {
				hydratedMap[cr.Chunk.ID] = cr
			}
		}
	}

	finalResults := make([]*types.ChunkResult, 0, cfg.TopK)
	for _, rc := range rankedChunks[:candidateLimit] {
		if len(finalResults) >= cfg.TopK {
			break
		}
		cr, ok := seedChunkMap[rc.id]
		if !ok {
			cr, ok = hydratedMap[rc.id]
		}
		if !ok {
			continue
		}
		copyRes := *cr
		copyRes.Score = rc.score
		copyRes.Rank = len(finalResults) + 1
		finalResults = append(finalResults, &copyRes)
	}

	var graphCtx *types.GraphContext
	if cfg.IncludeGraphContext {
		graphCtx = buildGraphContext(pprEntities, entityInfo, entityScoreMap, relMap, cfg.MaxContextEntities, cfg.MaxContextRelationships)
	}

	hydrateDur := time.Since(hydrateStart)

	return &SearchResult{
		Results:      finalResults,
		GraphContext: graphCtx,
		Stats: SearchStats{
			SeedChunks:        len(seedChunks),
			SeedEntities:      len(seedEntities),
			LexicalEntities:   len(lexicalEnts),
			SeedRelationships: len(seedRels),
			SubgraphNodes:     g.NodeCount(),
			SubgraphEdges:     g.EdgeCount(),
			PPRIterations:     pprIters,
			SeedDuration:      seedDur,
			ExpandDuration:    expandDur,
			PPRDuration:       pprDur,
			HydrateDuration:   hydrateDur,
			TotalDuration:     time.Since(totalStart),
		},
	}, nil
}

type scoredNode struct {
	id    string
	score float64
}

func buildGraphContext(
	pprEntities []scoredNode,
	entityInfo map[string]*types.Entity,
	entityScores map[string]float64,
	relMap map[string]*types.EntityRelationship,
	maxEntities, maxRels int,
) *types.GraphContext {
	sort.Slice(pprEntities, func(i, j int) bool {
		if pprEntities[i].score == pprEntities[j].score {
			return pprEntities[i].id < pprEntities[j].id
		}
		return pprEntities[i].score > pprEntities[j].score
	})

	ctxEntities := make([]types.GraphContextEntity, 0, min(len(pprEntities), maxEntities))
	topEntitySet := make(map[string]bool, maxEntities)
	for _, pe := range pprEntities {
		if len(ctxEntities) >= maxEntities {
			break
		}
		ent, ok := entityInfo[pe.id]
		if !ok || ent == nil {
			continue
		}
		topEntitySet[ent.ID] = true
		ctxEntities = append(ctxEntities, types.GraphContextEntity{
			ID:      ent.ID,
			Name:    ent.Name,
			Type:    ent.Type,
			Summary: ent.Summary,
			Score:   pe.score,
		})
	}

	type scoredRel struct {
		rel   *types.EntityRelationship
		score float64
	}
	candidates := make([]scoredRel, 0, len(relMap))
	for _, rel := range relMap {
		if !topEntitySet[rel.SourceEntityID] && !topEntitySet[rel.TargetEntityID] {
			continue
		}
		sScore := entityScores[rel.SourceEntityID]
		tScore := entityScores[rel.TargetEntityID]
		bothBonus := 1.0
		if topEntitySet[rel.SourceEntityID] && topEntitySet[rel.TargetEntityID] {
			bothBonus = 2.0
		}
		conf := rel.Confidence
		if conf <= 0 {
			conf = 0.5
		}
		candidates = append(candidates, scoredRel{
			rel:   rel,
			score: (sScore + tScore) * conf * bothBonus,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].rel.ID < candidates[j].rel.ID
		}
		return candidates[i].score > candidates[j].score
	})

	ctxRels := make([]types.GraphContextRelationship, 0, min(len(candidates), maxRels))
	for _, c := range candidates {
		if len(ctxRels) >= maxRels {
			break
		}
		srcName := c.rel.SourceEntityID
		if e, ok := entityInfo[c.rel.SourceEntityID]; ok && e != nil {
			srcName = e.Name
		}
		tgtName := c.rel.TargetEntityID
		if e, ok := entityInfo[c.rel.TargetEntityID]; ok && e != nil {
			tgtName = e.Name
		}
		ctxRels = append(ctxRels, types.GraphContextRelationship{
			Source:       srcName,
			Target:       tgtName,
			RelationType: c.rel.RelationType,
			Description:  c.rel.Description,
			Confidence:   c.rel.Confidence,
			Score:        c.score,
		})
	}

	return &types.GraphContext{
		Entities:      ctxEntities,
		Relationships: ctxRels,
	}
}

func normalizeConfig(cfg SearchConfig) SearchConfig {
	def := DefaultSearchConfig()
	if cfg.TopK <= 0 {
		cfg.TopK = def.TopK
	}
	if cfg.MaxHops <= 0 {
		cfg.MaxHops = def.MaxHops
	}
	if cfg.MaxHops > 2 {
		cfg.MaxHops = 2
	}
	if cfg.MaxDegree <= 0 {
		cfg.MaxDegree = def.MaxDegree
	}
	if cfg.MaxNodes <= 0 {
		cfg.MaxNodes = def.MaxNodes
	}
	if cfg.SeedChunksK <= 0 {
		cfg.SeedChunksK = def.SeedChunksK
	}
	if cfg.SeedEntitiesK <= 0 {
		cfg.SeedEntitiesK = def.SeedEntitiesK
	}
	if cfg.SeedRelationshipsK <= 0 {
		cfg.SeedRelationshipsK = def.SeedRelationshipsK
	}
	if cfg.EntityTeleportWeight <= 0 || cfg.EntityTeleportWeight > 1 {
		cfg.EntityTeleportWeight = def.EntityTeleportWeight
	}
	if cfg.EntityChunkEdgeScale <= 0 {
		cfg.EntityChunkEdgeScale = def.EntityChunkEdgeScale
	}
	if cfg.FactBoostWeight <= 0 {
		cfg.FactBoostWeight = def.FactBoostWeight
	}
	if cfg.LexicalAnchorWeight <= 0 {
		cfg.LexicalAnchorWeight = def.LexicalAnchorWeight
	}
	if cfg.SynonymWeight <= 0 {
		cfg.SynonymWeight = def.SynonymWeight
	}
	if cfg.RRFConstant <= 0 {
		cfg.RRFConstant = def.RRFConstant
	}
	if cfg.RelationshipMode == "" {
		cfg.RelationshipMode = def.RelationshipMode
	}
	if cfg.MaxContextEntities <= 0 {
		cfg.MaxContextEntities = def.MaxContextEntities
	}
	if cfg.MaxContextRelationships <= 0 {
		cfg.MaxContextRelationships = def.MaxContextRelationships
	}
	if cfg.PPR.Damping <= 0 || cfg.PPR.Damping >= 1 {
		cfg.PPR.Damping = def.PPR.Damping
	}
	if cfg.PPR.MaxIterations <= 0 {
		cfg.PPR.MaxIterations = def.PPR.MaxIterations
	}
	if cfg.PPR.Tolerance <= 0 {
		cfg.PPR.Tolerance = def.PPR.Tolerance
	}
	return cfg
}
