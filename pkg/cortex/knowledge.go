package cortex

import (
	"context"
	"errors"
	"fmt"

	"github.com/petal-labs/cortex/v2/internal/knowledge"
	"github.com/petal-labs/cortex/v2/pkg/types"
)

// KnowledgeClient provides typed access to the Cortex Knowledge Store.
type KnowledgeClient struct {
	engine *knowledge.Engine
}

// CreateCollectionOpts configures KnowledgeClient.CreateCollection.
type CreateCollectionOpts struct {
	Name         string
	Description  string
	ChunkConfig  *types.ChunkConfig
	GraphEnabled bool
}

// IngestOpts configures KnowledgeClient.Ingest.
type IngestOpts struct {
	Title       string
	Source      string
	ContentType string
	Metadata    map[string]string
	ChunkConfig *types.ChunkConfig
}

// IngestResult contains the result of KnowledgeClient.Ingest.
type IngestResult struct {
	DocumentID    string `json:"document_id"`
	ChunksCreated int    `json:"chunks_created"`
	CollectionID  string `json:"collection_id"`
}

// BulkIngestDocument describes a single document for KnowledgeClient.BulkIngest.
type BulkIngestDocument struct {
	Content     string            `json:"content"`
	Title       string            `json:"title,omitempty"`
	Source      string            `json:"source,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// BulkIngestOpts configures KnowledgeClient.BulkIngest.
type BulkIngestOpts struct {
	ChunkConfig     *types.ChunkConfig
	Concurrency     int
	OnProgress      func(completed, total int, doc string)
	ContinueOnError bool
}

// BulkIngestDocResult contains the per-document outcome of KnowledgeClient.BulkIngest.
type BulkIngestDocResult struct {
	Index         int    `json:"index"`
	DocumentID    string `json:"document_id,omitempty"`
	Title         string `json:"title,omitempty"`
	ChunksCreated int    `json:"chunks_created"`
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
}

// BulkIngestResult contains the aggregate outcome of KnowledgeClient.BulkIngest.
type BulkIngestResult struct {
	CollectionID   string                 `json:"collection_id"`
	TotalDocuments int                    `json:"total_documents"`
	Succeeded      int                    `json:"succeeded"`
	Failed         int                    `json:"failed"`
	TotalChunks    int                    `json:"total_chunks"`
	Documents      []*BulkIngestDocResult `json:"documents"`
}

// KnowledgeSearchOpts configures KnowledgeClient.Search.
type KnowledgeSearchOpts struct {
	CollectionID        *string
	TopK                int
	MinScore            float64
	Filters             map[string]string
	ContextWindow       int
	SearchMode          SearchMode
	Alpha               float64
	IncludeGraphContext bool
	MaxHops             int
}

// KnowledgeSearchResult contains the result of KnowledgeClient.Search.
type KnowledgeSearchResult struct {
	Results      []*types.ChunkResult `json:"results"`
	GraphContext *types.GraphContext  `json:"graph_context,omitempty"`
	Query        string               `json:"query"`
	TotalFound   int                  `json:"total_found"`
}

// CreateCollection creates a new knowledge collection.
func (k *KnowledgeClient) CreateCollection(ctx context.Context, namespace string, opts CreateCollectionOpts) (*types.Collection, error) {
	col, err := k.engine.CreateCollection(ctx, namespace, knowledge.CreateCollectionOpts{
		Name:         opts.Name,
		Description:  opts.Description,
		ChunkConfig:  opts.ChunkConfig,
		GraphEnabled: opts.GraphEnabled,
	})
	if err != nil {
		return nil, mapKnowledgeErr(err)
	}
	return col, nil
}

// GetCollection retrieves a collection by ID.
func (k *KnowledgeClient) GetCollection(ctx context.Context, namespace, collectionID string) (*types.Collection, error) {
	col, err := k.engine.GetCollection(ctx, namespace, collectionID)
	if err != nil {
		return nil, mapKnowledgeErr(err)
	}
	return col, nil
}

// ListCollections lists collections in a namespace with cursor pagination.
func (k *KnowledgeClient) ListCollections(ctx context.Context, namespace, cursor string, limit int) ([]*types.Collection, string, error) {
	cols, nextCursor, err := k.engine.ListCollections(ctx, namespace, cursor, limit)
	if err != nil {
		return nil, "", mapKnowledgeErr(err)
	}
	return cols, nextCursor, nil
}

// DeleteCollection removes a collection and all its documents and chunks.
func (k *KnowledgeClient) DeleteCollection(ctx context.Context, namespace, collectionID string) error {
	return mapKnowledgeErr(k.engine.DeleteCollection(ctx, namespace, collectionID))
}

// Ingest chunks, embeds (if an embedding provider is configured), and stores a document.
func (k *KnowledgeClient) Ingest(ctx context.Context, namespace, collectionID, content string, opts *IngestOpts) (*IngestResult, error) {
	var internalOpts *knowledge.IngestOpts
	if opts != nil {
		internalOpts = &knowledge.IngestOpts{
			Title:       opts.Title,
			Source:      opts.Source,
			ContentType: opts.ContentType,
			Metadata:    opts.Metadata,
			ChunkConfig: opts.ChunkConfig,
		}
	}
	res, err := k.engine.Ingest(ctx, namespace, collectionID, content, internalOpts)
	if err != nil {
		return nil, mapKnowledgeErr(err)
	}
	return &IngestResult{
		DocumentID:    res.DocumentID,
		ChunksCreated: res.ChunksCreated,
		CollectionID:  res.CollectionID,
	}, nil
}

// BulkIngest ingests multiple documents concurrently into a collection.
func (k *KnowledgeClient) BulkIngest(ctx context.Context, namespace, collectionID string, documents []BulkIngestDocument, opts *BulkIngestOpts) (*BulkIngestResult, error) {
	internalDocs := make([]knowledge.BulkIngestDocument, len(documents))
	for i, d := range documents {
		internalDocs[i] = knowledge.BulkIngestDocument{
			Content:     d.Content,
			Title:       d.Title,
			Source:      d.Source,
			ContentType: d.ContentType,
			Metadata:    d.Metadata,
		}
	}
	var internalOpts *knowledge.BulkIngestOpts
	if opts != nil {
		internalOpts = &knowledge.BulkIngestOpts{
			ChunkConfig:     opts.ChunkConfig,
			Concurrency:     opts.Concurrency,
			OnProgress:      opts.OnProgress,
			ContinueOnError: opts.ContinueOnError,
		}
	}
	res, err := k.engine.BulkIngest(ctx, namespace, collectionID, internalDocs, internalOpts)
	if err != nil {
		return nil, mapKnowledgeErr(err)
	}
	docs := make([]*BulkIngestDocResult, len(res.Documents))
	for i, d := range res.Documents {
		if d != nil {
			docs[i] = &BulkIngestDocResult{
				Index:         d.Index,
				DocumentID:    d.DocumentID,
				Title:         d.Title,
				ChunksCreated: d.ChunksCreated,
				Success:       d.Success,
				Error:         d.Error,
			}
		}
	}
	return &BulkIngestResult{
		CollectionID:   res.CollectionID,
		TotalDocuments: res.TotalDocuments,
		Succeeded:      res.Succeeded,
		Failed:         res.Failed,
		TotalChunks:    res.TotalChunks,
		Documents:      docs,
	}, nil
}

// Search performs vector, hybrid, text, or Graph RAG search across knowledge chunks.
func (k *KnowledgeClient) Search(ctx context.Context, namespace, query string, opts *KnowledgeSearchOpts) (*KnowledgeSearchResult, error) {
	var internalOpts *knowledge.SearchOpts
	if opts != nil {
		internalOpts = &knowledge.SearchOpts{
			CollectionID:        opts.CollectionID,
			TopK:                opts.TopK,
			MinScore:            opts.MinScore,
			Filters:             opts.Filters,
			ContextWindow:       opts.ContextWindow,
			SearchMode:          knowledge.SearchMode(opts.SearchMode),
			Alpha:               opts.Alpha,
			IncludeGraphContext: opts.IncludeGraphContext,
			MaxHops:             opts.MaxHops,
		}
	}
	res, err := k.engine.Search(ctx, namespace, query, internalOpts)
	if err != nil {
		return nil, mapKnowledgeErr(err)
	}
	return &KnowledgeSearchResult{
		Results:      res.Results,
		GraphContext: res.GraphContext,
		Query:        res.Query,
		TotalFound:   res.TotalFound,
	}, nil
}

// GetDocument retrieves a document by ID.
func (k *KnowledgeClient) GetDocument(ctx context.Context, namespace, docID string) (*types.Document, error) {
	doc, err := k.engine.GetDocument(ctx, namespace, docID)
	if err != nil {
		return nil, mapKnowledgeErr(err)
	}
	return doc, nil
}

// DeleteDocument removes a document and all of its chunks.
func (k *KnowledgeClient) DeleteDocument(ctx context.Context, namespace, docID string) error {
	return mapKnowledgeErr(k.engine.DeleteDocument(ctx, namespace, docID))
}

// CollectionStats returns document, chunk, and token statistics for a collection.
func (k *KnowledgeClient) CollectionStats(ctx context.Context, namespace, collectionID string) (*types.CollectionStats, error) {
	stats, err := k.engine.CollectionStats(ctx, namespace, collectionID)
	if err != nil {
		return nil, mapKnowledgeErr(err)
	}
	return stats, nil
}

func mapKnowledgeErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, knowledge.ErrCollectionNotFound), errors.Is(err, knowledge.ErrDocumentNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, knowledge.ErrCollectionExists):
		return fmt.Errorf("%w: %v", ErrAlreadyExists, err)
	case errors.Is(err, knowledge.ErrEmptyContent):
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	default:
		return err
	}
}
