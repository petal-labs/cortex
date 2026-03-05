package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// HybridSearchMessages performs combined vector and full-text search on messages.
// Uses Reciprocal Rank Fusion (RRF) to combine results.
// Falls back to vector-only search if FTS5 is not available.
func (b *Backend) HybridSearchMessages(ctx context.Context, namespace string, query string, embedding []float32, opts storage.HybridSearchOpts) ([]*types.MessageResult, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}
	if opts.Alpha < 0 || opts.Alpha > 1 {
		opts.Alpha = 0.5 // Default: equal weight to vector and text
	}
	if opts.RRFConstant <= 0 {
		opts.RRFConstant = 60 // Standard RRF constant
	}

	// Fetch more candidates to allow for RRF ranking
	fetchK := opts.TopK * 3

	// Run vector search
	vectorResults, err := b.vectorSearchMessages(ctx, namespace, embedding, fetchK, opts.ThreadID)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	// Check if FTS5 is available
	var ftsResults []*types.MessageResult
	if b.FTS5Available(ctx) {
		// Run FTS search
		ftsResults, err = b.ftsSearchMessages(ctx, namespace, query, fetchK, opts.ThreadID)
		if err != nil {
			// Log but don't fail - fall back to vector-only
			ftsResults = nil
		}
	}

	// Combine using RRF (or return vector results if no FTS)
	var combined []*types.MessageResult
	if len(ftsResults) > 0 {
		combined = reciprocalRankFusion(vectorResults, ftsResults, opts.Alpha, opts.RRFConstant)
	} else {
		// No FTS results - use vector results directly
		combined = vectorResults
	}

	// Limit to TopK
	if len(combined) > opts.TopK {
		combined = combined[:opts.TopK]
	}

	// Filter by min score
	if opts.MinScore > 0 {
		filtered := make([]*types.MessageResult, 0, len(combined))
		for _, r := range combined {
			if r.Score >= opts.MinScore {
				filtered = append(filtered, r)
			}
		}
		combined = filtered
	}

	return combined, nil
}

// vectorSearchMessages performs pure vector similarity search.
func (b *Backend) vectorSearchMessages(ctx context.Context, namespace string, embedding []float32, topK int, threadID *string) ([]*types.MessageResult, error) {
	queryEmbedding := encodeVectorBinary(embedding)

	var args []any
	query := `
		SELECT m.id, m.namespace, m.thread_id, m.role, m.content, m.metadata,
		       m.summarized, m.created_at,
		       vec_distance_cosine(e.embedding, ?) AS distance
		FROM message_embeddings e
		JOIN messages m ON m.id = e.message_id
		WHERE m.namespace = ?
	`
	args = append(args, queryEmbedding, namespace)

	if threadID != nil {
		query += " AND m.thread_id = ?"
		args = append(args, *threadID)
	}

	query += " ORDER BY distance ASC LIMIT ?"
	args = append(args, topK)

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*types.MessageResult
	rank := 1
	for rows.Next() {
		var msg types.Message
		var metadata []byte
		var summarized int
		var createdAt int64
		var distance float64

		err := rows.Scan(&msg.ID, &msg.Namespace, &msg.ThreadID, &msg.Role, &msg.Content,
			&metadata, &summarized, &createdAt, &distance)
		if err != nil {
			return nil, err
		}

		if len(metadata) > 0 {
			json.Unmarshal(metadata, &msg.Metadata)
		}
		msg.Summarized = summarized != 0
		msg.CreatedAt = time.Unix(createdAt, 0)

		// Convert cosine distance to similarity score
		score := 1.0 - distance

		results = append(results, &types.MessageResult{
			Message: &msg,
			Score:   score,
			Rank:    rank,
		})
		rank++
	}

	return results, nil
}

