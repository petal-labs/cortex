package entity

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// Common errors returned by the entity engine.
var (
	ErrEntityNotFound     = errors.New("entity not found")
	ErrEmptyName          = errors.New("entity name cannot be empty")
	ErrInvalidType        = errors.New("invalid entity type")
	ErrSelfMerge          = errors.New("cannot merge entity with itself")
	ErrEmbeddingRequired  = errors.New("embedding provider required for search")
	ErrEmptyQuery         = errors.New("search query cannot be empty")
	ErrEmptySourceID      = errors.New("source ID is required")
)

// ValidEntityTypes defines valid entity types.
var ValidEntityTypes = map[types.EntityType]bool{
	types.EntityTypePerson:       true,
	types.EntityTypeOrganization: true,
	types.EntityTypeProduct:      true,
	types.EntityTypeLocation:     true,
	types.EntityTypeConcept:      true,
}

// Engine implements the entity memory logic layer.
// It orchestrates storage, embedding, and extraction operations.
type Engine struct {
	storage   storage.Backend
	embedding embedding.Provider
	cfg       *config.EntityConfig
}

// NewEngine creates a new entity engine.
func NewEngine(store storage.Backend, emb embedding.Provider, cfg *config.EntityConfig) (*Engine, error) {
	if store == nil {
		return nil, errors.New("storage backend is required")
	}

	if cfg == nil {
		defaultCfg := config.DefaultConfig()
		cfg = &defaultCfg.Entity
	}

	return &Engine{
		storage:   store,
		embedding: emb,
		cfg:       cfg,
	}, nil
}

// CreateOpts contains options for creating an entity.
type CreateOpts struct {
	Aliases    []string          // Alternative names
	Summary    string            // Initial summary
	Attributes map[string]string // Structured facts
	Metadata   map[string]string // Additional metadata
}

// CreateResult contains the result of entity creation.
type CreateResult struct {
	Entity  *types.Entity `json:"entity"`
	Created bool          `json:"created"` // false if entity already existed
}

// Create creates a new entity or returns existing one with same name.
func (e *Engine) Create(ctx context.Context, namespace, name string, entityType types.EntityType, opts *CreateOpts) (*CreateResult, error) {
	if name == "" {
		return nil, ErrEmptyName
	}

	name = strings.TrimSpace(name)
	if !ValidEntityTypes[entityType] {
		return nil, fmt.Errorf("%w: %s", ErrInvalidType, entityType)
	}

	if opts == nil {
		opts = &CreateOpts{}
	}

	// Check if entity already exists
	existing, err := e.storage.GetEntityByName(ctx, namespace, name)
	if err == nil {
		return &CreateResult{Entity: existing, Created: false}, nil
	}
	if !errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("failed to check existing entity: %w", err)
	}

	now := time.Now().UTC()
	entity := &types.Entity{
		ID:           uuid.New().String(),
		Namespace:    namespace,
		Name:         name,
		Type:         entityType,
		Aliases:      opts.Aliases,
		Summary:      opts.Summary,
		Attributes:   opts.Attributes,
		Metadata:     opts.Metadata,
		MentionCount: 0,
		FirstSeenAt:  now,
		LastSeenAt:   now,
	}

	if err := e.storage.UpsertEntity(ctx, entity); err != nil {
		return nil, fmt.Errorf("failed to create entity: %w", err)
	}

	// Register aliases
	for _, alias := range opts.Aliases {
		alias = strings.TrimSpace(alias)
		if alias != "" && !strings.EqualFold(alias, name) {
			if err := e.storage.RegisterAlias(ctx, namespace, alias, entity.ID); err != nil {
				// Log but don't fail - alias registration is secondary
			}
		}
	}

	// Generate and store embedding for summary if provided
	if opts.Summary != "" && e.embedding != nil {
		emb, err := e.embedding.Embed(ctx, opts.Summary)
		if err == nil {
			e.storage.StoreEntityEmbedding(ctx, entity.ID, emb)
		}
	}

	return &CreateResult{Entity: entity, Created: true}, nil
}

// Get retrieves an entity by ID.
func (e *Engine) Get(ctx context.Context, namespace, entityID string) (*types.Entity, error) {
	entity, err := e.storage.GetEntityByID(ctx, namespace, entityID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to get entity: %w", err)
	}
	return entity, nil
}

