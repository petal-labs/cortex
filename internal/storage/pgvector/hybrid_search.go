package pgvector

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// HybridSearchMessages performs combined vector and full-text search on messages.
// Uses Reciprocal Rank Fusion (RRF) to combine results.
func (b *Backend) HybridSearchMessages(ctx context.Context, namespace string, query string, embedding []float32, opts storage.HybridSearchOpts) ([]*types.MessageResult, error) {
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
	vectorResults, err := b.vectorSearchMessages(ctx, namespace, embedding, fetchK, opts.ThreadID)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	// Run FTS search
	ftsResults, err := b.ftsSearchMessages(ctx, namespace, query, fetchK, opts.ThreadID)
	if err != nil {
		return nil, fmt.Errorf("FTS search failed: %w", err)
	}

	// Combine using RRF
	combined := reciprocalRankFusion(vectorResults, ftsResults, opts.Alpha, opts.RRFConstant)

	if len(combined) > opts.TopK {
		combined = combined[:opts.TopK]
	}

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
	var args []any
	argNum := 1

	query := fmt.Sprintf(`
		SELECT m.id, m.namespace, m.thread_id, m.role, m.content, m.metadata,
		       m.summarized, m.created_at,
		       1 - (e.embedding <=> $%d::vector) AS score
		FROM message_embeddings e
		JOIN messages m ON m.id = e.message_id
		WHERE m.namespace = $%d
	`, argNum, argNum+1)
	args = append(args, toVector(embedding), namespace)
	argNum += 2

	if threadID != nil {
		query += fmt.Sprintf(" AND m.thread_id = $%d", argNum)
		args = append(args, *threadID)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY e.embedding <=> $1::vector LIMIT $%d", argNum)
	args = append(args, topK)

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*types.MessageResult
	rank := 1
	for rows.Next() {
		var msg types.Message
		var score float64

		err := rows.Scan(&msg.ID, &msg.Namespace, &msg.ThreadID, &msg.Role, &msg.Content,
			&msg.Metadata, &msg.Summarized, &msg.CreatedAt, &score)
		if err != nil {
			return nil, err
		}

		results = append(results, &types.MessageResult{
			Message:  &msg,
			Score:    score,
			Rank:     rank,
			ThreadID: msg.ThreadID,
		})
		rank++
	}

	return results, nil
}

// ftsSearchMessages performs BM25-like full-text search using ts_rank_cd.
func (b *Backend) ftsSearchMessages(ctx context.Context, namespace string, query string, topK int, threadID *string) ([]*types.MessageResult, error) {
	tsQuery := prepareTSQuery(query)
	if tsQuery == "" {
		return nil, nil
	}

	var args []any
	argNum := 1

	sqlQuery := fmt.Sprintf(`
		SELECT m.id, m.namespace, m.thread_id, m.role, m.content, m.metadata,
		       m.summarized, m.created_at,
		       ts_rank_cd(m.content_tsvector, plainto_tsquery('english', $%d)) AS score
		FROM messages m
		WHERE m.namespace = $%d
		  AND m.content_tsvector @@ plainto_tsquery('english', $%d)
	`, argNum, argNum+1, argNum)
	args = append(args, tsQuery, namespace)
	argNum += 2

	if threadID != nil {
		sqlQuery += fmt.Sprintf(" AND m.thread_id = $%d", argNum)
		args = append(args, *threadID)
		argNum++
	}

	sqlQuery += fmt.Sprintf(" ORDER BY score DESC LIMIT $%d", argNum)
	args = append(args, topK)

	rows, err := b.pool.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*types.MessageResult
	rank := 1
	for rows.Next() {
		var msg types.Message
		var score float64

		err := rows.Scan(&msg.ID, &msg.Namespace, &msg.ThreadID, &msg.Role, &msg.Content,
			&msg.Metadata, &msg.Summarized, &msg.CreatedAt, &score)
		if err != nil {
			return nil, err
		}

		results = append(results, &types.MessageResult{
			Message:  &msg,
			Score:    score,
			Rank:     rank,
			ThreadID: msg.ThreadID,
		})
		rank++
	}

	return results, nil
}

// HybridSearchChunks performs combined vector and full-text search on chunks.
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

	vectorResults, err := b.vectorSearchChunks(ctx, namespace, embedding, fetchK, opts.CollectionID, opts.Filters)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	ftsResults, err := b.ftsSearchChunks(ctx, namespace, query, fetchK, opts.CollectionID)
	if err != nil {
		return nil, fmt.Errorf("FTS search failed: %w", err)
	}

	combined := reciprocalRankFusionChunks(vectorResults, ftsResults, opts.Alpha, opts.RRFConstant)

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
	var args []any
	argNum := 1

	query := fmt.Sprintf(`
		SELECT c.id, c.document_id, c.namespace, c.collection_id, c.content,
		       c.sequence_num, c.token_count, c.metadata,
		       1 - (c.embedding <=> $%d::vector) AS score
		FROM chunks c
		WHERE c.namespace = $%d
	`, argNum, argNum+1)
	args = append(args, toVector(embedding), namespace)
	argNum += 2

	if collectionID != nil {
		query += fmt.Sprintf(" AND c.collection_id = $%d", argNum)
		args = append(args, *collectionID)
		argNum++
	}

	// Apply metadata filters using JSONB operators
	for key, value := range filters {
		query += fmt.Sprintf(" AND c.metadata->>$%d = $%d", argNum, argNum+1)
		args = append(args, key, value)
		argNum += 2
	}

	query += fmt.Sprintf(" ORDER BY c.embedding <=> $1::vector LIMIT $%d", argNum)
	args = append(args, topK)

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*types.ChunkResult
	rank := 1
	for rows.Next() {
		var chunk types.Chunk
		var score float64

		err := rows.Scan(&chunk.ID, &chunk.DocumentID, &chunk.Namespace, &chunk.CollectionID,
			&chunk.Content, &chunk.Index, &chunk.TokenCount, &chunk.Metadata, &score)
		if err != nil {
			return nil, err
		}

		results = append(results, &types.ChunkResult{
			Chunk: &chunk,
			Score: score,
			Rank:  rank,
		})
		rank++
	}

	return results, nil
}