// ftsSearchMessages performs BM25 full-text search.
func (b *Backend) ftsSearchMessages(ctx context.Context, namespace string, query string, topK int, threadID *string) ([]*types.MessageResult, error) {
	// Escape FTS5 special characters and prepare query
	ftsQuery := prepareFTSQuery(query)

	var args []any
	sqlQuery := `
		SELECT m.id, m.namespace, m.thread_id, m.role, m.content, m.metadata,
		       m.summarized, m.created_at,
		       bm25(messages_fts) AS bm25_score
		FROM messages_fts
		JOIN messages m ON messages_fts.id = m.id
		WHERE messages_fts MATCH ? AND messages_fts.namespace = ?
	`
	args = append(args, ftsQuery, namespace)

	if threadID != nil {
		sqlQuery += " AND messages_fts.thread_id = ?"
		args = append(args, *threadID)
	}

	sqlQuery += " ORDER BY bm25_score LIMIT ?"
	args = append(args, topK)

	rows, err := b.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		// If FTS table doesn't exist or query fails, return empty results
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	var results []*types.MessageResult
	rank := 1
	for rows.Next() {
		var msg types.Message
		var metadata []byte
		var summarized int
		var createdAt int64
		var bm25Score float64

		err := rows.Scan(&msg.ID, &msg.Namespace, &msg.ThreadID, &msg.Role, &msg.Content,
			&metadata, &summarized, &createdAt, &bm25Score)
		if err != nil {
			return nil, err
		}

		if len(metadata) > 0 {
			json.Unmarshal(metadata, &msg.Metadata)
		}
		msg.Summarized = summarized != 0
		msg.CreatedAt = time.Unix(createdAt, 0)

		// BM25 returns negative scores (lower is better), normalize to positive
		score := -bm25Score

		results = append(results, &types.MessageResult{
			Message: &msg,
			Score:   score,
			Rank:    rank,
		})
		rank++
	}

	return results, nil
}

// HybridSearchChunks performs combined vector and full-text search on chunks.
// Falls back to vector-only search if FTS5 is not available.
func (b *Backend) HybridSearchChunks(ctx context.Context, namespace string, query string, embedding []float32, opts storage.HybridChunkSearchOpts) ([]*types.ChunkResult, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}
	if opts.Alpha < 0 || opts.Alpha > 1 {
		opts.Alpha = 0.5
	}
	if opts.RRFConstant <= 0 {
		opts.RRFConstant = 60
	}

	fetchK := opts.TopK * 3

	// Run vector search
	vectorResults, err := b.vectorSearchChunks(ctx, namespace, embedding, fetchK, opts.CollectionID, opts.Filters)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	// Check if FTS5 is available
	var ftsResults []*types.ChunkResult
	if b.FTS5Available(ctx) {
		// Run FTS search
		ftsResults, err = b.ftsSearchChunks(ctx, namespace, query, fetchK, opts.CollectionID)
		if err != nil {
			ftsResults = nil
		}
	}

	// Combine using RRF (or return vector results if no FTS)
	var combined []*types.ChunkResult
	if len(ftsResults) > 0 {
		combined = reciprocalRankFusionChunks(vectorResults, ftsResults, opts.Alpha, opts.RRFConstant)
	} else {
		combined = vectorResults
	}

	if len(combined) > opts.TopK {
		combined = combined[:opts.TopK]
	}

	if opts.MinScore > 0 {
		filtered := make([]*types.ChunkResult, 0, len(combined))
		for _, r := range combined {
			if r.Score >= opts.MinScore {
				filtered = append(filtered, r)
			}
		}
		combined = filtered
	}

	return combined, nil
}

// vectorSearchChunks performs pure vector similarity search on chunks.
func (b *Backend) vectorSearchChunks(ctx context.Context, namespace string, embedding []float32, topK int, collectionID *string, filters map[string]string) ([]*types.ChunkResult, error) {
	queryEmbedding := encodeVectorBinary(embedding)

	var args []any
	query := `
		SELECT c.id, c.document_id, c.namespace, c.collection_id, c.content,
		       c.chunk_index, c.token_count, c.metadata,
		       vec_distance_cosine(e.embedding, ?) AS distance
		FROM chunk_embeddings e
		JOIN chunks c ON c.id = e.chunk_id
		WHERE c.namespace = ?
	`
	args = append(args, queryEmbedding, namespace)

	if collectionID != nil {
		query += " AND c.collection_id = ?"
		args = append(args, *collectionID)
	}

	query += " ORDER BY distance ASC LIMIT ?"
	args = append(args, topK)

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*types.ChunkResult
	rank := 1
	for rows.Next() {
		var chunk types.Chunk
		var metadata []byte
		var distance float64

		err := rows.Scan(&chunk.ID, &chunk.DocumentID, &chunk.Namespace, &chunk.CollectionID,
			&chunk.Content, &chunk.Index, &chunk.TokenCount, &metadata, &distance)
		if err != nil {
			return nil, err
		}

		if len(metadata) > 0 {
			json.Unmarshal(metadata, &chunk.Metadata)
		}

		// Apply metadata filters
		if !matchesFilters(chunk.Metadata, filters) {
			continue
		}

		score := 1.0 - distance

		results = append(results, &types.ChunkResult{
			Chunk: &chunk,
			Score: score,
			Rank:  rank,
		})
		rank++
	}

	return results, nil
}

