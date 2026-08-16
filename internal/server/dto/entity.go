package dto

import (
	"time"

	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/pkg/types"
)

// Entity mirrors types.Entity.
type Entity struct {
	ID           string            `json:"id"`
	Namespace    string            `json:"namespace"`
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Aliases      []string          `json:"aliases"`
	Summary      string            `json:"summary"`
	Attributes   map[string]string `json:"attributes"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	MentionCount int64             `json:"mention_count"`
	FirstSeenAt  time.Time         `json:"first_seen_at"`
	LastSeenAt   time.Time         `json:"last_seen_at"`
}

func newEntity(e *types.Entity) *Entity {
	if e == nil {
		return nil
	}
	// Collections are never nil on the wire: clients iterate them directly
	// (entity.aliases.map(...) etc.) and a JSON null would throw. Nil from
	// storage becomes an empty [] / {} instead.
	aliases := e.Aliases
	if aliases == nil {
		aliases = []string{}
	}
	attributes := e.Attributes
	if attributes == nil {
		attributes = map[string]string{}
	}
	return &Entity{
		ID:           e.ID,
		Namespace:    e.Namespace,
		Name:         e.Name,
		Type:         string(e.Type),
		Aliases:      aliases,
		Summary:      e.Summary,
		Attributes:   attributes,
		Metadata:     e.Metadata,
		MentionCount: e.MentionCount,
		FirstSeenAt:  e.FirstSeenAt,
		LastSeenAt:   e.LastSeenAt,
	}
}

// EntityResult mirrors types.EntityResult.
type EntityResult struct {
	Entity *Entity `json:"entity"`
	Score  float64 `json:"score"`
	Rank   int     `json:"rank,omitempty"`
}

// EntityMention mirrors types.EntityMention.
type EntityMention struct {
	ID         string    `json:"id"`
	EntityID   string    `json:"entity_id"`
	Namespace  string    `json:"namespace"`
	SourceType string    `json:"source_type"`
	SourceID   string    `json:"source_id"`
	Context    string    `json:"context"`
	Snippet    string    `json:"snippet"`
	CreatedAt  time.Time `json:"created_at"`
}

// EntityRelationship mirrors types.EntityRelationship.
type EntityRelationship struct {
	ID             string    `json:"id"`
	Namespace      string    `json:"namespace"`
	SourceEntityID string    `json:"source_entity_id"`
	TargetEntityID string    `json:"target_entity_id"`
	RelationType   string    `json:"relation_type"`
	Description    string    `json:"description"`
	Confidence     float64   `json:"confidence"`
	MentionCount   int64     `json:"mention_count"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
}

func newRelationship(r *types.EntityRelationship) EntityRelationship {
	if r == nil {
		return EntityRelationship{}
	}
	return EntityRelationship{
		ID:             r.ID,
		Namespace:      r.Namespace,
		SourceEntityID: r.SourceEntityID,
		TargetEntityID: r.TargetEntityID,
		RelationType:   r.RelationType,
		Description:    r.Description,
		Confidence:     r.Confidence,
		MentionCount:   r.MentionCount,
		FirstSeenAt:    r.FirstSeenAt,
		LastSeenAt:     r.LastSeenAt,
	}
}

// EntityQuery is the entity_query response.
type EntityQuery struct {
	Contract
	Entity        *Entity              `json:"entity,omitempty"`
	Relationships []EntityRelationship `json:"relationships,omitempty"`
	Mentions      []EntityMention      `json:"mentions,omitempty"`
	Found         bool                 `json:"found"`
}

