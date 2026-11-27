package pgvector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/petal-labs/cortex/pkg/types"
)

// SearchRelationshipsFTS performs full-text search across relational triples
// (source entity name, relation type, target entity name, and description)
// using PostgreSQL ts_rank and GIN-indexed tsvectors.
func (b *Backend) SearchRelationshipsFTS(ctx context.Context, namespace, query string, topK int) ([]*types.RelationshipResult, error) {
	if topK <= 0 {
		topK = 15
	}
	tsQuery := preparePGRelationshipTSQuery(query)
	if tsQuery == "" {
		return nil, nil
	}

	rows, err := b.pool.Query(ctx, `
		WITH q AS (SELECT to_tsquery('english', $1) AS tsq)
		SELECT r.id, r.namespace, r.source_entity_id, r.target_entity_id,
		       r.relation_type, r.description, r.confidence, r.mention_count,
		       r.created_at, r.updated_at,
		       ts_rank(
		           r.search_tsvector ||
		           to_tsvector('english', COALESCE(s.name, '') || ' ' || COALESCE(t.name, '')),
		           q.tsq
		       ) AS rank_score
		FROM entity_relationships r
		JOIN entities s ON s.id = r.source_entity_id
		JOIN entities t ON t.id = r.target_entity_id
		CROSS JOIN q
		WHERE r.namespace = $2
		  AND (
		      r.search_tsvector @@ q.tsq
		      OR s.search_tsvector @@ q.tsq
		      OR t.search_tsvector @@ q.tsq
		  )
		ORDER BY rank_score DESC, r.confidence DESC, r.mention_count DESC
		LIMIT $3
	`, tsQuery, namespace, topK)
	if err != nil {
		return nil, fmt.Errorf("failed to fts search relationships: %w", err)
	}
	defer rows.Close()

	var results []*types.RelationshipResult
	rank := 1
	for rows.Next() {
		rel := &types.EntityRelationship{}
		var desc *string
		var score float64
		if err := rows.Scan(
			&rel.ID, &rel.Namespace, &rel.SourceEntityID, &rel.TargetEntityID,
			&rel.RelationType, &desc, &rel.Confidence, &rel.MentionCount,
			&rel.FirstSeenAt, &rel.LastSeenAt, &score,
		); err != nil {
			return nil, fmt.Errorf("failed to scan relationship fts result: %w", err)
		}
		if desc != nil {
			rel.Description = *desc
		}
		results = append(results, &types.RelationshipResult{
			Relationship: rel,
			Score:        score,
			Rank:         rank,
		})
		rank++
	}
	return results, rows.Err()
}