// GetByName retrieves an entity by its canonical name.
func (e *Engine) GetByName(ctx context.Context, namespace, name string) (*types.Entity, error) {
	if name == "" {
		return nil, ErrEmptyName
	}

	entity, err := e.storage.GetEntityByName(ctx, namespace, name)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to get entity by name: %w", err)
	}
	return entity, nil
}

// Resolve finds an entity by name or alias.
func (e *Engine) Resolve(ctx context.Context, namespace, nameOrAlias string) (*types.Entity, error) {
	if nameOrAlias == "" {
		return nil, ErrEmptyName
	}

	// Try canonical name first
	entity, err := e.storage.GetEntityByName(ctx, namespace, nameOrAlias)
	if err == nil {
		return entity, nil
	}

	// Try alias resolution
	entity, err = e.storage.ResolveAlias(ctx, namespace, nameOrAlias)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to resolve entity: %w", err)
	}

	return entity, nil
}

// UpdateOpts contains options for updating an entity.
type UpdateOpts struct {
	Summary    *string           // New summary (nil = don't update)
	Attributes map[string]string // Merge with existing attributes
	Metadata   map[string]string // Merge with existing metadata
}

// Update modifies an existing entity.
func (e *Engine) Update(ctx context.Context, namespace, entityID string, opts UpdateOpts) (*types.Entity, error) {
	entity, err := e.storage.GetEntityByID(ctx, namespace, entityID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, ErrEntityNotFound
		}
		return nil, fmt.Errorf("failed to get entity: %w", err)
	}

	// Apply updates
	if opts.Summary != nil {
		entity.Summary = *opts.Summary
		// Update embedding for new summary
		if *opts.Summary != "" && e.embedding != nil {
			emb, err := e.embedding.Embed(ctx, *opts.Summary)
			if err == nil {
				e.storage.StoreEntityEmbedding(ctx, entity.ID, emb)
			}
		}
	}

	// Merge attributes
	if len(opts.Attributes) > 0 {
		if entity.Attributes == nil {
			entity.Attributes = make(map[string]string)
		}
		maps.Copy(entity.Attributes, opts.Attributes)
	}

	// Merge metadata
	if len(opts.Metadata) > 0 {
		if entity.Metadata == nil {
			entity.Metadata = make(map[string]string)
		}
		maps.Copy(entity.Metadata, opts.Metadata)
	}

	entity.LastSeenAt = time.Now().UTC()

	if err := e.storage.UpsertEntity(ctx, entity); err != nil {
		return nil, fmt.Errorf("failed to update entity: %w", err)
	}

	return entity, nil
}

// Delete removes an entity and all associated data.
func (e *Engine) Delete(ctx context.Context, namespace, entityID string) error {
	err := e.storage.DeleteEntity(ctx, namespace, entityID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrEntityNotFound
		}
		return fmt.Errorf("failed to delete entity: %w", err)
	}
	return nil
}

// ListOpts contains options for listing entities.
type ListOpts struct {
	EntityType *types.EntityType // Filter by type
	SortBy     types.EntitySortBy // Sort order
	Cursor     string             // Pagination cursor
	Limit      int                // Max results (0 = default)
}

// ListResult contains the result of listing entities.
type ListResult struct {
	Entities   []*types.Entity `json:"entities"`
	NextCursor string          `json:"next_cursor,omitempty"`
	Count      int             `json:"count"`
}