// ftsSearchChunks performs BM25 full-text search on chunks.
func (b *Backend) ftsSearchChunks(ctx context.Context, namespace string, query string, topK int, collectionID *string) ([]*types.ChunkResult, error) {
	ftsQuery := prepareFTSQuery(query)

	var args []any
	sqlQuery := `
		SELECT c.id, c.document_id, c.namespace, c.collection_id, c.content,
		       c.chunk_index, c.token_count, c.metadata,
		       bm25(chunks_fts) AS bm25_score
		FROM chunks_fts
		JOIN chunks c ON chunks_fts.id = c.id
		WHERE chunks_fts MATCH ? AND chunks_fts.namespace = ?
	`
	args = append(args, ftsQuery, namespace)

	if collectionID != nil {
		sqlQuery += " AND chunks_fts.collection_id = ?"
		args = append(args, *collectionID)
	}

	sqlQuery += " ORDER BY bm25_score LIMIT ?"
	args = append(args, topK)

	rows, err := b.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	var results []*types.ChunkResult
	rank := 1
	for rows.Next() {
		var chunk types.Chunk
		var metadata []byte
		var bm25Score float64

		err := rows.Scan(&chunk.ID, &chunk.DocumentID, &chunk.Namespace, &chunk.CollectionID,
			&chunk.Content, &chunk.Index, &chunk.TokenCount, &metadata, &bm25Score)
		if err != nil {
			return nil, err
		}

		if len(metadata) > 0 {
			json.Unmarshal(metadata, &chunk.Metadata)
		}

		score := -bm25Score

		results = append(results, &types.ChunkResult{
			Chunk: &chunk,
			Score: score,
			Rank:  rank,
		})
		rank++
	}

	return results, nil
}

// HybridSearchEntities performs combined vector and full-text search on entities.
// Falls back to vector-only search if FTS5 is not available.
func (b *Backend) HybridSearchEntities(ctx context.Context, namespace string, query string, embedding []float32, opts storage.HybridEntitySearchOpts) ([]*types.EntityResult, error) {
	if opts.TopK <= 0 {
		opts.TopK = 10
	}
	if opts.Alpha < 0 || opts.Alpha > 1 {
		opts.Alpha = 0.5
	}
	if opts.RRFConstant <= 0 {
		opts.RRFConstant = 60
	}

	fetchK := opts.TopK * 3

	// Run vector search
	vectorResults, err := b.vectorSearchEntities(ctx, namespace, embedding, fetchK, opts.EntityType)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	// Check if FTS5 is available
	var ftsResults []*types.EntityResult
	if b.FTS5Available(ctx) {
		// Run FTS search
		ftsResults, err = b.ftsSearchEntities(ctx, namespace, query, fetchK, opts.EntityType)
		if err != nil {
			ftsResults = nil
		}
	}

	// Combine using RRF (or return vector results if no FTS)
	var combined []*types.EntityResult
	if len(ftsResults) > 0 {
		combined = reciprocalRankFusionEntities(vectorResults, ftsResults, opts.Alpha, opts.RRFConstant)
	} else {
		combined = vectorResults
	}

	if len(combined) > opts.TopK {
		combined = combined[:opts.TopK]
	}

	if opts.MinScore > 0 {
		filtered := make([]*types.EntityResult, 0, len(combined))
		for _, r := range combined {
			if r.Score >= opts.MinScore {
				filtered = append(filtered, r)
			}
		}
		combined = filtered
	}

	return combined, nil
}

