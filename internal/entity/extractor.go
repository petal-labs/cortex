package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/llm"
	"github.com/petal-labs/cortex/pkg/types"
	"github.com/petal-labs/iris/core"
)

// ExtractionPrompt is the system prompt for entity extraction.
const ExtractionPrompt = `Extract all named entities from the following text. For each entity, provide:
- name: The canonical name
- type: One of "person", "organization", "product", "location", "concept"
- aliases: Any alternative names or abbreviations used in the text
- attributes: Key facts mentioned about the entity
- confidence: How confident you are in this extraction (0.0-1.0)

Return your response as a JSON array of entities. If no entities are found, return an empty array [].

Example response:
[
  {
    "name": "International Business Machines",
    "type": "organization",
    "aliases": ["IBM", "Big Blue"],
    "attributes": {"industry": "technology", "founded": "1911"},
    "confidence": 0.95
  }
]

Text:`

// ExtractedEntity represents an entity extracted from text by the LLM.
type ExtractedEntity struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Aliases    []string          `json:"aliases,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Confidence float64           `json:"confidence"`
}

// ExtractedRelationship represents a relationship between extracted entities.
type ExtractedRelationship struct {
	SourceName   string  `json:"source_name"`
	TargetName   string  `json:"target_name"`
	RelationType string  `json:"relation_type"`
	Description  string  `json:"description,omitempty"`
	Confidence   float64 `json:"confidence"`
}

// ExtractionResult contains the result of entity extraction.
type ExtractionResult struct {
	Entities      []ExtractedEntity       `json:"entities"`
	Relationships []ExtractedRelationship `json:"relationships,omitempty"`
	SourceText    string                  `json:"-"` // Original text (not serialized)
}

// Extractor extracts entities from text using an LLM via the iris SDK.
type Extractor struct {
	client    *core.Client
	model     core.ModelID
	maxTokens int
}

// NewExtractor creates a new entity extractor using the iris SDK.
func NewExtractor(cfg *config.Config) (*Extractor, error) {
	if cfg.Summarization.Provider == "" {
		return nil, fmt.Errorf("summarization provider is required for entity extraction")
	}

	provider, err := llm.NewProvider(cfg.Summarization.Provider)
	if err != nil {
		return nil, fmt.Errorf("failed to create entity extraction provider: %w", err)
	}

	client := llm.NewClient(provider)

	return &Extractor{
		client:    client,
		model:     core.ModelID(cfg.Entity.ExtractionModel),
		maxTokens: 2048, // Sufficient for entity extraction responses
	}, nil
}

// Extract extracts entities from the given text.
func (e *Extractor) Extract(ctx context.Context, text string) (*ExtractionResult, error) {
	if strings.TrimSpace(text) == "" {
		return &ExtractionResult{Entities: []ExtractedEntity{}}, nil
	}

	content, err := e.complete(ctx, ExtractionPrompt+"\n\n"+text)
	if err != nil {
		return nil, fmt.Errorf("extraction completion failed: %w", err)
	}

	// Parse the JSON response
	entities, err := parseExtractionResponse(content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse extraction response: %w", err)
	}

	// Validate and normalize entities
	validEntities := make([]ExtractedEntity, 0, len(entities))
	for _, ent := range entities {
		if err := validateExtractedEntity(&ent); err != nil {
			continue // Skip invalid entities
		}
		normalizeExtractedEntity(&ent)
		validEntities = append(validEntities, ent)
	}

	// Extract relationships from co-mentioned entities
	relationships := extractRelationships(validEntities, text)

	return &ExtractionResult{
		Entities:      validEntities,
		Relationships: relationships,
		SourceText:    text,
	}, nil
}

// complete sends a completion request using the iris SDK.
func (e *Extractor) complete(ctx context.Context, userMessage string) (string, error) {
	builder := e.client.Chat(e.model).
		User(userMessage)

	if e.maxTokens > 0 {
		builder = builder.MaxTokens(e.maxTokens)
	}

	resp, err := builder.GetResponse(ctx)
	if err != nil {
		return "", fmt.Errorf("completion request failed: %w", err)
	}

	return resp.Output, nil
}

// parseExtractionResponse parses the LLM response into extracted entities.
func parseExtractionResponse(content string) ([]ExtractedEntity, error) {
	// Try to extract JSON from the response
	content = strings.TrimSpace(content)

	// Handle markdown code blocks if present
	if strings.HasPrefix(content, "```") {
		// Find the content between code blocks
		start := strings.Index(content, "\n")
		end := strings.LastIndex(content, "```")
		if start > 0 && end > start {
			content = strings.TrimSpace(content[start:end])
		}
	}

	// Try to find JSON array in the response
	startIdx := strings.Index(content, "[")
	endIdx := strings.LastIndex(content, "]")
	if startIdx >= 0 && endIdx > startIdx {
		content = content[startIdx : endIdx+1]
	}

	var entities []ExtractedEntity
	if err := json.Unmarshal([]byte(content), &entities); err != nil {
		// Try parsing as a single entity
		var single ExtractedEntity
		if singleErr := json.Unmarshal([]byte(content), &single); singleErr == nil && single.Name != "" {
			return []ExtractedEntity{single}, nil
		}
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	return entities, nil
}

