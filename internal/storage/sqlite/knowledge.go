package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
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

	chunkConfigJSON, err := json.Marshal(col.ChunkConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal chunk config: %w", err)
	}

	_, err = b.db.ExecContext(ctx, `
		INSERT INTO collections (id, namespace, name, description, chunk_config, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, col.ID, col.Namespace, col.Name, col.Description, chunkConfigJSON, col.CreatedAt.Unix())

	if err != nil {
		// Check for unique constraint violation
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
	var description, chunkConfigJSON sql.NullString
	var createdAtUnix int64

	err := b.db.QueryRowContext(ctx, `
		SELECT id, namespace, name, description, chunk_config, created_at
		FROM collections
		WHERE id = ? AND namespace = ?
	`, collectionID, namespace).Scan(&col.ID, &col.Namespace, &col.Name, &description, &chunkConfigJSON, &createdAtUnix)

	if err == sql.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query collection: %w", err)
	}

	col.Description = description.String
	col.CreatedAt = time.Unix(createdAtUnix, 0).UTC()

	if chunkConfigJSON.Valid && chunkConfigJSON.String != "" {
		if err := json.Unmarshal([]byte(chunkConfigJSON.String), &col.ChunkConfig); err != nil {
			return nil, fmt.Errorf("failed to unmarshal chunk config: %w", err)
		}
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
		SELECT id, namespace, name, description, chunk_config, created_at
		FROM collections
		WHERE namespace = ?
	`
	args = append(args, namespace)

	if cursor != "" {
		query += " AND created_at < ?"
		args = append(args, cursor)
	}

	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query collections: %w", err)
	}
	defer rows.Close()

	var collections []*types.Collection
	for rows.Next() {
		col := &types.Collection{}
		var description, chunkConfigJSON sql.NullString
		var createdAtUnix int64

		if err := rows.Scan(&col.ID, &col.Namespace, &col.Name, &description, &chunkConfigJSON, &createdAtUnix); err != nil {
			return nil, "", fmt.Errorf("failed to scan collection: %w", err)
		}

		col.Description = description.String
		col.CreatedAt = time.Unix(createdAtUnix, 0).UTC()

		if chunkConfigJSON.Valid && chunkConfigJSON.String != "" {
			if err := json.Unmarshal([]byte(chunkConfigJSON.String), &col.ChunkConfig); err != nil {
				return nil, "", fmt.Errorf("failed to unmarshal chunk config: %w", err)
			}
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
			nextCursor = fmt.Sprintf("%d", collections[len(collections)-1].CreatedAt.Unix())
		}
	}

	return collections, nextCursor, nil
}