// FindLexicalEntityMatches matches lowercased 1..3-word n-grams from query
// against entity_aliases in a single indexed lookup.
func (b *Backend) FindLexicalEntityMatches(ctx context.Context, namespace, query string, limit int) ([]*types.Entity, error) {
	if limit <= 0 {
		limit = 15
	}
	ngrams := extractPGQueryNGrams(query, 3)
	if len(ngrams) == 0 {
		return nil, nil
	}

	rows, err := b.pool.Query(ctx, `
		SELECT DISTINCT entity_id
		FROM entity_aliases
		WHERE namespace = $1 AND LOWER(alias) = ANY($2)
		LIMIT $3
	`, namespace, ngrams, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup lexical entity matches: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return b.GetEntitiesByIDs(ctx, namespace, ids)
}

// GetRelationshipsBatch fetches up to maxPerEntity incident relationships for
// each entity in entityIDs using a single SQL query with a window rank cap.
func (b *Backend) GetRelationshipsBatch(ctx context.Context, namespace string, entityIDs []string, maxPerEntity int) ([]*types.EntityRelationship, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	if maxPerEntity <= 0 {
		maxPerEntity = 15
	}

	rows, err := b.pool.Query(ctx, `
		WITH incident AS (
			SELECT source_entity_id AS anchor_id,
			       id, namespace, source_entity_id, target_entity_id, relation_type,
			       description, confidence, mention_count, created_at, updated_at
			FROM entity_relationships
			WHERE namespace = $1 AND source_entity_id = ANY($2)
			UNION ALL
			SELECT target_entity_id AS anchor_id,
			       id, namespace, source_entity_id, target_entity_id, relation_type,
			       description, confidence, mention_count, created_at, updated_at
			FROM entity_relationships
			WHERE namespace = $1 AND target_entity_id = ANY($2)
		),
		ranked AS (
			SELECT *,
			       ROW_NUMBER() OVER (PARTITION BY anchor_id ORDER BY confidence DESC, mention_count DESC, id ASC) AS rn
			FROM incident
		)
		SELECT DISTINCT id, namespace, source_entity_id, target_entity_id, relation_type,
		       description, confidence, mention_count, created_at, updated_at
		FROM ranked
		WHERE rn <= $3
	`, namespace, entityIDs, maxPerEntity)
	if err != nil {
		return nil, fmt.Errorf("failed to batch query relationships: %w", err)
	}
	defer rows.Close()

	var rels []*types.EntityRelationship
	for rows.Next() {
		rel := &types.EntityRelationship{}
		var desc *string
		if err := rows.Scan(
			&rel.ID, &rel.Namespace, &rel.SourceEntityID, &rel.TargetEntityID,
			&rel.RelationType, &desc, &rel.Confidence, &rel.MentionCount,
			&rel.FirstSeenAt, &rel.LastSeenAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan batch relationship: %w", err)
		}
		if desc != nil {
			rel.Description = *desc
		}
		rels = append(rels, rel)
	}
	return rels, rows.Err()
}

// GetMentionsByEntityIDs fetches up to limitPerEntity mention links for each
// entity using the covering index on entity_mentions.
func (b *Backend) GetMentionsByEntityIDs(ctx context.Context, namespace string, entityIDs []string, sourceType string, limitPerEntity int) ([]*types.EntityMention, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	if limitPerEntity <= 0 {
		limitPerEntity = 15
	}

	rows, err := b.pool.Query(ctx, `
		WITH ranked AS (
			SELECT entity_id, source_id,
			       ROW_NUMBER() OVER (PARTITION BY entity_id ORDER BY created_at DESC) AS rn
			FROM entity_mentions
			WHERE namespace = $1 AND source_type = $2 AND entity_id = ANY($3)
		)
		SELECT entity_id, source_id
		FROM ranked
		WHERE rn <= $4
	`, namespace, sourceType, entityIDs, limitPerEntity)
	if err != nil {
		return nil, fmt.Errorf("failed to batch query mentions by entity: %w", err)
	}
	defer rows.Close()

	return scanPGMentionLinks(rows, namespace, sourceType)
}

// GetMentionsBySourceIDs fetches mention links for the given source IDs.
func (b *Backend) GetMentionsBySourceIDs(ctx context.Context, namespace, sourceType string, sourceIDs []string) ([]*types.EntityMention, error) {
	if len(sourceIDs) == 0 {
		return nil, nil
	}

	rows, err := b.pool.Query(ctx, `
		SELECT entity_id, source_id
		FROM entity_mentions
		WHERE namespace = $1 AND source_type = $2 AND source_id = ANY($3)
	`, namespace, sourceType, sourceIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to batch query mentions by source: %w", err)
	}
	defer rows.Close()

	return scanPGMentionLinks(rows, namespace, sourceType)
}

// GetChunksByIDs hydrates chunk results with document metadata, honoring
// optional collection and metadata filters.
func (b *Backend) GetChunksByIDs(ctx context.Context, namespace string, chunkIDs []string, collectionID *string, filters map[string]string) ([]*types.ChunkResult, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}

	args := []any{namespace, chunkIDs}
	query := `
		SELECT c.id, c.document_id, c.namespace, c.collection_id, c.content,
		       c.sequence_num, c.token_count, c.metadata,
		       d.title, d.source, d.metadata AS doc_metadata
		FROM chunks c
		JOIN documents d ON d.id = c.document_id
		WHERE c.namespace = $1 AND c.id = ANY($2)
	`
	if collectionID != nil {
		query += " AND c.collection_id = $3"
		args = append(args, *collectionID)
	}

	rows, err := b.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to batch query chunks: %w", err)
	}
	defer rows.Close()

	var results []*types.ChunkResult
	for rows.Next() {
		chunk := &types.Chunk{}
		res := &types.ChunkResult{Chunk: chunk}
		var chunkMetaJSON, docMetaJSON []byte
		var docTitle, docSource *string

		if err := rows.Scan(
			&chunk.ID, &chunk.DocumentID, &chunk.Namespace, &chunk.CollectionID,
			&chunk.Content, &chunk.Index, &chunk.TokenCount, &chunkMetaJSON,
			&docTitle, &docSource, &docMetaJSON,
		); err != nil {
			return nil, fmt.Errorf("failed to scan batch chunk: %w", err)
		}
		if len(chunkMetaJSON) > 0 {
			if err := json.Unmarshal(chunkMetaJSON, &chunk.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal chunk metadata: %w", err)
			}
		}
		if docTitle != nil {
			res.DocumentTitle = *docTitle
		}
		if docSource != nil {
			res.Source = *docSource
		}
		if len(docMetaJSON) > 0 {
			if err := json.Unmarshal(docMetaJSON, &res.DocMetadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal doc metadata: %w", err)
			}
		}
		if !matchesPGMetadataFilters(chunk.Metadata, filters) {
			continue
		}
		results = append(results, res)
	}
	return results, rows.Err()
}