// NewEntityQuery maps an engine query result.
func NewEntityQuery(r *types.EntityQueryResponse) EntityQuery {
	if r == nil {
		return EntityQuery{Contract: Contract{SchemaVersion}}
	}
	out := EntityQuery{
		Contract: Contract{SchemaVersion},
		Entity:   newEntity(r.Entity),
		Found:    r.Found,
	}
	for _, rel := range r.Relationships {
		out.Relationships = append(out.Relationships, newRelationship(rel))
	}
	for _, m := range r.Mentions {
		if m == nil {
			continue
		}
		out.Mentions = append(out.Mentions, EntityMention{
			ID:         m.ID,
			EntityID:   m.EntityID,
			Namespace:  m.Namespace,
			SourceType: m.SourceType,
			SourceID:   m.SourceID,
			Context:    m.Context,
			Snippet:    m.Snippet,
			CreatedAt:  m.CreatedAt,
		})
	}
	return out
}

// EntitySearch is the entity_search response.
type EntitySearch struct {
	Contract
	Results    []EntityResult `json:"results"`
	Query      string         `json:"query"`
	TotalFound int            `json:"total_found"`
}

// NewEntitySearch maps an engine search result.
func NewEntitySearch(r *entity.SearchResult) EntitySearch {
	if r == nil {
		return EntitySearch{Contract: Contract{SchemaVersion}, Results: []EntityResult{}}
	}
	out := EntitySearch{
		Contract:   Contract{SchemaVersion},
		Results:    make([]EntityResult, 0, len(r.Results)),
		Query:      r.Query,
		TotalFound: r.TotalFound,
	}
	for _, res := range r.Results {
		if res == nil {
			continue
		}
		out.Results = append(out.Results, EntityResult{
			Entity: newEntity(res.Entity),
			Score:  res.Score,
			Rank:   res.Rank,
		})
	}
	return out
}

// EntityRelationships is the entity_relationships response. It wraps the
// relationship list in an object (with schema_version) so the tool can
// declare an object-rooted output schema per the MCP spec.
type EntityRelationships struct {
	Contract
	Relationships []EntityRelationship `json:"relationships"`
}

// NewEntityRelationships maps a relationships result.
func NewEntityRelationships(rels []*types.EntityRelationship) EntityRelationships {
	out := EntityRelationships{
		Contract:      Contract{SchemaVersion},
		Relationships: make([]EntityRelationship, 0, len(rels)),
	}
	for _, rel := range rels {
		out.Relationships = append(out.Relationships, newRelationship(rel))
	}
	return out
}

// EntityUpdate is the entity_update response.
type EntityUpdate struct {
	Contract
	Entity
}

// NewEntityUpdate maps an updated entity.
func NewEntityUpdate(e *types.Entity) EntityUpdate {
	return EntityUpdate{Contract{SchemaVersion}, *newEntity(e)}
}

// EntityMerge is the entity_merge response.
type EntityMerge struct {
	Contract
	KeptEntity          *Entity `json:"kept_entity"`
	MergedMentions      int     `json:"merged_mentions"`
	MergedRelationships int     `json:"merged_relationships"`
}

// NewEntityMerge maps an engine merge result.
func NewEntityMerge(r *entity.MergeResult) EntityMerge {
	if r == nil {
		return EntityMerge{Contract: Contract{SchemaVersion}}
	}
	return EntityMerge{
		Contract:            Contract{SchemaVersion},
		KeptEntity:          newEntity(r.KeptEntity),
		MergedMentions:      r.MergedMentions,
		MergedRelationships: r.MergedRelationships,
	}
}

// EntityList is the entity_list response.
type EntityList struct {
	Contract
	Entities   []Entity `json:"entities"`
	NextCursor string   `json:"next_cursor,omitempty"`
	Count      int      `json:"count"`
}

// NewEntityList maps an engine list result.
func NewEntityList(r *entity.ListResult) EntityList {
	if r == nil {
		return EntityList{Contract: Contract{SchemaVersion}, Entities: []Entity{}}
	}
	out := EntityList{
		Contract:   Contract{SchemaVersion},
		Entities:   make([]Entity, 0, len(r.Entities)),
		NextCursor: r.NextCursor,
		Count:      r.Count,
	}
	for _, e := range r.Entities {
		out.Entities = append(out.Entities, *newEntity(e))
	}
	return out
}
