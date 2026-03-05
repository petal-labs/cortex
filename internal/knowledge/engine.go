package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// Common errors returned by the knowledge engine.
var (
	ErrEmptyContent        = errors.New("document content cannot be empty")
	ErrCollectionNotFound  = errors.New("collection not found")
	ErrDocumentNotFound    = errors.New("document not found")
	ErrCollectionExists    = errors.New("collection already exists")
	ErrEmbeddingRequired   = errors.New("embedding provider required for search")
	ErrInvalidChunkConfig  = errors.New("invalid chunk configuration")
)

// ExtractionEnqueuer queues content for entity extraction.
type ExtractionEnqueuer interface {
	EnqueueForExtraction(ctx context.Context, namespace, sourceType, sourceID, content string) error
}

// Engine implements the knowledge store logic layer.
// It orchestrates chunking, embedding, and storage operations.
type Engine struct {
	storage            storage.Backend
	embedding          embedding.Provider
	chunker            Chunker
	semanticChunker    *SemanticChunker
	extractionEnqueuer ExtractionEnqueuer
	cfg                *config.KnowledgeConfig
}

// NewEngine creates a new knowledge engine.
func NewEngine(store storage.Backend, emb embedding.Provider, cfg *config.KnowledgeConfig) (*Engine, error) {
	if store == nil {
		return nil, errors.New("storage backend is required")
	}

	if cfg == nil {
		defaultCfg := config.DefaultConfig()
		cfg = &defaultCfg.Knowledge
	}

	e := &Engine{
		storage:   store,
		embedding: emb,
		chunker:   NewChunker(),
		cfg:       cfg,
	}

	// Initialize semantic chunker if embedding provider is available
	if emb != nil {
		e.semanticChunker = NewSemanticChunker(emb)
	}

	return e, nil
}

// SetExtractionEnqueuer sets the extraction enqueuer for entity extraction.
// When set, ingested chunks will be queued for background entity extraction.
func (e *Engine) SetExtractionEnqueuer(eq ExtractionEnqueuer) {
	e.extractionEnqueuer = eq
}

// enqueueForExtraction queues content for entity extraction.
// This is a fire-and-forget operation that logs errors but doesn't fail the caller.
func (e *Engine) enqueueForExtraction(ctx context.Context, namespace, chunkID, content string) {
	if e.extractionEnqueuer == nil {
		return
	}

	// Fire and forget - extraction is best-effort
	if err := e.extractionEnqueuer.EnqueueForExtraction(ctx, namespace, "knowledge", chunkID, content); err != nil {
		// Log but don't fail - extraction is optional
		// In production, use structured logging
		_ = err
	}
}

// IngestOpts contains options for document ingestion.
type IngestOpts struct {
	Title       string            // Optional document title
	Source      string            // Source URL or identifier
	ContentType string            // "text", "markdown", "html"
	Metadata    map[string]string // Optional metadata
	ChunkConfig *types.ChunkConfig // Override collection's default config
}

// IngestResult contains the result of document ingestion.
type IngestResult struct {
	DocumentID    string `json:"document_id"`
	ChunksCreated int    `json:"chunks_created"`
	CollectionID  string `json:"collection_id"`
}