// DeleteCollection removes a collection and all its documents.
func (b *Backend) DeleteCollection(ctx context.Context, namespace, collectionID string) error {
	result, err := b.db.ExecContext(ctx,
		"DELETE FROM collections WHERE id = ? AND namespace = ?",
		collectionID, namespace,
	)
	if err != nil {
		return fmt.Errorf("failed to delete collection: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
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
	if doc.UpdatedAt.IsZero() {
		doc.UpdatedAt = doc.CreatedAt
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

	_, err := b.db.ExecContext(ctx, `
		INSERT INTO documents (id, namespace, collection_id, title, content, content_type, source, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, doc.ID, doc.Namespace, doc.CollectionID, doc.Title, doc.Content, doc.ContentType, doc.Source, metadataJSON, doc.CreatedAt.Unix(), doc.UpdatedAt.Unix())

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

	return b.withTx(ctx, func(tx *sql.Tx) error {
		// Prepare statements
		chunkStmt, err := tx.PrepareContext(ctx, `
			INSERT INTO chunks (id, document_id, namespace, collection_id, content, chunk_index, token_count, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("failed to prepare chunk statement: %w", err)
		}
		defer chunkStmt.Close()

		embeddingStmt, err := tx.PrepareContext(ctx, `
			INSERT INTO chunk_embeddings (chunk_id, embedding)
			VALUES (?, ?)
		`)
		if err != nil {
			return fmt.Errorf("failed to prepare embedding statement: %w", err)
		}
		defer embeddingStmt.Close()

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

			// Insert chunk
			_, err := chunkStmt.ExecContext(ctx,
				chunk.ID, chunk.DocumentID, chunk.Namespace, chunk.CollectionID,
				chunk.Content, chunk.Index, chunk.TokenCount, metadataJSON,
			)
			if err != nil {
				return fmt.Errorf("failed to insert chunk: %w", err)
			}

			// Insert embedding if present (binary format for sqlite-vec)
			if len(chunk.Embedding) > 0 {
				embeddingBytes := encodeVectorBinary(chunk.Embedding)
				_, err = embeddingStmt.ExecContext(ctx, chunk.ID, embeddingBytes)
				if err != nil {
					return fmt.Errorf("failed to insert chunk embedding: %w", err)
				}
			}
		}

		return nil
	})
}

// GetDocument retrieves a document by ID.
func (b *Backend) GetDocument(ctx context.Context, namespace, docID string) (*types.Document, error) {
	doc := &types.Document{}
	var title, source, metadataJSON sql.NullString
	var createdAtUnix, updatedAtUnix int64

	err := b.db.QueryRowContext(ctx, `
		SELECT id, namespace, collection_id, title, content, content_type, source, metadata, created_at, updated_at
		FROM documents
		WHERE id = ? AND namespace = ?
	`, docID, namespace).Scan(
		&doc.ID, &doc.Namespace, &doc.CollectionID, &title, &doc.Content,
		&doc.ContentType, &source, &metadataJSON, &createdAtUnix, &updatedAtUnix,
	)

	if err == sql.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query document: %w", err)
	}

	doc.Title = title.String
	doc.Source = source.String
	doc.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
	doc.UpdatedAt = time.Unix(updatedAtUnix, 0).UTC()

	if metadataJSON.Valid && metadataJSON.String != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &doc.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	return doc, nil
}

// DeleteDocument removes a document and all its chunks.
func (b *Backend) DeleteDocument(ctx context.Context, namespace, docID string) error {
	result, err := b.db.ExecContext(ctx,
		"DELETE FROM documents WHERE id = ? AND namespace = ?",
		docID, namespace,
	)
	if err != nil {
		return fmt.Errorf("failed to delete document: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return storage.ErrNotFound
	}

	return nil
}

// GetAdjacentChunks retrieves chunks adjacent to a given chunk for context.
func (b *Backend) GetAdjacentChunks(ctx context.Context, chunkID string, window int) ([]*types.Chunk, error) {
	// First, get the chunk's document_id and index
	var documentID string
	var chunkIndex int
	err := b.db.QueryRowContext(ctx,
		"SELECT document_id, chunk_index FROM chunks WHERE id = ?",
		chunkID,
	).Scan(&documentID, &chunkIndex)

	if err == sql.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get chunk info: %w", err)
	}

	// Get adjacent chunks
	rows, err := b.db.QueryContext(ctx, `
		SELECT id, document_id, namespace, collection_id, content, chunk_index, token_count, metadata
		FROM chunks
		WHERE document_id = ? AND chunk_index >= ? AND chunk_index <= ?
		ORDER BY chunk_index
	`, documentID, chunkIndex-window, chunkIndex+window)
	if err != nil {
		return nil, fmt.Errorf("failed to query adjacent chunks: %w", err)
	}
	defer rows.Close()

	var chunks []*types.Chunk
	for rows.Next() {
		chunk := &types.Chunk{}
		var metadataJSON sql.NullString
		var tokenCount sql.NullInt64

		if err := rows.Scan(
			&chunk.ID, &chunk.DocumentID, &chunk.Namespace, &chunk.CollectionID,
			&chunk.Content, &chunk.Index, &tokenCount, &metadataJSON,
		); err != nil {
			return nil, fmt.Errorf("failed to scan chunk: %w", err)
		}

		if tokenCount.Valid {
			chunk.TokenCount = int(tokenCount.Int64)
		}

		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &chunk.Metadata); err != nil {
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
	err := b.db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM collections WHERE id = ? AND namespace = ?)",
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
	err = b.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM documents WHERE collection_id = ? AND namespace = ?",
		collectionID, namespace,
	).Scan(&stats.DocumentCount)
	if err != nil {
		return nil, fmt.Errorf("failed to count documents: %w", err)
	}

	// Get chunk count and total tokens
	err = b.db.QueryRowContext(ctx,
		"SELECT COUNT(*), COALESCE(SUM(token_count), 0) FROM chunks WHERE collection_id = ? AND namespace = ?",
		collectionID, namespace,
	).Scan(&stats.ChunkCount, &stats.TotalTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to count chunks: %w", err)
	}

	// Get last ingest time
	var lastIngestUnix sql.NullInt64
	err = b.db.QueryRowContext(ctx,
		"SELECT MAX(created_at) FROM documents WHERE collection_id = ? AND namespace = ?",
		collectionID, namespace,
	).Scan(&lastIngestUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to get last ingest: %w", err)
	}
	if lastIngestUnix.Valid {
		stats.LastIngest = time.Unix(lastIngestUnix.Int64, 0).UTC()
	}

	return stats, nil
}

