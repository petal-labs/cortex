package graphrag

import (
	"context"
	"time"

	"github.com/petal-labs/cortex/v2/internal/storage"
	"github.com/petal-labs/cortex/v2/pkg/types"
)

// RelationshipMode selects how relational triples are seeded at query time.
type RelationshipMode string

const (
	// RelationshipModeFTS (Variant B, default) uses FTS5 BM25 (SQLite) or
	// tsvector GIN (PostgreSQL) over relational triples.
	RelationshipModeFTS RelationshipMode = "fts"
	// RelationshipModeNone disables direct relationship seeding (ablation).
	RelationshipModeNone RelationshipMode = "none"
)

// SearchConfig configures zero-LLM HippoRAG-style graph retrieval.
type SearchConfig struct {
	TopK                    int
	CollectionID            *string
	Filters                 map[string]string
	MaxHops                 int
	MaxDegree               int
	MaxNodes                int
	SeedChunksK             int
	SeedEntitiesK           int
	SeedRelationshipsK      int
	EntityTeleportWeight    float64 // Fraction of PPR teleport mass on entities (rest on chunks)
	EntityChunkEdgeScale    float64 // Conductance scale for entity<->chunk edges relative to entity<->entity edges
	FactBoostWeight         float64 // Multiplier for relationship-endpoint seed scores
	LexicalAnchorWeight     float64 // Multiplier for lexical entity/alias matches in query text
	SynonymWeight           float64 // Weight multiplier for synonym_of edges
	RRFConstant             float64 // RRF constant k for seed hybrid searches (default: 60)
	RelationshipMode        RelationshipMode
	IncludeGraphContext     bool
	MaxContextEntities      int
	MaxContextRelationships int
	PPR                     PPRConfig
}

// DefaultSearchConfig returns tuned defaults for zero-LLM-at-query-time HippoRAG 2 (Variant B).
func DefaultSearchConfig() SearchConfig {
	return SearchConfig{
		TopK:                    5,
		MaxHops:                 2,
		MaxDegree:               15,
		MaxNodes:                500,
		SeedChunksK:             15,
		SeedEntitiesK:           15,
		SeedRelationshipsK:      15,
		EntityTeleportWeight:    0.75,
		EntityChunkEdgeScale:    0.25,
		FactBoostWeight:         1.5,
		LexicalAnchorWeight:     2.0,
		SynonymWeight:           0.8,
		RRFConstant:             60,
		RelationshipMode:        RelationshipModeFTS,
		IncludeGraphContext:     true,
		MaxContextEntities:      10,
		MaxContextRelationships: 15,
		PPR:                     DefaultPPRConfig(),
	}
}

// SearchStats captures per-stage timings and subgraph size for observability and benchmarking.
type SearchStats struct {
	SeedChunks        int
	SeedEntities      int
	LexicalEntities   int
	SeedRelationships int
	SubgraphNodes     int
	SubgraphEdges     int
	PPRIterations     int
	SeedDuration      time.Duration
	ExpandDuration    time.Duration
	PPRDuration       time.Duration
	HydrateDuration   time.Duration
	TotalDuration     time.Duration
}

// SearchResult is the output of a graph-augmented knowledge search.
type SearchResult struct {
	Results      []*types.ChunkResult
	GraphContext *types.GraphContext
	Stats        SearchStats
}

// GraphStore defines the read-only storage operations required by Retriever.
// Any storage.Backend (SQLite or pgvector) satisfies GraphStore directly.
type GraphStore interface {
	storage.HybridSearch
	storage.GraphStorage
}

// Compile-time check that storage.Backend satisfies GraphStore.
var _ GraphStore = (storage.Backend)(nil)

// Helper alias to keep context package imports clean.
var _ context.Context