// vectorSearchEntities performs pure vector similarity search on entities.
func (b *Backend) vectorSearchEntities(ctx context.Context, namespace string, embedding []float32, topK int, entityType *types.EntityType) ([]*types.EntityResult, error) {
	queryEmbedding := encodeVectorBinary(embedding)

	var args []any
	query := `
		SELECT e.id, e.namespace, e.name, e.type, e.aliases, e.summary,
		       e.attributes, e.metadata, e.mention_count, e.first_seen_at, e.last_seen_at,
		       vec_distance_cosine(emb.embedding, ?) AS distance
		FROM entity_embeddings emb
		JOIN entities e ON e.id = emb.entity_id
		WHERE e.namespace = ?
	`
	args = append(args, queryEmbedding, namespace)

	if entityType != nil {
		query += " AND e.type = ?"
		args = append(args, string(*entityType))
	}

	query += " ORDER BY distance ASC LIMIT ?"
	args = append(args, topK)

	rows, err := b.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*types.EntityResult
	rank := 1
	for rows.Next() {
		var entity types.Entity
		var aliases, attributes, metadata []byte
		var firstSeen, lastSeen int64
		var distance float64

		err := rows.Scan(&entity.ID, &entity.Namespace, &entity.Name, &entity.Type,
			&aliases, &entity.Summary, &attributes, &metadata,
			&entity.MentionCount, &firstSeen, &lastSeen, &distance)
		if err != nil {
			return nil, err
		}

		if len(aliases) > 0 {
			json.Unmarshal(aliases, &entity.Aliases)
		}
		if len(attributes) > 0 {
			json.Unmarshal(attributes, &entity.Attributes)
		}
		if len(metadata) > 0 {
			json.Unmarshal(metadata, &entity.Metadata)
		}
		entity.FirstSeenAt = time.Unix(firstSeen, 0)
		entity.LastSeenAt = time.Unix(lastSeen, 0)

		score := 1.0 - distance

		results = append(results, &types.EntityResult{
			Entity: &entity,
			Score:  score,
			Rank:   rank,
		})
		rank++
	}

	return results, nil
}

// ftsSearchEntities performs BM25 full-text search on entities.
func (b *Backend) ftsSearchEntities(ctx context.Context, namespace string, query string, topK int, entityType *types.EntityType) ([]*types.EntityResult, error) {
	ftsQuery := prepareFTSQuery(query)

	var args []any
	sqlQuery := `
		SELECT e.id, e.namespace, e.name, e.type, e.aliases, e.summary,
		       e.attributes, e.metadata, e.mention_count, e.first_seen_at, e.last_seen_at,
		       bm25(entities_fts) AS bm25_score
		FROM entities_fts
		JOIN entities e ON entities_fts.id = e.id
		WHERE entities_fts MATCH ? AND entities_fts.namespace = ?
	`
	args = append(args, ftsQuery, namespace)

	if entityType != nil {
		sqlQuery += " AND e.type = ?"
		args = append(args, string(*entityType))
	}

	sqlQuery += " ORDER BY bm25_score LIMIT ?"
	args = append(args, topK)

	rows, err := b.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	var results []*types.EntityResult
	rank := 1
	for rows.Next() {
		var entity types.Entity
		var aliases, attributes, metadata []byte
		var firstSeen, lastSeen int64
		var bm25Score float64

		err := rows.Scan(&entity.ID, &entity.Namespace, &entity.Name, &entity.Type,
			&aliases, &entity.Summary, &attributes, &metadata,
			&entity.MentionCount, &firstSeen, &lastSeen, &bm25Score)
		if err != nil {
			return nil, err
		}

		if len(aliases) > 0 {
			json.Unmarshal(aliases, &entity.Aliases)
		}
		if len(attributes) > 0 {
			json.Unmarshal(attributes, &entity.Attributes)
		}
		if len(metadata) > 0 {
			json.Unmarshal(metadata, &entity.Metadata)
		}
		entity.FirstSeenAt = time.Unix(firstSeen, 0)
		entity.LastSeenAt = time.Unix(lastSeen, 0)

		score := -bm25Score

		results = append(results, &types.EntityResult{
			Entity: &entity,
			Score:  score,
			Rank:   rank,
		})
		rank++
	}

	return results, nil
}