// SearchChunks performs semantic search across chunks in a namespace.
// Uses sqlite-vec's vec_distance_cosine for brute-force KNN search.
func (b *Backend) SearchChunks(ctx context.Context, namespace string, embedding []float32, opts storage.ChunkSearchOpts) ([]*types.ChunkResult, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	// Encode query embedding as binary for sqlite-vec
	queryEmbedding := encodeVectorBinary(embedding)

	// Build the query with joins for document info
	var args []any
	query := `
		SELECT c.id, c.document_id, c.namespace, c.collection_id, c.content,
		       c.chunk_index, c.token_count, c.metadata,
		       d.title, d.source, d.metadata AS doc_metadata,
		       vec_distance_cosine(e.embedding, ?) AS distance
		FROM chunk_embeddings e
		JOIN chunks c ON c.id = e.chunk_id
		JOIN documents d ON d.id = c.document_id
		WHERE c.namespace = ?
	`
	args = append(args, queryEmbedding, namespace)

	// Optional: filter by collection
	if opts.CollectionID != nil {
		query += " AND c.collection_id = ?"
		args = append(args, *opts.CollectionID)
	}

	// Note: Metadata filters would require JSON extraction which varies by SQLite version
	// For now, we apply them in Go after fetching

	query += " ORDER BY distance ASC LIMIT ?"
	args = append(args, opts.TopK)

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search chunks: %w", err)
	}
	defer rows.Close()

	var results []*types.ChunkResult
	for rows.Next() {
		chunk := &types.Chunk{}
		result := &types.ChunkResult{Chunk: chunk}
		var chunkMetadataJSON, docTitle, docSource, docMetadataJSON sql.NullString
		var tokenCount sql.NullInt64
		var distance float64

		if err := rows.Scan(
			&chunk.ID, &chunk.DocumentID, &chunk.Namespace, &chunk.CollectionID,
			&chunk.Content, &chunk.Index, &tokenCount, &chunkMetadataJSON,
			&docTitle, &docSource, &docMetadataJSON, &distance,
		); err != nil {
			return nil, fmt.Errorf("failed to scan chunk: %w", err)
		}

		if tokenCount.Valid {
			chunk.TokenCount = int(tokenCount.Int64)
		}

		if chunkMetadataJSON.Valid && chunkMetadataJSON.String != "" {
			if err := json.Unmarshal([]byte(chunkMetadataJSON.String), &chunk.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal chunk metadata: %w", err)
			}
		}

		result.DocumentTitle = docTitle.String
		result.Source = docSource.String

		if docMetadataJSON.Valid && docMetadataJSON.String != "" {
			if err := json.Unmarshal([]byte(docMetadataJSON.String), &result.DocMetadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal doc metadata: %w", err)
			}
		}

		// Convert distance to similarity score (cosine distance -> similarity)
		// Cosine distance ranges from 0 (identical) to 2 (opposite)
		// We convert to similarity: 1 - (distance / 2) gives range [0, 1]
		score := 1.0 - (distance / 2.0)

		// Apply minimum score filter
		if opts.MinScore > 0 && score < opts.MinScore {
			continue
		}

		// Apply metadata filters in Go
		if len(opts.Filters) > 0 {
			// If filters are specified but chunk has no metadata, skip it
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

// Helper function to check for unique constraint errors
func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	// SQLite unique constraint error contains "UNIQUE constraint failed"
	return contains(err.Error(), "UNIQUE constraint failed")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