// Ingest adds a document to a collection, chunking and generating embeddings.
func (e *Engine) Ingest(ctx context.Context, namespace, collectionID, content string, opts *IngestOpts) (*IngestResult, error) {
	if content == "" {
		return nil, ErrEmptyContent
	}

	if opts == nil {
		opts = &IngestOpts{}
	}

	// Verify collection exists
	collection, err := e.storage.GetCollection(ctx, namespace, collectionID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrCollectionNotFound
		}
		return nil, fmt.Errorf("failed to get collection: %w", err)
	}

	// Determine chunk config
	chunkConfig := collection.ChunkConfig
	if opts.ChunkConfig != nil {
		chunkConfig = *opts.ChunkConfig
	}

	// Create document
	docID := uuid.New().String()
	contentType := opts.ContentType
	if contentType == "" {
		contentType = "text"
	}

	now := time.Now().UTC()
	doc := &types.Document{
		ID:           docID,
		Namespace:    namespace,
		CollectionID: collectionID,
		Title:        opts.Title,
		Content:      content,
		ContentType:  contentType,
		Source:       opts.Source,
		Metadata:     opts.Metadata,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// Store document
	if err := e.storage.InsertDocument(ctx, doc); err != nil {
		return nil, fmt.Errorf("failed to insert document: %w", err)
	}

	// Chunk content
	var chunkOutputs []ChunkOutput
	if chunkConfig.Strategy == string(ChunkStrategySemantic) && e.semanticChunker != nil {
		// Use semantic chunker for embedding-based chunking
		var err error
		chunkOutputs, err = e.semanticChunker.Chunk(ctx, content, chunkConfig)
		if err != nil {
			// Fall back to sentence chunking on semantic chunking failure
			chunkOutputs = e.chunker.Chunk(content, types.ChunkConfig{
				Strategy:  string(ChunkStrategySentence),
				MaxTokens: chunkConfig.MaxTokens,
				Overlap:   chunkConfig.Overlap,
			})
		}
	} else {
		chunkOutputs = e.chunker.Chunk(content, chunkConfig)
	}
	if len(chunkOutputs) == 0 {
		// Document stored but no chunks created (empty after processing)
		return &IngestResult{
			DocumentID:    docID,
			ChunksCreated: 0,
			CollectionID:  collectionID,
		}, nil
	}

	// Generate embeddings for all chunks
	chunkTexts := make([]string, len(chunkOutputs))
	for i, co := range chunkOutputs {
		chunkTexts[i] = co.Content
	}

	var embeddings [][]float32
	if e.embedding != nil {
		embeddings, err = e.embedding.EmbedBatch(ctx, chunkTexts)
		if err != nil {
			// Log but don't fail - chunks will be stored without embeddings
			embeddings = nil
		}
	}

	// Create chunk records
	chunks := make([]*types.Chunk, len(chunkOutputs))
	for i, co := range chunkOutputs {
		chunkID := uuid.New().String()
		chunk := &types.Chunk{
			ID:           chunkID,
			DocumentID:   docID,
			Namespace:    namespace,
			CollectionID: collectionID,
			Content:      co.Content,
			Index:        co.Index,
			TokenCount:   co.TokenCount,
			Metadata:     opts.Metadata, // Inherit document metadata
		}

		// Add embedding if available
		if embeddings != nil && i < len(embeddings) {
			chunk.Embedding = embeddings[i]
		}

		chunks[i] = chunk
	}

	// Store chunks
	if err := e.storage.InsertChunks(ctx, chunks); err != nil {
		return nil, fmt.Errorf("failed to insert chunks: %w", err)
	}

	// Queue chunks for entity extraction
	for _, chunk := range chunks {
		e.enqueueForExtraction(ctx, namespace, chunk.ID, chunk.Content)
	}

	return &IngestResult{
		DocumentID:    docID,
		ChunksCreated: len(chunks),
		CollectionID:  collectionID,
	}, nil
}

