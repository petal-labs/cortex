package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/petal-labs/iris/core"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/llm"
	"github.com/petal-labs/cortex/pkg/types"
)

// ExtractionPrompt is the system prompt for entity extraction.
// With structured output, we don't need to specify the JSON format.
const ExtractionPrompt = `Extract all named entities from the following text. For each entity, identify:
- name: The canonical name of the entity
- type: Classify as "person", "organization", "product", "location", "concept", "event", or "other"
- aliases: Any alternative names or abbreviations used in the text
- attributes: Key facts mentioned about the entity as key-value pairs
- confidence: Your confidence in this extraction (0.0-1.0)

If no entities are found, return an empty array.

Text:`

// entityExtractionSchema defines the JSON schema for structured output.
// This ensures the model returns valid, parseable JSON matching our ExtractedEntity type.
//
// The root MUST be an object: OpenAI-style structured output rejects array
// roots at request time (before any response is generated), which would kill
// extraction entirely on providers that enforce it. Strict mode also requires
// additionalProperties:false at every level and every property in required
// (optional fields expressed as nullable types).
var entityExtractionSchema = &core.JSONSchemaDefinition{
	Name:        "entity_extraction",
	Description: "Array of extracted entities from text",
	Strict:      true,
	Schema: json.RawMessage(`{
		"type": "object",
		"properties": {
			"entities": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"name": {
							"type": "string",
							"description": "The canonical name of the entity"
						},
					"type": {
						"type": "string",
						"enum": ["person", "organization", "product", "location", "concept", "event", "other"],
						"description": "The type of entity"
					},
						"aliases": {
							"type": ["array", "null"],
							"items": {"type": "string"},
							"description": "Alternative names or abbreviations"
						},
						"attributes": {
							"type": ["object", "null"],
							"additionalProperties": {"type": "string"},
							"description": "Key facts about the entity"
						},
						"confidence": {
							"type": "number",
							"minimum": 0,
							"maximum": 1,
							"description": "Confidence score from 0.0 to 1.0"
						}
					},
					"required": ["name", "type", "aliases", "attributes", "confidence"],
					"additionalProperties": false
				}
			}
		},
		"required": ["entities"],
		"additionalProperties": false
	}`),
}

// ExtractedEntity and ExtractedRelationship alias the canonical types in
// pkg/types (see types.ExtractedEntity). The definitions were consolidated
// there so the public API and the extraction pipeline share one wire shape
// instead of drifting apart; the aliases keep the internal call sites
// unchanged.
type ExtractedEntity = types.ExtractedEntity

