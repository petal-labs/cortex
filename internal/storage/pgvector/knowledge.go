package pgvector

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pgvector/pgvector-go"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// CreateCollection creates a new collection.
func (b *Backend) CreateCollection(ctx context.Context, col *types.Collection) error {
	if col.ID == "" {
		col.ID = uuid.New().String()
	}
	if col.CreatedAt.IsZero() {
		col.CreatedAt = time.Now().UTC()
	}

	_, err := b.pool.Exec(ctx, `
		INSERT INTO collections (id, namespace, name, description, chunk_strategy, chunk_max_tokens, chunk_overlap, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, col.ID, col.Namespace, col.Name, col.Description,
		col.ChunkConfig.Strategy, col.ChunkConfig.MaxTokens, col.ChunkConfig.Overlap,
		col.CreatedAt)

	if err != nil {
		if isUniqueConstraintError(err) {
			return storage.ErrAlreadyExists
		}
		return fmt.Errorf("failed to create collection: %w", err)
	}

	return nil
}

// GetCollection retrieves a collection by ID.
func (b *Backend) GetCollection(ctx context.Context, namespace, collectionID string) (*types.Collection, error) {
	col := &types.Collection{}
	var description *string
	var strategy string
	var maxTokens, overlap int

	err := b.pool.QueryRow(ctx, `
		SELECT id, namespace, name, description, chunk_strategy, chunk_max_tokens, chunk_overlap, created_at
		FROM collections
		WHERE id = $1 AND namespace = $2
	`, collectionID, namespace).Scan(
		&col.ID, &col.Namespace, &col.Name, &description,
		&strategy, &maxTokens, &overlap, &col.CreatedAt,
	)

	if err == pgx.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query collection: %w", err)
	}

	if description != nil {
		col.Description = *description
	}

	col.ChunkConfig = types.ChunkConfig{
		Strategy:  strategy,
		MaxTokens: maxTokens,
		Overlap:   overlap,
	}

	return col, nil
}

// ListCollections returns all collections in a namespace.
func (b *Backend) ListCollections(ctx context.Context, namespace string, cursor string, limit int) ([]*types.Collection, string, error) {
	if limit <= 0 {
		limit = 50
	}

	var args []any
	query := `
		SELECT id, namespace, name, description, chunk_strategy, chunk_max_tokens, chunk_overlap, created_at
		FROM collections
		WHERE namespace = $1
	`
	args = append(args, namespace)
	argNum := 2

	if cursor != "" {
		query += fmt.Sprintf(" AND created_at < $%d", argNum)
		args = append(args, cursor)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", argNum)
	args = append(args, limit+1)

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query collections: %w", err)
	}
	defer rows.Close()

	var collections []*types.Collection
	for rows.Next() {
		col := &types.Collection{}
		var description *string
		var strategy string
		var maxTokens, overlap int

		if err := rows.Scan(
			&col.ID, &col.Namespace, &col.Name, &description,
			&strategy, &maxTokens, &overlap, &col.CreatedAt,
		); err != nil {
			return nil, "", fmt.Errorf("failed to scan collection: %w", err)
		}

		if description != nil {
			col.Description = *description
		}

		col.ChunkConfig = types.ChunkConfig{
			Strategy:  strategy,
			MaxTokens: maxTokens,
			Overlap:   overlap,
		}

		collections = append(collections, col)
	}

	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("failed to iterate collections: %w", err)
	}

	var nextCursor string
	if len(collections) > limit {
		collections = collections[:limit]
		if len(collections) > 0 {
			nextCursor = collections[len(collections)-1].CreatedAt.Format(time.RFC3339Nano)
		}
	}

	return collections, nextCursor, nil
}

// DeleteCollection removes a collection and all its documents.
func (b *Backend) DeleteCollection(ctx context.Context, namespace, collectionID string) error {
	result, err := b.pool.Exec(ctx,
		"DELETE FROM collections WHERE id = $1 AND namespace = $2",
		collectionID, namespace,
	)
	if err != nil {
		return fmt.Errorf("failed to delete collection: %w", err)
	}

	if result.RowsAffected() == 0 {
		return storage.ErrNotFound
	}

	return nil
}

// InsertDocument adds a new document to the store.
func (b *Backend) InsertDocument(ctx context.Context, doc *types.Document) error {
	if doc.ID == "" {
		doc.ID = uuid.New().String()
	}
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = time.Now().UTC()
	}
	if doc.ContentType == "" {
		doc.ContentType = "text"
	}

	var metadataJSON []byte
	if doc.Metadata != nil {
		var err error
		metadataJSON, err = json.Marshal(doc.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	_, err := b.pool.Exec(ctx, `
		INSERT INTO documents (id, namespace, collection_id, title, source, content_type, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, doc.ID, doc.Namespace, doc.CollectionID, doc.Title, doc.Source,
		doc.ContentType, metadataJSON, doc.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to insert document: %w", err)
	}

	return nil
}

// InsertChunks adds chunks for a document. Embeddings should already be populated.
func (b *Backend) InsertChunks(ctx context.Context, chunks []*types.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, chunk := range chunks {
		if chunk.ID == "" {
			chunk.ID = uuid.New().String()
		}

		var metadataJSON []byte
		if chunk.Metadata != nil {
			metadataJSON, err = json.Marshal(chunk.Metadata)
			if err != nil {
				return fmt.Errorf("failed to marshal chunk metadata: %w", err)
			}
		}

		// Convert embedding to pgvector if present
		var embedding interface{}
		if len(chunk.Embedding) > 0 {
			embedding = pgvector.NewVector(chunk.Embedding)
		}

		_, err := tx.Exec(ctx, `
			INSERT INTO chunks (id, document_id, collection_id, namespace, content, sequence_num, token_count, metadata, embedding, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, chunk.ID, chunk.DocumentID, chunk.CollectionID, chunk.Namespace,
			chunk.Content, chunk.Index, chunk.TokenCount, metadataJSON, embedding, time.Now().UTC())

		if err != nil {
			return fmt.Errorf("failed to insert chunk: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// GetDocument retrieves a document by ID.
func (b *Backend) GetDocument(ctx context.Context, namespace, docID string) (*types.Document, error) {
	doc := &types.Document{}
	var title, source *string
	var metadataJSON []byte

	err := b.pool.QueryRow(ctx, `
		SELECT id, namespace, collection_id, title, source, content_type, metadata, created_at
		FROM documents
		WHERE id = $1 AND namespace = $2
	`, docID, namespace).Scan(
		&doc.ID, &doc.Namespace, &doc.CollectionID, &title, &source,
		&doc.ContentType, &metadataJSON, &doc.CreatedAt,
	)

	if err == pgx.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query document: %w", err)
	}

	if title != nil {
		doc.Title = *title
	}
	if source != nil {
		doc.Source = *source
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &doc.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	return doc, nil
}

// DeleteDocument removes a document and all its chunks.
func (b *Backend) DeleteDocument(ctx context.Context, namespace, docID string) error {
	result, err := b.pool.Exec(ctx,
		"DELETE FROM documents WHERE id = $1 AND namespace = $2",
		docID, namespace,
	)
	if err != nil {
		return fmt.Errorf("failed to delete document: %w", err)
	}

	if result.RowsAffected() == 0 {
		return storage.ErrNotFound
	}

	return nil
}

// GetAdjacentChunks retrieves chunks adjacent to a given chunk for context.
func (b *Backend) GetAdjacentChunks(ctx context.Context, chunkID string, window int) ([]*types.Chunk, error) {
	// First, get the chunk's document_id and sequence_num
	var documentID string
	var sequenceNum int
	err := b.pool.QueryRow(ctx,
		"SELECT document_id, sequence_num FROM chunks WHERE id = $1",
		chunkID,
	).Scan(&documentID, &sequenceNum)

	if err == pgx.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get chunk info: %w", err)
	}

	// Get adjacent chunks
	rows, err := b.pool.Query(ctx, `
		SELECT id, document_id, namespace, collection_id, content, sequence_num, token_count, metadata
		FROM chunks
		WHERE document_id = $1 AND sequence_num >= $2 AND sequence_num <= $3
		ORDER BY sequence_num
	`, documentID, sequenceNum-window, sequenceNum+window)
	if err != nil {
		return nil, fmt.Errorf("failed to query adjacent chunks: %w", err)
	}
	defer rows.Close()

	var chunks []*types.Chunk
	for rows.Next() {
		chunk := &types.Chunk{}
		var metadataJSON []byte
		var tokenCount *int

		if err := rows.Scan(
			&chunk.ID, &chunk.DocumentID, &chunk.Namespace, &chunk.CollectionID,
			&chunk.Content, &chunk.Index, &tokenCount, &metadataJSON,
		); err != nil {
			return nil, fmt.Errorf("failed to scan chunk: %w", err)
		}

		if tokenCount != nil {
			chunk.TokenCount = *tokenCount
		}

		if len(metadataJSON) > 0 {
			if err := json.Unmarshal(metadataJSON, &chunk.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal chunk metadata: %w", err)
			}
		}

		chunks = append(chunks, chunk)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate chunks: %w", err)
	}

	return chunks, nil
}

// CollectionStats returns statistics for a collection.
func (b *Backend) CollectionStats(ctx context.Context, namespace, collectionID string) (*types.CollectionStats, error) {
	// First verify collection exists
	var exists bool
	err := b.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM collections WHERE id = $1 AND namespace = $2)",
		collectionID, namespace,
	).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("failed to check collection existence: %w", err)
	}
	if !exists {
		return nil, storage.ErrNotFound
	}

	stats := &types.CollectionStats{}

	// Get document count
	err = b.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM documents WHERE collection_id = $1 AND namespace = $2",
		collectionID, namespace,
	).Scan(&stats.DocumentCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count documents: %w", err)
	}

	// Get chunk count and total tokens
	err = b.pool.QueryRow(ctx,
		"SELECT COUNT(*), COALESCE(SUM(token_count), 0) FROM chunks WHERE collection_id = $1 AND namespace = $2",
		collectionID, namespace,
	).Scan(&stats.ChunkCount, &stats.TotalTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to count chunks: %w", err)
	}

	// Get last ingest time
	var lastIngest *time.Time
	err = b.pool.QueryRow(ctx,
		"SELECT MAX(created_at) FROM documents WHERE collection_id = $1 AND namespace = $2",
		collectionID, namespace,
	).Scan(&lastIngest)
	if err != nil {
		return nil, fmt.Errorf("failed to get last ingest: %w", err)
	}
	if lastIngest != nil {
		stats.LastIngest = *lastIngest
	}

	return stats, nil
}

// SearchChunks performs semantic search across chunks in a namespace.
// Uses pgvector's HNSW index for approximate nearest neighbor search.
func (b *Backend) SearchChunks(ctx context.Context, namespace string, embedding []float32, opts storage.ChunkSearchOpts) ([]*types.ChunkResult, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	queryVector := pgvector.NewVector(embedding)

	// Build the query with joins for document info
	var args []any
	query := `
		SELECT c.id, c.document_id, c.namespace, c.collection_id, c.content,
		       c.sequence_num, c.token_count, c.metadata,
		       d.title, d.source, d.metadata AS doc_metadata,
		       1 - (c.embedding <=> $1) AS score
		FROM chunks c
		JOIN documents d ON d.id = c.document_id
		WHERE c.namespace = $2 AND c.embedding IS NOT NULL
	`
	args = append(args, queryVector, namespace)
	argNum := 3

	// Optional: filter by collection
	if opts.CollectionID != nil {
		query += fmt.Sprintf(" AND c.collection_id = $%d", argNum)
		args = append(args, *opts.CollectionID)
		argNum++
	}

	// Optional: minimum score filter
	if opts.MinScore > 0 {
		query += fmt.Sprintf(" AND 1 - (c.embedding <=> $1) >= $%d", argNum)
		args = append(args, opts.MinScore)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY c.embedding <=> $1 ASC LIMIT $%d", argNum)
	args = append(args, opts.TopK)

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search chunks: %w", err)
	}
	defer rows.Close()

	var results []*types.ChunkResult
	for rows.Next() {
		chunk := &types.Chunk{}
		result := &types.ChunkResult{Chunk: chunk}
		var chunkMetadataJSON, docMetadataJSON []byte
		var docTitle, docSource *string
		var tokenCount *int
		var score float64

		if err := rows.Scan(
			&chunk.ID, &chunk.DocumentID, &chunk.Namespace, &chunk.CollectionID,
			&chunk.Content, &chunk.Index, &tokenCount, &chunkMetadataJSON,
			&docTitle, &docSource, &docMetadataJSON, &score,
		); err != nil {
			return nil, fmt.Errorf("failed to scan chunk: %w", err)
		}

		if tokenCount != nil {
			chunk.TokenCount = *tokenCount
		}

		if len(chunkMetadataJSON) > 0 {
			if err := json.Unmarshal(chunkMetadataJSON, &chunk.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal chunk metadata: %w", err)
			}
		}

		if docTitle != nil {
			result.DocumentTitle = *docTitle
		}
		if docSource != nil {
			result.Source = *docSource
		}

		if len(docMetadataJSON) > 0 {
			if err := json.Unmarshal(docMetadataJSON, &result.DocMetadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal doc metadata: %w", err)
			}
		}

		// Apply metadata filters in Go
		if len(opts.Filters) > 0 {
			if chunk.Metadata == nil {
				continue
			}
			match := true
			for key, value := range opts.Filters {
				if chunk.Metadata[key] != value {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		result.Score = score
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate chunks: %w", err)
	}

	return results, nil
}
