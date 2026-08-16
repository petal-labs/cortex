package dto

import (
	"time"

	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/pkg/types"
)

// ChunkConfig mirrors types.ChunkConfig.
type ChunkConfig struct {
	Strategy  string `json:"strategy"`
	MaxTokens int    `json:"max_tokens"`
	Overlap   int    `json:"overlap"`
	Separator string `json:"separator,omitempty"`
}

func newChunkConfig(c types.ChunkConfig) ChunkConfig {
	return ChunkConfig{
		Strategy:  c.Strategy,
		MaxTokens: c.MaxTokens,
		Overlap:   c.Overlap,
		Separator: c.Separator,
	}
}

// Collection mirrors types.Collection.
type Collection struct {
	ID          string      `json:"id"`
	Namespace   string      `json:"namespace"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	ChunkConfig ChunkConfig `json:"chunk_config"`
	CreatedAt   time.Time   `json:"created_at"`
}

func newCollection(c *types.Collection) *Collection {
	if c == nil {
		return nil
	}
	return &Collection{
		ID:          c.ID,
		Namespace:   c.Namespace,
		Name:        c.Name,
		Description: c.Description,
		ChunkConfig: newChunkConfig(c.ChunkConfig),
		CreatedAt:   c.CreatedAt,
	}
}

// Chunk mirrors types.Chunk.
type Chunk struct {
	ID           string            `json:"id"`
	DocumentID   string            `json:"document_id"`
	Namespace    string            `json:"namespace"`
	CollectionID string            `json:"collection_id"`
	Content      string            `json:"content"`
	Embedding    []float32         `json:"embedding,omitempty"`
	Index        int               `json:"index"`
	TokenCount   int               `json:"token_count,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

func newChunk(c *types.Chunk) *Chunk {
	if c == nil {
		return nil
	}
	return &Chunk{
		ID:           c.ID,
		DocumentID:   c.DocumentID,
		Namespace:    c.Namespace,
		CollectionID: c.CollectionID,
		Content:      c.Content,
		Embedding:    c.Embedding,
		Index:        c.Index,
		TokenCount:   c.TokenCount,
		Metadata:     c.Metadata,
	}
}

// ChunkResult mirrors types.ChunkResult.
type ChunkResult struct {
	Chunk         *Chunk            `json:"chunk"`
	Score         float64           `json:"score"`
	Rank          int               `json:"rank,omitempty"`
	DocumentTitle string            `json:"document_title"`
	Source        string            `json:"source"`
	DocMetadata   map[string]string `json:"doc_metadata,omitempty"`
	ContextBefore string            `json:"context_before,omitempty"`
	ContextAfter  string            `json:"context_after,omitempty"`
}

func newChunkResult(r *types.ChunkResult) *ChunkResult {
	if r == nil {
		return nil
	}
	return &ChunkResult{
		Chunk:         newChunk(r.Chunk),
		Score:         r.Score,
		Rank:          r.Rank,
		DocumentTitle: r.DocumentTitle,
		Source:        r.Source,
		DocMetadata:   r.DocMetadata,
		ContextBefore: r.ContextBefore,
		ContextAfter:  r.ContextAfter,
	}
}

// KnowledgeIngest is the knowledge_ingest response.
type KnowledgeIngest struct {
	Contract
	DocumentID    string `json:"document_id"`
	ChunksCreated int    `json:"chunks_created"`
	CollectionID  string `json:"collection_id"`
}

// NewKnowledgeIngest maps an engine ingest result.
func NewKnowledgeIngest(r *knowledge.IngestResult) KnowledgeIngest {
	if r == nil {
		return KnowledgeIngest{Contract: Contract{SchemaVersion}}
	}
	return KnowledgeIngest{
		Contract:      Contract{SchemaVersion},
		DocumentID:    r.DocumentID,
		ChunksCreated: r.ChunksCreated,
		CollectionID:  r.CollectionID,
	}
}

// KnowledgeSearch is the knowledge_search response.
type KnowledgeSearch struct {
	Contract
	Results    []ChunkResult `json:"results"`
	Query      string        `json:"query"`
	TotalFound int           `json:"total_found"`
}

// NewKnowledgeSearch maps an engine search result.
func NewKnowledgeSearch(r *knowledge.SearchResult) KnowledgeSearch {
	if r == nil {
		return KnowledgeSearch{Contract: Contract{SchemaVersion}, Results: []ChunkResult{}}
	}
	out := KnowledgeSearch{
		Contract:   Contract{SchemaVersion},
		Results:    make([]ChunkResult, 0, len(r.Results)),
		Query:      r.Query,
		TotalFound: r.TotalFound,
	}
	for _, res := range r.Results {
		out.Results = append(out.Results, *newChunkResult(res))
	}
	return out
}

// KnowledgeCollectionList is the knowledge_collections list response.
type KnowledgeCollectionList struct {
	Contract
	Collections []Collection `json:"collections"`
	NextCursor  string       `json:"next_cursor"`
}

// NewKnowledgeCollectionList maps a collections list result.
func NewKnowledgeCollectionList(cols []*types.Collection, nextCursor string) KnowledgeCollectionList {
	out := KnowledgeCollectionList{
		Contract:    Contract{SchemaVersion},
		Collections: make([]Collection, 0, len(cols)),
		NextCursor:  nextCursor,
	}
	for _, c := range cols {
		out.Collections = append(out.Collections, *newCollection(c))
	}
	return out
}

// KnowledgeCollectionCreated is the knowledge_collections create response.
type KnowledgeCollectionCreated struct {
	Contract
	Collection
}

// NewKnowledgeCollectionCreated maps a created collection.
func NewKnowledgeCollectionCreated(c *types.Collection) KnowledgeCollectionCreated {
	return KnowledgeCollectionCreated{Contract{SchemaVersion}, *newCollection(c)}
}

// KnowledgeCollectionDeleted is the knowledge_collections delete response.
type KnowledgeCollectionDeleted struct {
	Contract
	Deleted bool `json:"deleted"`
}

// NewKnowledgeCollectionDeleted maps a deleted collection response.
func NewKnowledgeCollectionDeleted() KnowledgeCollectionDeleted {
	return KnowledgeCollectionDeleted{Contract{SchemaVersion}, true}
}

// BulkIngestDoc mirrors knowledge.BulkIngestDocResult.
type BulkIngestDoc struct {
	Index         int    `json:"index"`
	DocumentID    string `json:"document_id,omitempty"`
	Title         string `json:"title,omitempty"`
	ChunksCreated int    `json:"chunks_created"`
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
}

// KnowledgeBulkIngest is the knowledge_bulk_ingest response.
type KnowledgeBulkIngest struct {
	Contract
	CollectionID   string          `json:"collection_id"`
	TotalDocuments int             `json:"total_documents"`
	Succeeded      int             `json:"succeeded"`
	Failed         int             `json:"failed"`
	TotalChunks    int             `json:"total_chunks"`
	Documents      []BulkIngestDoc `json:"documents"`
}

// NewKnowledgeBulkIngest maps an engine bulk ingest result.
func NewKnowledgeBulkIngest(r *knowledge.BulkIngestResult) KnowledgeBulkIngest {
	if r == nil {
		return KnowledgeBulkIngest{Contract: Contract{SchemaVersion}, Documents: []BulkIngestDoc{}}
	}
	out := KnowledgeBulkIngest{
		Contract:       Contract{SchemaVersion},
		CollectionID:   r.CollectionID,
		TotalDocuments: r.TotalDocuments,
		Succeeded:      r.Succeeded,
		Failed:         r.Failed,
		TotalChunks:    r.TotalChunks,
		Documents:      make([]BulkIngestDoc, 0, len(r.Documents)),
	}
	for _, d := range r.Documents {
		if d == nil {
			continue
		}
		out.Documents = append(out.Documents, BulkIngestDoc{
			Index:         d.Index,
			DocumentID:    d.DocumentID,
			Title:         d.Title,
			ChunksCreated: d.ChunksCreated,
			Success:       d.Success,
			Error:         d.Error,
		})
	}
	return out
}
