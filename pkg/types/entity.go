package types

import "time"

// EntityType represents the type of an entity.
type EntityType string

const (
	EntityTypePerson       EntityType = "person"
	EntityTypeOrganization EntityType = "organization"
	EntityTypeProduct      EntityType = "product"
	EntityTypeLocation     EntityType = "location"
	EntityTypeConcept      EntityType = "concept"
)

// Entity represents a named entity (person, organization, product, etc.).
type Entity struct {
	ID           string            `json:"id"`
	Namespace    string            `json:"namespace"`
	Name         string            `json:"name"`          // Canonical name
	Type         EntityType        `json:"type"`          // "person", "organization", "product", "location", "concept"
	Aliases      []string          `json:"aliases"`       // Alternative names, abbreviations
	Summary      string            `json:"summary"`       // LLM-generated summary of everything known
	Attributes   map[string]string `json:"attributes"`    // Structured facts (e.g., "role": "CEO", "founded": "2021")
	Metadata     map[string]string `json:"metadata,omitempty"`
	MentionCount int64             `json:"mention_count"`
	FirstSeenAt  time.Time         `json:"first_seen_at"`
	LastSeenAt   time.Time         `json:"last_seen_at"`
}

// EntityMention represents a mention of an entity in source content.
type EntityMention struct {
	ID         string    `json:"id"`
	EntityID   string    `json:"entity_id"`
	Namespace  string    `json:"namespace"`
	SourceType string    `json:"source_type"` // "conversation", "knowledge", "manual"
	SourceID   string    `json:"source_id"`   // Message ID, chunk ID, or manual entry ID
	Context    string    `json:"context"`     // Surrounding text where the entity was mentioned
	Snippet    string    `json:"snippet"`     // The exact mention text
	CreatedAt  time.Time `json:"created_at"`
}

// EntityRelationship represents a relationship between two entities.
type EntityRelationship struct {
	ID             string    `json:"id"`
	Namespace      string    `json:"namespace"`
	SourceEntityID string    `json:"source_entity_id"`
	TargetEntityID string    `json:"target_entity_id"`
	RelationType   string    `json:"relation_type"` // "works_at", "competes_with", "part_of", "related_to"
	Description    string    `json:"description"`   // Free-text description of the relationship
	Confidence     float64   `json:"confidence"`    // 0.0-1.0 extraction confidence
	MentionCount   int64     `json:"mention_count"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
}

// RelationshipDirection specifies the direction for relationship queries.
type RelationshipDirection string

const (
	RelationshipDirectionOutgoing RelationshipDirection = "outgoing"
	RelationshipDirectionIncoming RelationshipDirection = "incoming"
	RelationshipDirectionBoth     RelationshipDirection = "both"
)

// EntitySortBy specifies the sort order for entity list queries.
type EntitySortBy string

const (
	EntitySortByName         EntitySortBy = "name"
	EntitySortByMentionCount EntitySortBy = "mention_count"
	EntitySortByLastSeen     EntitySortBy = "last_seen"
)

// EntityResult represents a search result for an entity.
type EntityResult struct {
	Entity *Entity `json:"entity"`
	Score  float64 `json:"score"`
	Rank   int     `json:"rank,omitempty"` // Position in result set (for RRF)
}

// EntityQueryResponse represents the response from entity.query.
type EntityQueryResponse struct {
	Entity        *Entity               `json:"entity,omitempty"`
	Relationships []*EntityRelationship `json:"relationships,omitempty"`
	Mentions      []*EntityMention      `json:"mentions,omitempty"`
	Found         bool                  `json:"found"`
}

// EntitySearchResponse represents the response from entity.search.
type EntitySearchResponse struct {
	Results    []*EntityResult `json:"results"`
	TotalFound int             `json:"total_found"`
}

// EntityRelationshipsResponse represents the response from entity.relationships.
type EntityRelationshipsResponse struct {
	EntityName    string                        `json:"entity_name"`
	Relationships []*EntityRelationshipWithName `json:"relationships"`
}

// EntityRelationshipWithName includes the related entity name for display.
type EntityRelationshipWithName struct {
	RelatedEntity     string    `json:"related_entity"`
	RelatedEntityType string    `json:"related_entity_type"`
	RelationType      string    `json:"relation_type"`
	Description       string    `json:"description"`
	Direction         string    `json:"direction"` // "outgoing" or "incoming"
	Confidence        float64   `json:"confidence"`
	MentionCount      int64     `json:"mention_count"`
}

// EntityUpdateResponse represents the response from entity.update.
type EntityUpdateResponse struct {
	EntityName    string   `json:"entity_name"`
	UpdatedFields []string `json:"updated_fields"`
}

// EntityMergeResponse represents the response from entity.merge.
type EntityMergeResponse struct {
	KeptEntity          string `json:"kept_entity"`
	MergedMentions      int    `json:"merged_mentions"`
	MergedRelationships int    `json:"merged_relationships"`
}

// EntityListResponse represents the response from entity.list.
type EntityListResponse struct {
	Entities   []*EntityListItem `json:"entities"`
	TotalCount int               `json:"total_count"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// EntityListItem is a lightweight entity representation for list responses.
type EntityListItem struct {
	Name         string     `json:"name"`
	Type         EntityType `json:"type"`
	MentionCount int64      `json:"mention_count"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
}

// ExtractionQueueItem represents an item in the entity extraction queue.
type ExtractionQueueItem struct {
	ID          int64     `json:"id"`
	Namespace   string    `json:"namespace"`
	SourceType  string    `json:"source_type"` // "conversation", "knowledge"
	SourceID    string    `json:"source_id"`
	Content     string    `json:"content"`
	Status      string    `json:"status"` // "pending", "processing", "completed", "failed", "dead_letter"
	Attempts    int       `json:"attempts"`
	CreatedAt   time.Time `json:"created_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

// ExtractedEntity represents an entity extracted by the LLM.
type ExtractedEntity struct {
	Name         string                 `json:"name"`
	Type         EntityType             `json:"type"`
	Aliases      []string               `json:"aliases,omitempty"`
	Attributes   map[string]string      `json:"attributes,omitempty"`
	Relationships []ExtractedRelationship `json:"relationships,omitempty"`
}

// ExtractedRelationship represents a relationship extracted by the LLM.
type ExtractedRelationship struct {
	TargetName   string  `json:"target_name"`
	RelationType string  `json:"relation_type"`
	Description  string  `json:"description,omitempty"`
	Confidence   float64 `json:"confidence,omitempty"`
}