type ExtractedRelationship = types.ExtractedRelationship

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
// Uses structured output (JSON Schema) when supported by the provider for reliable parsing.
func (e *Extractor) Extract(ctx context.Context, text string) (*ExtractionResult, error) {
	if strings.TrimSpace(text) == "" {
		return &ExtractionResult{Entities: []ExtractedEntity{}}, nil
	}

	// Build request with structured output for reliable JSON parsing
	builder := e.client.Chat(e.model).
		User(ExtractionPrompt + "\n\n" + text).
		ResponseJSONSchema(entityExtractionSchema)

	if e.maxTokens > 0 {
		builder = builder.MaxTokens(e.maxTokens)
	}

	resp, err := builder.GetResponse(ctx)
	if err != nil {
		// Surface the typed classification (kind, status, code,
		// request_id) for alerting; the queue's dead-lettering keys off
		// the same taxonomy. Retry itself is handled inside iris's
		// core.Client.
		llm.LogProviderError(ctx, "entity_extraction", err)
		return nil, fmt.Errorf("extraction completion failed: %w", err)
	}

	// Parse the structured JSON response. The schema wraps entities in an
	// object root; fall back to legacy array parsing for providers that
	// ignore the schema root.
	entities, err := parseStructuredEntities(resp.Output)
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

// parseStructuredEntities parses the LLM response, handling both the
// object-wrapped form ({"entities": [...]}, matching entityExtractionSchema)
// and the raw array form ([...]) returned by providers that ignore the
// schema root.
func parseStructuredEntities(content string) ([]ExtractedEntity, error) {
	return parseExtractionResponse(content)
}

// parseExtractionResponse parses the LLM response into extracted entities.
// This is kept as a fallback for providers that don't fully support structured
// output. It is robust to surrounding prose: rather than slicing from the
// first '[' to the last ']' (which is corrupted by brackets in prose or
// inside attribute values), it scans for balanced, string-aware JSON spans
// and returns the first one that parses as entities.
func parseExtractionResponse(content string) ([]ExtractedEntity, error) {
	content = strings.TrimSpace(content)
	content = stripMarkdownFence(content)

	// Fast path: the whole content is the JSON.
	if entities, err := tryParseEntities(content); err == nil {
		return entities, nil
	}

	// Scan for a balanced JSON span embedded in prose.
	for i := 0; i < len(content); i++ {
		if content[i] != '[' && content[i] != '{' {
			continue
		}
		end := scanBalanced(content, i)
		if end < 0 {
			continue
		}
		if entities, err := tryParseEntities(content[i:end]); err == nil {
			return entities, nil
		}
		// Not the span we want (e.g. "[listed below]" in prose or an
		// unrelated JSON value) — keep scanning.
	}

	return nil, fmt.Errorf("no valid JSON entities found in response")
}

// stripMarkdownFence removes a leading ``` / ```lang fence line and its
// closing fence, if the content is fenced. Unlike a naive first-newline to
// last-``` slice, it only strips at line boundaries so fences inside the
// JSON payload are left untouched.
func stripMarkdownFence(content string) string {
	if !strings.HasPrefix(content, "```") {
		return content
	}
	nl := strings.Index(content, "\n")
	if nl < 0 {
		return content
	}
	content = content[nl+1:]
	if idx := strings.LastIndex(content, "\n```"); idx >= 0 {
		content = content[:idx]
	} else {
		content = strings.TrimSuffix(content, "```")
	}
	return strings.TrimSpace(content)
}

// scanBalanced returns the exclusive end index of the JSON value opening at
// content[start], or -1 if no balanced value completes. The scanner is
// string-aware: brackets inside JSON string literals and escape sequences
// are skipped, so an attribute value containing ']' cannot truncate the span.
func scanBalanced(content string, start int) int {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(content); i++ {
		c := content[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// tryParseEntities attempts to decode s as extracted entities, accepting the
// object-wrapped form ({"entities": [...]}), the raw array form ([...]),
// or a single entity object ({"name": ...}).
func tryParseEntities(s string) ([]ExtractedEntity, error) {
	var wrapper struct {
		Entities []ExtractedEntity `json:"entities"`
	}
	if err := json.Unmarshal([]byte(s), &wrapper); err == nil && wrapper.Entities != nil {
		return wrapper.Entities, nil
	}

	var entities []ExtractedEntity
	if err := json.Unmarshal([]byte(s), &entities); err == nil {
		return entities, nil
	}

	var single ExtractedEntity
	if err := json.Unmarshal([]byte(s), &single); err == nil && single.Name != "" {
		return []ExtractedEntity{single}, nil
	}

	return nil, fmt.Errorf("not parseable as entities")
}

// validateExtractedEntity checks if an extracted entity is valid.
func validateExtractedEntity(ent *ExtractedEntity) error {
	if strings.TrimSpace(ent.Name) == "" {
		return fmt.Errorf("entity name is empty")
	}
	if !isValidEntityType(string(ent.Type)) {
		return fmt.Errorf("invalid entity type: %s", ent.Type)
	}
	return nil
}

// normalizeExtractedEntity normalizes entity fields.
func normalizeExtractedEntity(ent *ExtractedEntity) {
	ent.Name = strings.TrimSpace(ent.Name)
	ent.Type = types.EntityType(strings.ToLower(strings.TrimSpace(string(ent.Type))))

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
// Uses the engine's ValidEntityTypes as the single source of truth.
func isValidEntityType(t string) bool {
	t = strings.ToLower(strings.TrimSpace(t))
	return ValidEntityTypes[types.EntityType(t)]
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
	case "event":
		return types.EntityTypeEvent
	case "other":
		return types.EntityTypeOther
	default:
		return types.EntityTypeConcept // Unreachable for validated input
	}
}
