package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/petal-labs/cortex/v2/pkg/types"
)

// SearchRelationshipsFTS performs Variant B FTS5 BM25 search across
// relationships_fts (source name, relation type, target name, description).
func (b *Backend) SearchRelationshipsFTS(ctx context.Context, namespace, query string, topK int) ([]*types.RelationshipResult, error) {
	if topK <= 0 {
		topK = 15
	}
	if !b.FTS5Available(ctx) {
		return nil, nil
	}
	ftsQuery := prepareRelationshipFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	rows, err := b.reader().QueryContext(ctx, `
		SELECT r.id, r.namespace, r.source_entity_id, r.target_entity_id,
		       r.relation_type, r.description, r.confidence, r.mention_count,
		       r.first_seen_at, r.last_seen_at,
		       bm25(relationships_fts) AS bm25_score
		FROM relationships_fts
		JOIN entity_relationships r ON r.id = relationships_fts.relationship_id
		WHERE relationships_fts MATCH ? AND relationships_fts.namespace = ?
		ORDER BY bm25_score ASC
		LIMIT ?
	`, ftsQuery, namespace, topK)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to fts search relationships: %w", err)
	}
	defer rows.Close()

	var results []*types.RelationshipResult
	rank := 1
	for rows.Next() {
		rel := &types.EntityRelationship{}
		var desc sql.NullString
		var firstSeen, lastSeen int64
		var bm25Score float64

		if err := rows.Scan(
			&rel.ID, &rel.Namespace, &rel.SourceEntityID, &rel.TargetEntityID,
			&rel.RelationType, &desc, &rel.Confidence, &rel.MentionCount,
			&firstSeen, &lastSeen, &bm25Score,
		); err != nil {
			return nil, fmt.Errorf("failed to scan relationship fts result: %w", err)
		}
		rel.Description = desc.String
		rel.FirstSeenAt = time.Unix(firstSeen, 0).UTC()
		rel.LastSeenAt = time.Unix(lastSeen, 0).UTC()

		results = append(results, &types.RelationshipResult{
			Relationship: rel,
			Score:        -bm25Score,
			Rank:         rank,
		})
		rank++
	}
	return results, rows.Err()
}

// FindLexicalEntityMatches extracts lowercased 1..3-word n-grams from query and
// performs a single indexed lookup on entity_aliases(namespace, alias).
func (b *Backend) FindLexicalEntityMatches(ctx context.Context, namespace, query string, limit int) ([]*types.Entity, error) {
	if limit <= 0 {
		limit = 15
	}
	ngrams := extractQueryNGrams(query, 3)
	if len(ngrams) == 0 {
		return nil, nil
	}

	ph := makePlaceholders(len(ngrams))
	args := make([]any, 0, len(ngrams)+2)
	args = append(args, namespace)
	for _, ng := range ngrams {
		args = append(args, ng)
	}
	args = append(args, limit)

	rows, err := b.reader().QueryContext(ctx, `
		SELECT DISTINCT entity_id
		FROM entity_aliases
		WHERE namespace = ? AND alias IN (`+ph+`)
		LIMIT ?
	`, args...)
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

	ph := makePlaceholders(len(entityIDs))
	args := make([]any, 0, len(entityIDs)*2+3)
	args = append(args, namespace)
	for _, id := range entityIDs {
		args = append(args, id)
	}
	args = append(args, namespace)
	for _, id := range entityIDs {
		args = append(args, id)
	}
	args = append(args, maxPerEntity)

	query := `
		WITH incident AS (
			SELECT source_entity_id AS anchor_id,
			       id, namespace, source_entity_id, target_entity_id, relation_type,
			       description, confidence, mention_count, first_seen_at, last_seen_at
			FROM entity_relationships
			WHERE namespace = ? AND source_entity_id IN (` + ph + `)
			UNION ALL
			SELECT target_entity_id AS anchor_id,
			       id, namespace, source_entity_id, target_entity_id, relation_type,
			       description, confidence, mention_count, first_seen_at, last_seen_at
			FROM entity_relationships
			WHERE namespace = ? AND target_entity_id IN (` + ph + `)
		),
		ranked AS (
			SELECT *,
			       ROW_NUMBER() OVER (PARTITION BY anchor_id ORDER BY confidence DESC, mention_count DESC, id ASC) AS rn
			FROM incident
		)
		SELECT DISTINCT id, namespace, source_entity_id, target_entity_id, relation_type,
		       description, confidence, mention_count, first_seen_at, last_seen_at
		FROM ranked
		WHERE rn <= ?
	`

	rows, err := b.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to batch query relationships: %w", err)
	}
	defer rows.Close()

	var rels []*types.EntityRelationship
	for rows.Next() {
		rel := &types.EntityRelationship{}
		var desc sql.NullString
		var firstSeen, lastSeen int64
		if err := rows.Scan(
			&rel.ID, &rel.Namespace, &rel.SourceEntityID, &rel.TargetEntityID,
			&rel.RelationType, &desc, &rel.Confidence, &rel.MentionCount,
			&firstSeen, &lastSeen,
		); err != nil {
			return nil, fmt.Errorf("failed to scan batch relationship: %w", err)
		}
		rel.Description = desc.String
		rel.FirstSeenAt = time.Unix(firstSeen, 0).UTC()
		rel.LastSeenAt = time.Unix(lastSeen, 0).UTC()
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

	ph := makePlaceholders(len(entityIDs))
	args := make([]any, 0, len(entityIDs)+3)
	args = append(args, namespace, sourceType)
	for _, id := range entityIDs {
		args = append(args, id)
	}
	args = append(args, limitPerEntity)

	query := `
		WITH ranked AS (
			SELECT entity_id, source_id,
			       ROW_NUMBER() OVER (PARTITION BY entity_id ORDER BY created_at DESC) AS rn
			FROM entity_mentions
			WHERE namespace = ? AND source_type = ? AND entity_id IN (` + ph + `)
		)
		SELECT entity_id, source_id
		FROM ranked
		WHERE rn <= ?
	`

	rows, err := b.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to batch query mentions by entity: %w", err)
	}
	defer rows.Close()

	return scanMentionLinks(rows, namespace, sourceType)
}