// GetEntitiesByIDs hydrates entity records for a batch of entity IDs.
func (b *Backend) GetEntitiesByIDs(ctx context.Context, namespace string, entityIDs []string) ([]*types.Entity, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}

	rows, err := b.pool.Query(ctx, `
		SELECT id, namespace, name, type, aliases, summary, attributes, metadata,
		       mention_count, first_seen_at, last_seen_at
		FROM entities
		WHERE namespace = $1 AND id = ANY($2)
	`, namespace, entityIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to batch query entities: %w", err)
	}
	defer rows.Close()

	return scanPGEntities(rows)
}

// FindEntityCandidatesByPrefix returns entities whose canonical name or alias
// starts with prefix (case-insensitive).
func (b *Backend) FindEntityCandidatesByPrefix(ctx context.Context, namespace, prefix string, limit int) ([]*types.Entity, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
	rows, err := b.pool.Query(ctx, `
		SELECT DISTINCT entity_id
		FROM entity_aliases
		WHERE namespace = $1 AND LOWER(alias) LIKE $2 ESCAPE '\'
		LIMIT $3
	`, namespace, escaped+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query entity candidates by prefix: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return b.GetEntitiesByIDs(ctx, namespace, ids)
}

func scanPGEntities(rows pgx.Rows) ([]*types.Entity, error) {
	var entities []*types.Entity
	for rows.Next() {
		ent := &types.Entity{}
		var summary *string
		var attrsJSON, metaJSON []byte
		var entType string

		if err := rows.Scan(
			&ent.ID, &ent.Namespace, &ent.Name, &entType,
			&ent.Aliases, &summary, &attrsJSON, &metaJSON,
			&ent.MentionCount, &ent.FirstSeenAt, &ent.LastSeenAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan entity: %w", err)
		}
		ent.Type = types.EntityType(entType)
		if summary != nil {
			ent.Summary = *summary
		}
		if len(attrsJSON) > 0 {
			_ = json.Unmarshal(attrsJSON, &ent.Attributes)
		}
		if len(metaJSON) > 0 {
			_ = json.Unmarshal(metaJSON, &ent.Metadata)
		}
		entities = append(entities, ent)
	}
	return entities, rows.Err()
}

func scanPGMentionLinks(rows pgx.Rows, namespace, sourceType string) ([]*types.EntityMention, error) {
	var mentions []*types.EntityMention
	for rows.Next() {
		m := &types.EntityMention{
			Namespace:  namespace,
			SourceType: sourceType,
		}
		if err := rows.Scan(&m.EntityID, &m.SourceID); err != nil {
			return nil, fmt.Errorf("failed to scan mention link: %w", err)
		}
		mentions = append(mentions, m)
	}
	return mentions, rows.Err()
}

func matchesPGMetadataFilters(meta, filters map[string]string) bool {
	if len(filters) == 0 {
		return true
	}
	if meta == nil {
		return false
	}
	for k, v := range filters {
		if meta[k] != v {
			return false
		}
	}
	return true
}

func extractPGQueryNGrams(query string, maxN int) []string {
	tokens := tokenizePGQuery(query)
	if len(tokens) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(tokens)*2)
	var out []string
	for n := 1; n <= maxN; n++ {
		for i := 0; i+n <= len(tokens); i++ {
			if n == 1 && (len(tokens[i]) < 2 || pgGraphStopWords[tokens[i]]) {
				continue
			}
			gram := strings.Join(tokens[i:i+n], " ")
			if !seen[gram] {
				seen[gram] = true
				out = append(out, gram)
			}
		}
	}
	return out
}

func tokenizePGQuery(s string) []string {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

func preparePGRelationshipTSQuery(query string) string {
	tokens := tokenizePGQuery(query)
	var parts []string
	seen := make(map[string]bool, len(tokens))
	for _, tok := range tokens {
		// Strip characters that are tsquery syntax operators.
		clean := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return -1
		}, tok)
		if len(clean) < 2 || pgGraphStopWords[clean] || seen[clean] {
			continue
		}
		seen[clean] = true
		parts = append(parts, clean)
	}
	return strings.Join(parts, " | ")
}

var pgGraphStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "in": true, "on": true,
	"at": true, "to": true, "for": true, "of": true, "with": true, "by": true,
	"from": true, "as": true, "and": true, "or": true, "not": true, "that": true,
	"this": true, "these": true, "those": true, "it": true, "its": true,
	"who": true, "what": true, "which": true, "where": true, "when": true,
	"why": true, "how": true, "does": true, "do": true, "did": true, "has": true,
	"have": true, "had": true, "can": true, "could": true, "would": true,
	"should": true, "will": true, "about": true, "into": true, "between": true,
}