// BulkIngestDocument represents a single document for bulk ingestion.
type BulkIngestDocument struct {
	Content     string            `json:"content"`
	Title       string            `json:"title,omitempty"`
	Source      string            `json:"source,omitempty"`
	ContentType string            `json:"content_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// BulkIngestOpts contains options for bulk document ingestion.
type BulkIngestOpts struct {
	ChunkConfig    *types.ChunkConfig                    // Override collection's default config
	Concurrency    int                                   // Number of concurrent workers (0 = default 4)
	OnProgress     func(completed, total int, doc string) // Optional progress callback
	ContinueOnError bool                                  // Continue processing on individual document errors
}

// BulkIngestDocResult contains the result for a single document.
type BulkIngestDocResult struct {
	Index         int    `json:"index"`
	DocumentID    string `json:"document_id,omitempty"`
	Title         string `json:"title,omitempty"`
	ChunksCreated int    `json:"chunks_created"`
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
}

// BulkIngestResult contains the overall result of bulk ingestion.
type BulkIngestResult struct {
	CollectionID    string                 `json:"collection_id"`
	TotalDocuments  int                    `json:"total_documents"`
	Succeeded       int                    `json:"succeeded"`
	Failed          int                    `json:"failed"`
	TotalChunks     int                    `json:"total_chunks"`
	Documents       []*BulkIngestDocResult `json:"documents"`
}

// BulkIngest ingests multiple documents into a collection with progress reporting.
// Documents are processed concurrently for efficiency.
func (e *Engine) BulkIngest(ctx context.Context, namespace, collectionID string, documents []BulkIngestDocument, opts *BulkIngestOpts) (*BulkIngestResult, error) {
	if len(documents) == 0 {
		return nil, errors.New("no documents provided")
	}

	// Verify collection exists
	collection, err := e.storage.GetCollection(ctx, namespace, collectionID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrCollectionNotFound
		}
		return nil, fmt.Errorf("failed to get collection: %w", err)
	}

	if opts == nil {
		opts = &BulkIngestOpts{}
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	// Cap concurrency at document count
	if concurrency > len(documents) {
		concurrency = len(documents)
	}

	// Determine chunk config
	chunkConfig := collection.ChunkConfig
	if opts.ChunkConfig != nil {
		chunkConfig = *opts.ChunkConfig
	}

	result := &BulkIngestResult{
		CollectionID:   collectionID,
		TotalDocuments: len(documents),
		Documents:      make([]*BulkIngestDocResult, len(documents)),
	}

	// Create work channel and results channel
	type workItem struct {
		index int
		doc   BulkIngestDocument
	}

	workCh := make(chan workItem, len(documents))
	resultCh := make(chan *BulkIngestDocResult, len(documents))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for work := range workCh {
				docResult := e.ingestSingleDocument(ctx, namespace, collectionID, work.doc, work.index, chunkConfig)
				resultCh <- docResult
			}
		}()
	}

	// Send work items
	go func() {
		for i, doc := range documents {
			workCh <- workItem{index: i, doc: doc}
		}
		close(workCh)
	}()

	// Wait for workers and close result channel
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results with progress reporting
	completed := 0
	for docResult := range resultCh {
		result.Documents[docResult.Index] = docResult
		completed++

		if docResult.Success {
			result.Succeeded++
			result.TotalChunks += docResult.ChunksCreated
		} else {
			result.Failed++
		}

		// Report progress if callback provided
		if opts.OnProgress != nil {
			title := docResult.Title
			if title == "" {
				title = fmt.Sprintf("Document %d", docResult.Index+1)
			}
			opts.OnProgress(completed, len(documents), title)
		}
	}

	return result, nil
}

// ingestSingleDocument ingests a single document and returns the result.
func (e *Engine) ingestSingleDocument(ctx context.Context, namespace, collectionID string, doc BulkIngestDocument, index int, chunkConfig types.ChunkConfig) *BulkIngestDocResult {
	result := &BulkIngestDocResult{
		Index: index,
		Title: doc.Title,
	}

	if doc.Content == "" {
		result.Error = "empty content"
		return result
	}

	ingestOpts := &IngestOpts{
		Title:       doc.Title,
		Source:      doc.Source,
		ContentType: doc.ContentType,
		Metadata:    doc.Metadata,
		ChunkConfig: &chunkConfig,
	}

	ingestResult, err := e.Ingest(ctx, namespace, collectionID, doc.Content, ingestOpts)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.DocumentID = ingestResult.DocumentID
	result.ChunksCreated = ingestResult.ChunksCreated
	result.Success = true

	return result
}

// SearchMode defines the search strategy.
type SearchMode string

const (
	// SearchModeVector uses pure vector similarity search (default).
	SearchModeVector SearchMode = "vector"
	// SearchModeHybrid combines vector and text search with RRF.
	SearchModeHybrid SearchMode = "hybrid"
	// SearchModeText uses pure full-text search (BM25).
	SearchModeText SearchMode = "text"
)

// SearchOpts contains options for knowledge search.
type SearchOpts struct {
	CollectionID   *string           // Optional: limit to specific collection
	TopK           int               // Number of results (0 = default 10)
	MinScore       float64           // Minimum similarity score (0-1)
	Filters        map[string]string // Metadata filters
	ContextWindow  int               // Chunks before/after to include (0 = none)
	SearchMode     SearchMode        // Search mode: "vector" (default), "hybrid", or "text"
	Alpha          float64           // Hybrid search weight: 0=pure text, 1=pure vector, 0.5=equal (default: 0.5)
}

// SearchResult contains search results with optional context.
type SearchResult struct {
	Results    []*types.ChunkResult `json:"results"`
	Query      string               `json:"query"`
	TotalFound int                  `json:"total_found"`
}

// Search performs semantic search across knowledge in a namespace.
func (e *Engine) Search(ctx context.Context, namespace, query string, opts *SearchOpts) (*SearchResult, error) {
	if query == "" {
		return nil, errors.New("search query cannot be empty")
	}

	if opts == nil {
		opts = &SearchOpts{}
	}

	topK := opts.TopK
	if topK <= 0 {
		topK = 10
	}

	// Set default alpha for hybrid search
	alpha := opts.Alpha
	if alpha == 0 && opts.SearchMode == SearchModeHybrid {
		alpha = 0.5 // Default to equal weighting
	}

	var results []*types.ChunkResult
	var err error

	switch opts.SearchMode {
	case SearchModeText:
		// Pure text search - use hybrid with alpha=0 (pure text)
		if e.embedding == nil {
			return nil, ErrEmbeddingRequired
		}
		queryEmb, embErr := e.embedding.Embed(ctx, query)
		if embErr != nil {
			return nil, fmt.Errorf("failed to generate query embedding: %w", embErr)
		}

		hybridOpts := storage.HybridChunkSearchOpts{
			TopK:         topK,
			MinScore:     opts.MinScore,
			CollectionID: opts.CollectionID,
			Filters:      opts.Filters,
			Alpha:        0.0, // Pure text search
			RRFConstant:  60,
		}
		results, err = e.storage.HybridSearchChunks(ctx, namespace, query, queryEmb, hybridOpts)

	case SearchModeHybrid:
		// Hybrid search combining vector and text
		if e.embedding == nil {
			return nil, ErrEmbeddingRequired
		}
		queryEmb, embErr := e.embedding.Embed(ctx, query)
		if embErr != nil {
			return nil, fmt.Errorf("failed to generate query embedding: %w", embErr)
		}

		hybridOpts := storage.HybridChunkSearchOpts{
			TopK:         topK,
			MinScore:     opts.MinScore,
			CollectionID: opts.CollectionID,
			Filters:      opts.Filters,
			Alpha:        alpha,
			RRFConstant:  60,
		}
		results, err = e.storage.HybridSearchChunks(ctx, namespace, query, queryEmb, hybridOpts)

	default:
		// Pure vector search (default)
		if e.embedding == nil {
			return nil, ErrEmbeddingRequired
		}
		queryEmb, embErr := e.embedding.Embed(ctx, query)
		if embErr != nil {
			return nil, fmt.Errorf("failed to generate query embedding: %w", embErr)
		}

		searchOpts := storage.ChunkSearchOpts{
			TopK:         topK,
			MinScore:     opts.MinScore,
			CollectionID: opts.CollectionID,
			Filters:      opts.Filters,
		}
		results, err = e.storage.SearchChunks(ctx, namespace, queryEmb, searchOpts)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to search chunks: %w", err)
	}

	// Expand context if requested
	if opts.ContextWindow > 0 {
		for _, result := range results {
			adjacentChunks, err := e.storage.GetAdjacentChunks(ctx, result.Chunk.ID, opts.ContextWindow)
			if err != nil {
				continue // Skip context expansion on error
			}

			// Build context strings
			var beforeParts, afterParts []string
			targetIndex := result.Chunk.Index
			for _, adj := range adjacentChunks {
				if adj.Index < targetIndex {
					beforeParts = append(beforeParts, adj.Content)
				} else if adj.Index > targetIndex {
					afterParts = append(afterParts, adj.Content)
				}
			}

			if len(beforeParts) > 0 {
				result.ContextBefore = joinChunks(beforeParts)
			}
			if len(afterParts) > 0 {
				result.ContextAfter = joinChunks(afterParts)
			}
		}
	}

	return &SearchResult{
		Results:    results,
		Query:      query,
		TotalFound: len(results),
	}, nil
}

// GetDocument retrieves a document by ID.
func (e *Engine) GetDocument(ctx context.Context, namespace, docID string) (*types.Document, error) {
	doc, err := e.storage.GetDocument(ctx, namespace, docID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrDocumentNotFound
		}
		return nil, fmt.Errorf("failed to get document: %w", err)
	}
	return doc, nil
}

// DeleteDocument removes a document and all its chunks.
func (e *Engine) DeleteDocument(ctx context.Context, namespace, docID string) error {
	err := e.storage.DeleteDocument(ctx, namespace, docID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrDocumentNotFound
		}
		return fmt.Errorf("failed to delete document: %w", err)
	}
	return nil
}

// CreateCollectionOpts contains options for creating a collection.
type CreateCollectionOpts struct {
	Name        string            // Required: collection name
	Description string            // Optional description
	ChunkConfig *types.ChunkConfig // Chunk configuration (uses default if nil)
}

// CreateCollection creates a new collection.
func (e *Engine) CreateCollection(ctx context.Context, namespace string, opts CreateCollectionOpts) (*types.Collection, error) {
	if opts.Name == "" {
		return nil, errors.New("collection name is required")
	}

	collectionID := uuid.New().String()
	chunkConfig := types.DefaultChunkConfig()
	if opts.ChunkConfig != nil {
		chunkConfig = *opts.ChunkConfig
	}

	col := &types.Collection{
		ID:          collectionID,
		Namespace:   namespace,
		Name:        opts.Name,
		Description: opts.Description,
		ChunkConfig: chunkConfig,
		CreatedAt:   time.Now().UTC(),
	}

	if err := e.storage.CreateCollection(ctx, col); err != nil {
		if errors.Is(err, storage.ErrAlreadyExists) {
			return nil, ErrCollectionExists
		}
		return nil, fmt.Errorf("failed to create collection: %w", err)
	}

	return col, nil
}

// GetCollection retrieves a collection by ID.
func (e *Engine) GetCollection(ctx context.Context, namespace, collectionID string) (*types.Collection, error) {
	col, err := e.storage.GetCollection(ctx, namespace, collectionID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrCollectionNotFound
		}
		return nil, fmt.Errorf("failed to get collection: %w", err)
	}
	return col, nil
}

// ListCollections returns all collections in a namespace.
func (e *Engine) ListCollections(ctx context.Context, namespace, cursor string, limit int) ([]*types.Collection, string, error) {
	if limit <= 0 {
		limit = 50
	}

	collections, nextCursor, err := e.storage.ListCollections(ctx, namespace, cursor, limit)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list collections: %w", err)
	}

	return collections, nextCursor, nil
}

// DeleteCollection removes a collection and all its documents.
func (e *Engine) DeleteCollection(ctx context.Context, namespace, collectionID string) error {
	err := e.storage.DeleteCollection(ctx, namespace, collectionID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrCollectionNotFound
		}
		return fmt.Errorf("failed to delete collection: %w", err)
	}
	return nil
}

// CollectionStats returns statistics for a collection.
func (e *Engine) CollectionStats(ctx context.Context, namespace, collectionID string) (*types.CollectionStats, error) {
	stats, err := e.storage.CollectionStats(ctx, namespace, collectionID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrCollectionNotFound
		}
		return nil, fmt.Errorf("failed to get collection stats: %w", err)
	}
	return stats, nil
}

// joinChunks concatenates chunk contents with paragraph breaks.
func joinChunks(contents []string) string {
	if len(contents) == 0 {
		return ""
	}
	if len(contents) == 1 {
		return contents[0]
	}

	var builder strings.Builder
	for i, c := range contents {
		if i > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(c)
	}
	return builder.String()
}