// GetMentionsBySourceIDs fetches entity mention links for the given source IDs
// using the covering index on entity_mentions.
func (b *Backend) GetMentionsBySourceIDs(ctx context.Context, namespace, sourceType string, sourceIDs []string) ([]*types.EntityMention, error) {
	if len(sourceIDs) == 0 {
		return nil, nil
	}
	ph := makePlaceholders(len(sourceIDs))
	args := make([]any, 0, len(sourceIDs)+2)
	args = append(args, namespace, sourceType)
	for _, id := range sourceIDs {
		args = append(args, id)
	}

	rows, err := b.reader().QueryContext(ctx, `
		SELECT entity_id, source_id
		FROM entity_mentions
		WHERE namespace = ? AND source_type = ? AND source_id IN (`+ph+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to batch query mentions by source: %w", err)
	}
	defer rows.Close()

	return scanMentionLinks(rows, namespace, sourceType)
}

// GetChunksByIDs hydrates chunk results with document metadata, respecting
// optional collection and metadata filters.
func (b *Backend) GetChunksByIDs(ctx context.Context, namespace string, chunkIDs []string, collectionID *string, filters map[string]string) ([]*types.ChunkResult, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	ph := makePlaceholders(len(chunkIDs))
	args := make([]any, 0, len(chunkIDs)+2)
	args = append(args, namespace)
	for _, id := range chunkIDs {
		args = append(args, id)
	}

	query := `
		SELECT c.id, c.document_id, c.namespace, c.collection_id, c.content,
		       c.chunk_index, c.token_count, c.metadata,
		       d.title, d.source, d.metadata AS doc_metadata
		FROM chunks c
		JOIN documents d ON d.id = c.document_id
		WHERE c.namespace = ? AND c.id IN (` + ph + `)
	`
	if collectionID != nil {
		query += " AND c.collection_id = ?"
		args = append(args, *collectionID)
	}

	rows, err := b.reader().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to batch query chunks: %w", err)
	}
	defer rows.Close()

	var results []*types.ChunkResult
	for rows.Next() {
		chunk := &types.Chunk{}
		res := &types.ChunkResult{Chunk: chunk}
		var chunkMetaJSON, docTitle, docSource, docMetaJSON sql.NullString
		var tokenCount sql.NullInt64

		if err := rows.Scan(
			&chunk.ID, &chunk.DocumentID, &chunk.Namespace, &chunk.CollectionID,
			&chunk.Content, &chunk.Index, &tokenCount, &chunkMetaJSON,
			&docTitle, &docSource, &docMetaJSON,
		); err != nil {
			return nil, fmt.Errorf("failed to scan batch chunk: %w", err)
		}
		if tokenCount.Valid {
			chunk.TokenCount = int(tokenCount.Int64)
		}
		if chunkMetaJSON.Valid && chunkMetaJSON.String != "" {
			if err := json.Unmarshal([]byte(chunkMetaJSON.String), &chunk.Metadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal chunk metadata: %w", err)
			}
		}
		res.DocumentTitle = docTitle.String
		res.Source = docSource.String
		if docMetaJSON.Valid && docMetaJSON.String != "" {
			if err := json.Unmarshal([]byte(docMetaJSON.String), &res.DocMetadata); err != nil {
				return nil, fmt.Errorf("failed to unmarshal doc metadata: %w", err)
			}
		}
		if !matchesFilters(chunk.Metadata, filters) {
			continue
		}
		results = append(results, res)
	}
	return results, rows.Err()
}