// validateExtractedEntity checks if an extracted entity is valid.
func validateExtractedEntity(ent *ExtractedEntity) error {
	if strings.TrimSpace(ent.Name) == "" {
		return fmt.Errorf("entity name is empty")
	}
	if !isValidEntityType(ent.Type) {
		return fmt.Errorf("invalid entity type: %s", ent.Type)
	}
	return nil
}

// normalizeExtractedEntity normalizes entity fields.
func normalizeExtractedEntity(ent *ExtractedEntity) {
	ent.Name = strings.TrimSpace(ent.Name)
	ent.Type = strings.ToLower(strings.TrimSpace(ent.Type))

	// Normalize aliases
	normalizedAliases := make([]string, 0, len(ent.Aliases))
	seen := make(map[string]bool)
	seen[strings.ToLower(ent.Name)] = true
	for _, alias := range ent.Aliases {
		alias = strings.TrimSpace(alias)
		if alias != "" && !seen[strings.ToLower(alias)] {
			normalizedAliases = append(normalizedAliases, alias)
			seen[strings.ToLower(alias)] = true
		}
	}
	ent.Aliases = normalizedAliases

	// Ensure confidence is in valid range
	if ent.Confidence < 0 {
		ent.Confidence = 0
	} else if ent.Confidence > 1 {
		ent.Confidence = 1
	}

	// Initialize attributes if nil
	if ent.Attributes == nil {
		ent.Attributes = make(map[string]string)
	}
}

// isValidEntityType checks if a type string is a valid entity type.
func isValidEntityType(t string) bool {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "person", "organization", "product", "location", "concept":
		return true
	default:
		return false
	}
}

// extractRelationships creates relationships between co-mentioned entities.
// Entities mentioned in the same text are assumed to be related.
func extractRelationships(entities []ExtractedEntity, _ string) []ExtractedRelationship {
	if len(entities) < 2 {
		return nil
	}

	var relationships []ExtractedRelationship

	// Create "related_to" relationships between co-mentioned entities
	// Use lower confidence since we're inferring from co-occurrence
	for i := range len(entities) {
		for j := i + 1; j < len(entities); j++ {
			rel := ExtractedRelationship{
				SourceName:   entities[i].Name,
				TargetName:   entities[j].Name,
				RelationType: "related_to",
				Confidence:   0.5, // Co-occurrence implies weak relationship
			}
			relationships = append(relationships, rel)
		}
	}

	return relationships
}

// ToEntityType converts a string to types.EntityType.
func ToEntityType(s string) types.EntityType {
	switch strings.ToLower(s) {
	case "person":
		return types.EntityTypePerson
	case "organization":
		return types.EntityTypeOrganization
	case "product":
		return types.EntityTypeProduct
	case "location":
		return types.EntityTypeLocation
	case "concept":
		return types.EntityTypeConcept
	default:
		return types.EntityTypeConcept // Default to concept
	}
}