// List returns entities matching the criteria.
func (e *Engine) List(ctx context.Context, namespace string, opts *ListOpts) (*ListResult, error) {
	if opts == nil {
		opts = &ListOpts{}
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	sortBy := opts.SortBy
	if sortBy == "" {
		sortBy = types.EntitySortByLastSeen
	}

	storageOpts := storage.EntityListOpts{
		EntityType: opts.EntityType,
		SortBy:     sortBy,
		Limit:      limit,
		Cursor:     opts.Cursor,
	}

	entities, nextCursor, err := e.storage.ListEntities(ctx, namespace, storageOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to list entities: %w", err)
	}

	return &ListResult{
		Entities:   entities,
		NextCursor: nextCursor,
		Count:      len(entities),
	}, nil
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

// SearchOpts contains options for entity search.
type SearchOpts struct {
	EntityType *types.EntityType // Filter by type
	TopK       int               // Number of results (0 = default 10)
	MinScore   float64           // Minimum similarity score (0-1)
	SearchMode SearchMode        // Search mode: "vector" (default), "hybrid", or "text"
	Alpha      float64           // Hybrid search weight: 0=pure text, 1=pure vector, 0.5=equal (default: 0.5)
}

// SearchResult contains search results.
type SearchResult struct {
	Results    []*types.EntityResult `json:"results"`
	Query      string                `json:"query"`
	TotalFound int                   `json:"total_found"`
}

// Search performs semantic search across entity summaries.
func (e *Engine) Search(ctx context.Context, namespace, query string, opts *SearchOpts) (*SearchResult, error) {
	if query == "" {
		return nil, ErrEmptyQuery
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

	var results []*types.EntityResult
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

		hybridOpts := storage.HybridEntitySearchOpts{
			TopK:        topK,
			MinScore:    opts.MinScore,
			EntityType:  opts.EntityType,
			Alpha:       0.0, // Pure text search
			RRFConstant: 60,
		}
		results, err = e.storage.HybridSearchEntities(ctx, namespace, query, queryEmb, hybridOpts)

	case SearchModeHybrid:
		// Hybrid search combining vector and text
		if e.embedding == nil {
			return nil, ErrEmbeddingRequired
		}
		queryEmb, embErr := e.embedding.Embed(ctx, query)
		if embErr != nil {
			return nil, fmt.Errorf("failed to generate query embedding: %w", embErr)
		}

		hybridOpts := storage.HybridEntitySearchOpts{
			TopK:        topK,
			MinScore:    opts.MinScore,
			EntityType:  opts.EntityType,
			Alpha:       alpha,
			RRFConstant: 60,
		}
		results, err = e.storage.HybridSearchEntities(ctx, namespace, query, queryEmb, hybridOpts)

	default:
		// Pure vector search (default)
		if e.embedding == nil {
			return nil, ErrEmbeddingRequired
		}
		queryEmb, embErr := e.embedding.Embed(ctx, query)
		if embErr != nil {
			return nil, fmt.Errorf("failed to generate query embedding: %w", embErr)
		}

		searchOpts := storage.EntitySearchOpts{
			TopK:       topK,
			MinScore:   opts.MinScore,
			EntityType: opts.EntityType,
		}
		results, err = e.storage.SearchEntities(ctx, namespace, queryEmb, searchOpts)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to search entities: %w", err)
	}

	return &SearchResult{
		Results:    results,
		Query:      query,
		TotalFound: len(results),
	}, nil
}

// AddAlias registers a new alias for an entity.
func (e *Engine) AddAlias(ctx context.Context, namespace, entityID, alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return errors.New("alias cannot be empty")
	}

	// Verify entity exists
	entity, err := e.storage.GetEntityByID(ctx, namespace, entityID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrEntityNotFound
		}
		return fmt.Errorf("failed to get entity: %w", err)
	}

	// Don't add alias if it matches canonical name
	if strings.EqualFold(alias, entity.Name) {
		return nil
	}

	if err := e.storage.RegisterAlias(ctx, namespace, alias, entityID); err != nil {
		return fmt.Errorf("failed to register alias: %w", err)
	}

	// Update entity's alias list
	entity.Aliases = append(entity.Aliases, alias)
	if err := e.storage.UpsertEntity(ctx, entity); err != nil {
		return fmt.Errorf("failed to update entity aliases: %w", err)
	}

	return nil
}

// MentionOpts contains options for recording a mention.
type MentionOpts struct {
	Context string // Surrounding text
	Snippet string // Exact mention text
}

// RecordMention records a mention of an entity.
func (e *Engine) RecordMention(ctx context.Context, namespace, entityID, sourceType, sourceID string, opts *MentionOpts) error {
	if sourceID == "" {
		return ErrEmptySourceID
	}

	if opts == nil {
		opts = &MentionOpts{}
	}

	// Verify entity exists
	entity, err := e.storage.GetEntityByID(ctx, namespace, entityID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return ErrEntityNotFound
		}
		return fmt.Errorf("failed to get entity: %w", err)
	}

	mention := &types.EntityMention{
		ID:         uuid.New().String(),
		EntityID:   entityID,
		Namespace:  namespace,
		SourceType: sourceType,
		SourceID:   sourceID,
		Context:    opts.Context,
		Snippet:    opts.Snippet,
		CreatedAt:  time.Now().UTC(),
	}

	if err := e.storage.InsertMention(ctx, mention); err != nil {
		return fmt.Errorf("failed to insert mention: %w", err)
	}

	// Update entity mention count and last seen
	entity.MentionCount++
	entity.LastSeenAt = time.Now().UTC()
	if err := e.storage.UpsertEntity(ctx, entity); err != nil {
		// Log but don't fail - mention was recorded
	}

	return nil
}