// ftsSearchChunks performs full-text search on chunks.
func (b *Backend) ftsSearchChunks(ctx context.Context, namespace string, query string, topK int, collectionID *string) ([]*types.ChunkResult, error) {
	tsQuery := prepareTSQuery(query)
	if tsQuery == "" {
		return nil, nil
	}

	var args []any
	argNum := 1

	sqlQuery := fmt.Sprintf(`
		SELECT c.id, c.document_id, c.namespace, c.collection_id, c.content,
		       c.sequence_num, c.token_count, c.metadata,
		       ts_rank_cd(c.content_tsvector, plainto_tsquery('english', $%d)) AS score
		FROM chunks c
		WHERE c.namespace = $%d
		  AND c.content_tsvector @@ plainto_tsquery('english', $%d)
	`, argNum, argNum+1, argNum)
	args = append(args, tsQuery, namespace)
	argNum += 2

	if collectionID != nil {
		sqlQuery += fmt.Sprintf(" AND c.collection_id = $%d", argNum)
		args = append(args, *collectionID)
		argNum++
	}

	sqlQuery += fmt.Sprintf(" ORDER BY score DESC LIMIT $%d", argNum)
	args = append(args, topK)

	rows, err := b.pool.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*types.ChunkResult
	rank := 1
	for rows.Next() {
		var chunk types.Chunk
		var score float64

		err := rows.Scan(&chunk.ID, &chunk.DocumentID, &chunk.Namespace, &chunk.CollectionID,
			&chunk.Content, &chunk.Index, &chunk.TokenCount, &chunk.Metadata, &score)
		if err != nil {
			return nil, err
		}

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

	vectorResults, err := b.vectorSearchEntitiesHybrid(ctx, namespace, embedding, fetchK, opts.EntityType)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	ftsResults, err := b.ftsSearchEntities(ctx, namespace, query, fetchK, opts.EntityType)
	if err != nil {
		return nil, fmt.Errorf("FTS search failed: %w", err)
	}

	combined := reciprocalRankFusionEntities(vectorResults, ftsResults, opts.Alpha, opts.RRFConstant)

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

// vectorSearchEntitiesHybrid performs pure vector similarity search on entities.
func (b *Backend) vectorSearchEntitiesHybrid(ctx context.Context, namespace string, embedding []float32, topK int, entityType *types.EntityType) ([]*types.EntityResult, error) {
	var args []any
	argNum := 1

	query := fmt.Sprintf(`
		SELECT e.id, e.namespace, e.name, e.type, e.aliases, e.summary,
		       e.attributes, e.metadata, e.mention_count, e.first_seen_at, e.last_seen_at,
		       1 - (emb.embedding <=> $%d::vector) AS score
		FROM entity_embeddings emb
		JOIN entities e ON e.id = emb.entity_id
		WHERE e.namespace = $%d
	`, argNum, argNum+1)
	args = append(args, toVector(embedding), namespace)
	argNum += 2

	if entityType != nil {
		query += fmt.Sprintf(" AND e.type = $%d", argNum)
		args = append(args, string(*entityType))
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY emb.embedding <=> $1::vector LIMIT $%d", argNum)
	args = append(args, topK)

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*types.EntityResult
	rank := 1
	for rows.Next() {
		var entity types.Entity
		var score float64

		err := rows.Scan(&entity.ID, &entity.Namespace, &entity.Name, &entity.Type,
			&entity.Aliases, &entity.Summary, &entity.Attributes, &entity.Metadata,
			&entity.MentionCount, &entity.FirstSeenAt, &entity.LastSeenAt, &score)
		if err != nil {
			return nil, err
		}

		results = append(results, &types.EntityResult{
			Entity: &entity,
			Score:  score,
			Rank:   rank,
		})
		rank++
	}

	return results, nil
}

// ftsSearchEntities performs full-text search on entities.
func (b *Backend) ftsSearchEntities(ctx context.Context, namespace string, query string, topK int, entityType *types.EntityType) ([]*types.EntityResult, error) {
	tsQuery := prepareTSQuery(query)
	if tsQuery == "" {
		return nil, nil
	}

	var args []any
	argNum := 1

	sqlQuery := fmt.Sprintf(`
		SELECT e.id, e.namespace, e.name, e.type, e.aliases, e.summary,
		       e.attributes, e.metadata, e.mention_count, e.first_seen_at, e.last_seen_at,
		       ts_rank_cd(e.search_tsvector, plainto_tsquery('english', $%d)) AS score
		FROM entities e
		WHERE e.namespace = $%d
		  AND e.search_tsvector @@ plainto_tsquery('english', $%d)
	`, argNum, argNum+1, argNum)
	args = append(args, tsQuery, namespace)
	argNum += 2

	if entityType != nil {
		sqlQuery += fmt.Sprintf(" AND e.type = $%d", argNum)
		args = append(args, string(*entityType))
		argNum++
	}

	sqlQuery += fmt.Sprintf(" ORDER BY score DESC LIMIT $%d", argNum)
	args = append(args, topK)

	rows, err := b.pool.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*types.EntityResult
	rank := 1
	for rows.Next() {
		var entity types.Entity
		var score float64

		err := rows.Scan(&entity.ID, &entity.Namespace, &entity.Name, &entity.Type,
			&entity.Aliases, &entity.Summary, &entity.Attributes, &entity.Metadata,
			&entity.MentionCount, &entity.FirstSeenAt, &entity.LastSeenAt, &score)
		if err != nil {
			return nil, err
		}

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
func reciprocalRankFusion(vectorResults, ftsResults []*types.MessageResult, alpha float64, k float64) []*types.MessageResult {
	scores := make(map[string]float64)
	messages := make(map[string]*types.Message)
	threadIDs := make(map[string]string)

	for _, r := range vectorResults {
		scores[r.Message.ID] += alpha * (1.0 / (k + float64(r.Rank)))
		messages[r.Message.ID] = r.Message
		threadIDs[r.Message.ID] = r.ThreadID
	}

	for _, r := range ftsResults {
		scores[r.Message.ID] += (1 - alpha) * (1.0 / (k + float64(r.Rank)))
		messages[r.Message.ID] = r.Message
		threadIDs[r.Message.ID] = r.ThreadID
	}

	var combined []*types.MessageResult
	for id, score := range scores {
		combined = append(combined, &types.MessageResult{
			Message:  messages[id],
			Score:    score,
			ThreadID: threadIDs[id],
		})
	}

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

// prepareTSQuery sanitizes the input query for PostgreSQL full-text search.
func prepareTSQuery(query string) string {
	// Trim whitespace
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}

	// plainto_tsquery handles basic sanitization
	// We just ensure the query isn't empty
	return query
}
