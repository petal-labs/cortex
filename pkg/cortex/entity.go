package cortex

import (
	"context"
	"errors"
	"fmt"

	"github.com/petal-labs/cortex/v2/internal/entity"
	"github.com/petal-labs/cortex/v2/pkg/types"
)

// EntityClient provides typed access to Cortex Entity Memory.
type EntityClient struct {
	engine *entity.Engine
}

// EntityCreateOpts configures EntityClient.Create.
type EntityCreateOpts struct {
	Aliases    []string
	Summary    string
	Attributes map[string]string
	Metadata   map[string]string
}

// EntityCreateResult contains the result of EntityClient.Create.
type EntityCreateResult struct {
	Entity  *types.Entity `json:"entity"`
	Created bool          `json:"created"`
}

// EntityUpdateOpts configures EntityClient.Update.
type EntityUpdateOpts struct {
	Summary    *string
	Attributes map[string]string
	Metadata   map[string]string
}

// EntityListOpts configures EntityClient.List.
type EntityListOpts struct {
	EntityType *types.EntityType
	SortBy     types.EntitySortBy
	Cursor     string
	Limit      int
}

// EntityListResult contains the result of EntityClient.List.
type EntityListResult struct {
	Entities   []*types.Entity `json:"entities"`
	NextCursor string          `json:"next_cursor,omitempty"`
	Count      int             `json:"count"`
}

// EntitySearchOpts configures EntityClient.Search.
type EntitySearchOpts struct {
	EntityType *types.EntityType
	TopK       int
	MinScore   float64
	SearchMode SearchMode
	Alpha      float64
}

// EntitySearchResult contains the result of EntityClient.Search.
type EntitySearchResult struct {
	Results    []*types.EntityResult `json:"results"`
	Query      string                `json:"query"`
	TotalFound int                   `json:"total_found"`
}

// EntityMentionOpts configures EntityClient.RecordMention.
type EntityMentionOpts struct {
	Context string
	Snippet string
}

// RelationshipOpts configures EntityClient.AddRelationship.
type RelationshipOpts struct {
	Description string
	Confidence  float64
}

// GetRelationshipsOpts configures EntityClient.GetRelationships.
type GetRelationshipsOpts struct {
	RelationType *string
	Direction    types.RelationshipDirection
}

// EntityMergeResult contains the result of EntityClient.Merge.
type EntityMergeResult struct {
	KeptEntity          *types.Entity `json:"kept_entity"`
	MergedMentions      int           `json:"merged_mentions"`
	MergedRelationships int           `json:"merged_relationships"`
}

// Create creates an entity or returns an existing entity with the same name.
func (e *EntityClient) Create(ctx context.Context, namespace, name string, entityType types.EntityType, opts *EntityCreateOpts) (*EntityCreateResult, error) {
	var internalOpts *entity.CreateOpts
	if opts != nil {
		internalOpts = &entity.CreateOpts{
			Aliases:    opts.Aliases,
			Summary:    opts.Summary,
			Attributes: opts.Attributes,
			Metadata:   opts.Metadata,
		}
	}
	res, err := e.engine.Create(ctx, namespace, name, entityType, internalOpts)
	if err != nil {
		return nil, mapEntityErr(err)
	}
	return &EntityCreateResult{
		Entity:  res.Entity,
		Created: res.Created,
	}, nil
}

// Get retrieves an entity by ID.
func (e *EntityClient) Get(ctx context.Context, namespace, entityID string) (*types.Entity, error) {
	ent, err := e.engine.Get(ctx, namespace, entityID)
	if err != nil {
		return nil, mapEntityErr(err)
	}
	return ent, nil
}

// GetByName retrieves an entity by canonical name (case-insensitive).
func (e *EntityClient) GetByName(ctx context.Context, namespace, name string) (*types.Entity, error) {
	ent, err := e.engine.GetByName(ctx, namespace, name)
	if err != nil {
		return nil, mapEntityErr(err)
	}
	return ent, nil
}

// Resolve resolves an entity by canonical name or registered alias.
func (e *EntityClient) Resolve(ctx context.Context, namespace, nameOrAlias string) (*types.Entity, error) {
	ent, err := e.engine.Resolve(ctx, namespace, nameOrAlias)
	if err != nil {
		return nil, mapEntityErr(err)
	}
	return ent, nil
}

// Query resolves an entity by name or alias and returns its relationships and recent mentions.
func (e *EntityClient) Query(ctx context.Context, namespace, nameOrAlias string, mentionLimit int) (*types.EntityQueryResponse, error) {
	resp, err := e.engine.Query(ctx, namespace, nameOrAlias, mentionLimit)
	if err != nil {
		return nil, mapEntityErr(err)
	}
	return resp, nil
}

// Search performs vector, hybrid, or text search across entities.
func (e *EntityClient) Search(ctx context.Context, namespace, query string, opts *EntitySearchOpts) (*EntitySearchResult, error) {
	var internalOpts *entity.SearchOpts
	if opts != nil {
		internalOpts = &entity.SearchOpts{
			EntityType: opts.EntityType,
			TopK:       opts.TopK,
			MinScore:   opts.MinScore,
			SearchMode: entity.SearchMode(opts.SearchMode),
			Alpha:      opts.Alpha,
		}
	}
	res, err := e.engine.Search(ctx, namespace, query, internalOpts)
	if err != nil {
		return nil, mapEntityErr(err)
	}
	return &EntitySearchResult{
		Results:    res.Results,
		Query:      res.Query,
		TotalFound: res.TotalFound,
	}, nil
}