// GetMentions retrieves recent mentions of an entity.
func (e *Engine) GetMentions(ctx context.Context, entityID string, limit int) ([]*types.EntityMention, error) {
	if limit <= 0 {
		limit = 20
	}

	mentions, err := e.storage.GetMentions(ctx, entityID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get mentions: %w", err)
	}

	return mentions, nil
}

// RelationshipOpts contains options for creating a relationship.
type RelationshipOpts struct {
	Description string  // Free-text description
	Confidence  float64 // 0.0-1.0 extraction confidence
}

// AddRelationship creates or updates a relationship between entities.
func (e *Engine) AddRelationship(ctx context.Context, namespace, sourceID, targetID, relationType string, opts *RelationshipOpts) (*types.EntityRelationship, error) {
	if relationType == "" {
		return nil, errors.New("relation type is required")
	}

	// Verify both entities exist
	_, err := e.storage.GetEntityByID(ctx, namespace, sourceID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, fmt.Errorf("source entity not found: %w", ErrEntityNotFound)
		}
		return nil, fmt.Errorf("failed to get source entity: %w", err)
	}

	_, err = e.storage.GetEntityByID(ctx, namespace, targetID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, fmt.Errorf("target entity not found: %w", ErrEntityNotFound)
		}
		return nil, fmt.Errorf("failed to get target entity: %w", err)
	}

	if opts == nil {
		opts = &RelationshipOpts{}
	}

	confidence := opts.Confidence
	if confidence <= 0 {
		confidence = 1.0
	}

	now := time.Now().UTC()
	rel := &types.EntityRelationship{
		ID:             uuid.New().String(),
		Namespace:      namespace,
		SourceEntityID: sourceID,
		TargetEntityID: targetID,
		RelationType:   relationType,
		Description:    opts.Description,
		Confidence:     confidence,
		MentionCount:   1,
		FirstSeenAt:    now,
		LastSeenAt:     now,
	}

	if err := e.storage.UpsertRelationship(ctx, rel); err != nil {
		return nil, fmt.Errorf("failed to create relationship: %w", err)
	}

	return rel, nil
}

// GetRelationshipsOpts contains options for retrieving relationships.
type GetRelationshipsOpts struct {
	RelationType *string                   // Filter by type
	Direction    types.RelationshipDirection // Direction filter
}

// GetRelationships retrieves relationships for an entity.
func (e *Engine) GetRelationships(ctx context.Context, namespace, entityID string, opts *GetRelationshipsOpts) ([]*types.EntityRelationship, error) {
	if opts == nil {
		opts = &GetRelationshipsOpts{}
	}

	direction := opts.Direction
	if direction == "" {
		direction = types.RelationshipDirectionBoth
	}

	storageOpts := storage.RelationshipOpts{
		RelationType: opts.RelationType,
		Direction:    direction,
	}

	relationships, err := e.storage.GetRelationships(ctx, namespace, entityID, storageOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to get relationships: %w", err)
	}

	return relationships, nil
}

// MergeResult contains the result of entity merging.
type MergeResult struct {
	KeptEntity          *types.Entity `json:"kept_entity"`
	MergedMentions      int           `json:"merged_mentions"`
	MergedRelationships int           `json:"merged_relationships"`
}