// reciprocalRankFusion combines two result sets using RRF.
// Alpha controls the weight: 0 = pure FTS, 1 = pure vector.
func reciprocalRankFusion(vectorResults, ftsResults []*types.MessageResult, alpha float64, k float64) []*types.MessageResult {
	scores := make(map[string]float64)
	messages := make(map[string]*types.Message)

	// Score from vector results
	for _, r := range vectorResults {
		scores[r.Message.ID] += alpha * (1.0 / (k + float64(r.Rank)))
		messages[r.Message.ID] = r.Message
	}

	// Score from FTS results
	for _, r := range ftsResults {
		scores[r.Message.ID] += (1 - alpha) * (1.0 / (k + float64(r.Rank)))
		messages[r.Message.ID] = r.Message
	}

	// Build combined results
	var combined []*types.MessageResult
	for id, score := range scores {
		combined = append(combined, &types.MessageResult{
			Message: messages[id],
			Score:   score,
		})
	}

	// Sort by score descending
	sort.Slice(combined, func(i, j int) bool {
		return combined[i].Score > combined[j].Score
	})

	return combined
}

// reciprocalRankFusionChunks combines chunk result sets using RRF.
func reciprocalRankFusionChunks(vectorResults, ftsResults []*types.ChunkResult, alpha float64, k float64) []*types.ChunkResult {
	scores := make(map[string]float64)
	chunks := make(map[string]*types.Chunk)

	for _, r := range vectorResults {
		scores[r.Chunk.ID] += alpha * (1.0 / (k + float64(r.Rank)))
		chunks[r.Chunk.ID] = r.Chunk
	}

	for _, r := range ftsResults {
		scores[r.Chunk.ID] += (1 - alpha) * (1.0 / (k + float64(r.Rank)))
		chunks[r.Chunk.ID] = r.Chunk
	}

	var combined []*types.ChunkResult
	for id, score := range scores {
		combined = append(combined, &types.ChunkResult{
			Chunk: chunks[id],
			Score: score,
		})
	}

	sort.Slice(combined, func(i, j int) bool {
		return combined[i].Score > combined[j].Score
	})

	return combined
}

// reciprocalRankFusionEntities combines entity result sets using RRF.
func reciprocalRankFusionEntities(vectorResults, ftsResults []*types.EntityResult, alpha float64, k float64) []*types.EntityResult {
	scores := make(map[string]float64)
	entities := make(map[string]*types.Entity)

	for _, r := range vectorResults {
		scores[r.Entity.ID] += alpha * (1.0 / (k + float64(r.Rank)))
		entities[r.Entity.ID] = r.Entity
	}

	for _, r := range ftsResults {
		scores[r.Entity.ID] += (1 - alpha) * (1.0 / (k + float64(r.Rank)))
		entities[r.Entity.ID] = r.Entity
	}

	var combined []*types.EntityResult
	for id, score := range scores {
		combined = append(combined, &types.EntityResult{
			Entity: entities[id],
			Score:  score,
		})
	}

	sort.Slice(combined, func(i, j int) bool {
		return combined[i].Score > combined[j].Score
	})

	return combined
}

// prepareFTSQuery escapes special characters and formats the query for FTS5.
func prepareFTSQuery(query string) string {
	// FTS5 uses double quotes for phrase queries
	// Escape existing quotes and special characters
	query = strings.ReplaceAll(query, "\"", "\"\"")

	// Split into words and wrap each in quotes for exact matching
	words := strings.Fields(query)
	if len(words) == 0 {
		return "\"\""
	}

	// Join with OR for broader matching
	var parts []string
	for _, word := range words {
		// Skip very short words
		if len(word) < 2 {
			continue
		}
		parts = append(parts, fmt.Sprintf("\"%s\"", word))
	}

	if len(parts) == 0 {
		return "\"" + query + "\""
	}

	return strings.Join(parts, " OR ")
}

// matchesFilters checks if metadata matches the given filters.
func matchesFilters(metadata map[string]string, filters map[string]string) bool {
	if len(filters) == 0 {
		return true
	}

	for key, value := range filters {
		if metadata == nil {
			return false
		}
		if metaValue, ok := metadata[key]; !ok || metaValue != value {
			return false
		}
	}

	return true
}

// Ensure we have rowid for FTS sync (SQLite uses implicit rowid)
func init() {
	// No-op, but documents that messages/chunks/entities tables use implicit rowid
}