// GetEntitiesByIDs hydrates entity records for a batch of IDs.
func (b *Backend) GetEntitiesByIDs(ctx context.Context, namespace string, entityIDs []string) ([]*types.Entity, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	ph := makePlaceholders(len(entityIDs))
	args := make([]any, 0, len(entityIDs)+1)
	args = append(args, namespace)
	for _, id := range entityIDs {
		args = append(args, id)
	}

	rows, err := b.reader().QueryContext(ctx, `
		SELECT id, namespace, name, type, aliases, summary, attributes, metadata,
		       mention_count, first_seen_at, last_seen_at
		FROM entities
		WHERE namespace = ? AND id IN (`+ph+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to batch query entities: %w", err)
	}
	defer rows.Close()

	return scanEntities(rows)
}

// FindEntityCandidatesByPrefix returns entities whose canonical name or alias
// starts with prefix (case-insensitive), avoiding the 1,000-entity ListEntities
// cap in entity resolution.
func (b *Backend) FindEntityCandidatesByPrefix(ctx context.Context, namespace, prefix string, limit int) ([]*types.Entity, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	// Range scan on entity_aliases(namespace, alias) PRIMARY KEY avoids LIKE wildcards
	// and uses the B-tree index directly even with 25K+ entities.
	upperBound := prefix + "\uffff"
	rows, err := b.reader().QueryContext(ctx, `
		SELECT DISTINCT entity_id
		FROM entity_aliases
		WHERE namespace = ? AND alias >= ? AND alias < ?
		LIMIT ?
	`, namespace, prefix, upperBound, limit)
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

func scanEntities(rows *sql.Rows) ([]*types.Entity, error) {
	var entities []*types.Entity
	for rows.Next() {
		ent := &types.Entity{}
		var aliasesJSON, attrsJSON, metaJSON, summary sql.NullString
		var firstSeen, lastSeen int64
		var entType string

		if err := rows.Scan(
			&ent.ID, &ent.Namespace, &ent.Name, &entType,
			&aliasesJSON, &summary, &attrsJSON, &metaJSON,
			&ent.MentionCount, &firstSeen, &lastSeen,
		); err != nil {
			return nil, fmt.Errorf("failed to scan entity: %w", err)
		}
		ent.Type = types.EntityType(entType)
		ent.Summary = summary.String
		ent.FirstSeenAt = time.Unix(firstSeen, 0).UTC()
		ent.LastSeenAt = time.Unix(lastSeen, 0).UTC()
		if aliasesJSON.Valid && aliasesJSON.String != "" {
			_ = json.Unmarshal([]byte(aliasesJSON.String), &ent.Aliases)
		}
		if attrsJSON.Valid && attrsJSON.String != "" {
			_ = json.Unmarshal([]byte(attrsJSON.String), &ent.Attributes)
		}
		if metaJSON.Valid && metaJSON.String != "" {
			_ = json.Unmarshal([]byte(metaJSON.String), &ent.Metadata)
		}
		entities = append(entities, ent)
	}
	return entities, rows.Err()
}

func scanMentionLinks(rows *sql.Rows, namespace, sourceType string) ([]*types.EntityMention, error) {
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

func makePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	var sb strings.Builder
	sb.Grow(n*2 - 1)
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte('?')
	}
	return sb.String()
}

func extractQueryNGrams(query string, maxN int) []string {
	tokens := tokenizeGraphQuery(query)
	if len(tokens) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(tokens)*2)
	var out []string
	for n := 1; n <= maxN; n++ {
		for i := 0; i+n <= len(tokens); i++ {
			if n == 1 && (len(tokens[i]) < 2 || graphStopWords[tokens[i]]) {
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

func tokenizeGraphQuery(s string) []string {
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

func prepareRelationshipFTSQuery(query string) string {
	tokens := tokenizeGraphQuery(query)
	var parts []string
	seen := make(map[string]bool, len(tokens))
	for _, tok := range tokens {
		if len(tok) < 2 || graphStopWords[tok] || seen[tok] {
			continue
		}
		seen[tok] = true
		escaped := strings.ReplaceAll(tok, `"`, `""`)
		parts = append(parts, `"`+escaped+`"`)
	}
	return strings.Join(parts, " OR ")
}

var graphStopWords = map[string]bool{
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