// Merge combines two entities, moving all data from source to target.
func (e *Engine) Merge(ctx context.Context, namespace, sourceID, targetID string) (*MergeResult, error) {
	if sourceID == targetID {
		return nil, ErrSelfMerge
	}

	// Get both entities
	source, err := e.storage.GetEntityByID(ctx, namespace, sourceID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, fmt.Errorf("source entity not found: %w", ErrEntityNotFound)
		}
		return nil, fmt.Errorf("failed to get source entity: %w", err)
	}

	target, err := e.storage.GetEntityByID(ctx, namespace, targetID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, fmt.Errorf("target entity not found: %w", ErrEntityNotFound)
		}
		return nil, fmt.Errorf("failed to get target entity: %w", err)
	}

	// Merge source aliases into target
	aliasSet := make(map[string]bool)
	for _, a := range target.Aliases {
		aliasSet[strings.ToLower(a)] = true
	}
	for _, a := range source.Aliases {
		if !aliasSet[strings.ToLower(a)] {
			target.Aliases = append(target.Aliases, a)
		}
	}
	// Add source name as alias
	if !aliasSet[strings.ToLower(source.Name)] && !strings.EqualFold(source.Name, target.Name) {
		target.Aliases = append(target.Aliases, source.Name)
	}

	// Merge attributes
	if source.Attributes != nil {
		if target.Attributes == nil {
			target.Attributes = make(map[string]string)
		}
		for k, v := range source.Attributes {
			if _, exists := target.Attributes[k]; !exists {
				target.Attributes[k] = v
			}
		}
	}

	// Update mention count
	target.MentionCount += source.MentionCount

	// Update first seen
	if source.FirstSeenAt.Before(target.FirstSeenAt) {
		target.FirstSeenAt = source.FirstSeenAt
	}

	// Update last seen
	if source.LastSeenAt.After(target.LastSeenAt) {
		target.LastSeenAt = source.LastSeenAt
	}

	// Update target entity
	if err := e.storage.UpsertEntity(ctx, target); err != nil {
		return nil, fmt.Errorf("failed to update target entity: %w", err)
	}

	// Perform the storage-level merge (moves mentions, relationships, etc.)
	if err := e.storage.MergeEntities(ctx, namespace, sourceID, targetID); err != nil {
		return nil, fmt.Errorf("failed to merge entities: %w", err)
	}

	return &MergeResult{
		KeptEntity:          target,
		MergedMentions:      int(source.MentionCount),
		MergedRelationships: 0, // TODO: count relationships
	}, nil
}

// EnqueueExtraction adds content to the extraction queue.
func (e *Engine) EnqueueExtraction(ctx context.Context, namespace, sourceType, sourceID, content string) error {
	if content == "" {
		return errors.New("content cannot be empty")
	}

	if sourceID == "" {
		return ErrEmptySourceID
	}

	item := &types.ExtractionQueueItem{
		Namespace:  namespace,
		SourceType: sourceType,
		SourceID:   sourceID,
		Content:    content,
		Status:     "pending",
		Attempts:   0,
		CreatedAt:  time.Now().UTC(),
	}

	if err := e.storage.EnqueueExtraction(ctx, item); err != nil {
		return fmt.Errorf("failed to enqueue extraction: %w", err)
	}

	return nil
}

// DequeueExtraction retrieves items from the extraction queue for processing.
func (e *Engine) DequeueExtraction(ctx context.Context, batchSize int) ([]*types.ExtractionQueueItem, error) {
	if batchSize <= 0 {
		batchSize = 10
	}

	items, err := e.storage.DequeueExtraction(ctx, batchSize)
	if err != nil {
		return nil, fmt.Errorf("failed to dequeue extraction items: %w", err)
	}

	return items, nil
}

// CompleteExtraction marks an extraction queue item as completed or failed.
func (e *Engine) CompleteExtraction(ctx context.Context, itemID int64, status string) error {
	if status != "completed" && status != "failed" && status != "dead_letter" {
		return fmt.Errorf("invalid status: %s", status)
	}

	if err := e.storage.CompleteExtraction(ctx, itemID, status); err != nil {
		return fmt.Errorf("failed to complete extraction: %w", err)
	}

	return nil
}

// ExtractionQueueStats returns statistics about the extraction queue.
func (e *Engine) ExtractionQueueStats(ctx context.Context) (*storage.ExtractionQueueStats, error) {
	stats, err := e.storage.GetExtractionQueueStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get extraction queue stats: %w", err)
	}

	return stats, nil
}

// Query retrieves an entity with its relationships and recent mentions.
func (e *Engine) Query(ctx context.Context, namespace, nameOrAlias string, mentionLimit int) (*types.EntityQueryResponse, error) {
	entity, err := e.Resolve(ctx, namespace, nameOrAlias)
	if err != nil {
		if errors.Is(err, ErrEntityNotFound) {
			return &types.EntityQueryResponse{Found: false}, nil
		}
		return nil, err
	}

	if mentionLimit <= 0 {
		mentionLimit = 10
	}

	// Get relationships
	relationships, _ := e.GetRelationships(ctx, namespace, entity.ID, nil)

	// Get mentions
	mentions, _ := e.GetMentions(ctx, entity.ID, mentionLimit)

	return &types.EntityQueryResponse{
		Entity:        entity,
		Relationships: relationships,
		Mentions:      mentions,
		Found:         true,
	}, nil
}