// Update modifies an existing entity's summary, attributes, or metadata.
func (e *EntityClient) Update(ctx context.Context, namespace, entityID string, opts EntityUpdateOpts) (*types.Entity, error) {
	ent, err := e.engine.Update(ctx, namespace, entityID, entity.UpdateOpts{
		Summary:    opts.Summary,
		Attributes: opts.Attributes,
		Metadata:   opts.Metadata,
	})
	if err != nil {
		return nil, mapEntityErr(err)
	}
	return ent, nil
}

// Delete removes an entity and all associated mentions and relationships.
func (e *EntityClient) Delete(ctx context.Context, namespace, entityID string) error {
	return mapEntityErr(e.engine.Delete(ctx, namespace, entityID))
}

// Merge merges sourceID into targetID, deleting sourceID.
func (e *EntityClient) Merge(ctx context.Context, namespace, sourceID, targetID string) (*EntityMergeResult, error) {
	res, err := e.engine.Merge(ctx, namespace, sourceID, targetID)
	if err != nil {
		return nil, mapEntityErr(err)
	}
	return &EntityMergeResult{
		KeptEntity:          res.KeptEntity,
		MergedMentions:      res.MergedMentions,
		MergedRelationships: res.MergedRelationships,
	}, nil
}

// List lists entities in a namespace with optional type filter, sorting, and pagination.
func (e *EntityClient) List(ctx context.Context, namespace string, opts *EntityListOpts) (*EntityListResult, error) {
	var internalOpts *entity.ListOpts
	if opts != nil {
		internalOpts = &entity.ListOpts{
			EntityType: opts.EntityType,
			SortBy:     opts.SortBy,
			Cursor:     opts.Cursor,
			Limit:      opts.Limit,
		}
	}
	res, err := e.engine.List(ctx, namespace, internalOpts)
	if err != nil {
		return nil, mapEntityErr(err)
	}
	return &EntityListResult{
		Entities:   res.Entities,
		NextCursor: res.NextCursor,
		Count:      res.Count,
	}, nil
}

// AddAlias registers an alternative name for an entity.
func (e *EntityClient) AddAlias(ctx context.Context, namespace, entityID, alias string) error {
	return mapEntityErr(e.engine.AddAlias(ctx, namespace, entityID, alias))
}

// RecordMention records a mention of an entity in source content.
func (e *EntityClient) RecordMention(ctx context.Context, namespace, entityID, sourceType, sourceID string, opts *EntityMentionOpts) error {
	var internalOpts *entity.MentionOpts
	if opts != nil {
		internalOpts = &entity.MentionOpts{
			Context: opts.Context,
			Snippet: opts.Snippet,
		}
	}
	return mapEntityErr(e.engine.RecordMention(ctx, namespace, entityID, sourceType, sourceID, internalOpts))
}

// GetMentions retrieves recent mentions for an entity.
func (e *EntityClient) GetMentions(ctx context.Context, entityID string, limit int) ([]*types.EntityMention, error) {
	mentions, err := e.engine.GetMentions(ctx, entityID, limit)
	if err != nil {
		return nil, mapEntityErr(err)
	}
	return mentions, nil
}

// AddRelationship creates or updates a directed relationship between two entities.
func (e *EntityClient) AddRelationship(ctx context.Context, namespace, sourceID, targetID, relationType string, opts *RelationshipOpts) (*types.EntityRelationship, error) {
	var internalOpts *entity.RelationshipOpts
	if opts != nil {
		internalOpts = &entity.RelationshipOpts{
			Description: opts.Description,
			Confidence:  opts.Confidence,
		}
	}
	rel, err := e.engine.AddRelationship(ctx, namespace, sourceID, targetID, relationType, internalOpts)
	if err != nil {
		return nil, mapEntityErr(err)
	}
	return rel, nil
}

// GetRelationships retrieves relationships for an entity.
func (e *EntityClient) GetRelationships(ctx context.Context, namespace, entityID string, opts *GetRelationshipsOpts) ([]*types.EntityRelationship, error) {
	var internalOpts *entity.GetRelationshipsOpts
	if opts != nil {
		internalOpts = &entity.GetRelationshipsOpts{
			RelationType: opts.RelationType,
			Direction:    opts.Direction,
		}
	}
	rels, err := e.engine.GetRelationships(ctx, namespace, entityID, internalOpts)
	if err != nil {
		return nil, mapEntityErr(err)
	}
	return rels, nil
}

func mapEntityErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, entity.ErrEntityNotFound):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case errors.Is(err, entity.ErrEmptyName),
		errors.Is(err, entity.ErrInvalidType),
		errors.Is(err, entity.ErrSelfMerge),
		errors.Is(err, entity.ErrEmptyQuery),
		errors.Is(err, entity.ErrEmptySourceID):
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	default:
		return err
	}
}
