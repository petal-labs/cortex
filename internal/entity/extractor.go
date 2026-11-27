package entity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/petal-labs/iris/core"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/llm"
	"github.com/petal-labs/cortex/pkg/types"
)

// ExtractionPrompt is the system prompt for entity and relationship extraction.
// With structured output, we don't need to specify the JSON format.
const ExtractionPrompt = `Extract all named entities and typed relationships between them from the following text.

For each entity, identify:
- name: The canonical name of the entity
- type: Classify as "person", "organization", "product", "location", "concept", "event", or "other"
- aliases: Any alternative names or abbreviations used in the text
- summary: A concise one-sentence description of what this entity is based on the text
- attributes: Key facts mentioned about the entity as key-value pairs
- confidence: Your confidence in this extraction (0.0-1.0)

For each relationship explicitly stated in the text between two extracted entities, identify:
- source_name: The canonical name of the source entity (must match an extracted entity name)
- target_name: The canonical name of the target entity (must match an extracted entity name)
- relation_type: A concise lowercase snake_case verb phrase (e.g., "works_at", "manages", "persists_in", "depends_on", "hosted_by", "authored")
- description: A short phrase describing the relationship in context
- confidence: Your confidence in this relationship (0.0-1.0)

If no entities or relationships are found, return empty arrays.

Text:`

// entityExtractionSchema defines the JSON schema for structured output.
// This ensures the model returns valid, parseable JSON matching our
// ExtractedEntity and ExtractedRelationship types.
//
// The root MUST be an object: OpenAI-style structured output rejects array
// roots at request time (before any response is generated), which would kill
// extraction entirely on providers that enforce it. Strict mode also requires
// additionalProperties:false at every level and every property in required
// (optional fields expressed as nullable types).
var entityExtractionSchema = &core.JSONSchemaDefinition{
	Name:        "entity_extraction",
	Description: "Extracted entities and typed directed relationships from text",
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
						"summary": {
							"type": ["string", "null"],
							"description": "Concise one-sentence summary of the entity"
						},
						"attributes": {
							"type": ["array", "null"],
							"items": {
								"type": "object",
								"properties": {
									"key": {
										"type": "string",
										"description": "Attribute name"
									},
									"value": {
										"type": "string",
										"description": "Attribute value"
									}
								},
								"required": ["key", "value"],
								"additionalProperties": false
							},
							"description": "Key facts about the entity as key-value pairs"
						},
						"confidence": {
							"type": "number",
							"minimum": 0,
							"maximum": 1,
							"description": "Confidence score from 0.0 to 1.0"
						}
					},
					"required": ["name", "type", "aliases", "summary", "attributes", "confidence"],
					"additionalProperties": false
				}
			},
			"relationships": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"source_name": {
							"type": "string",
							"description": "Canonical name of the source entity"
						},
						"target_name": {
							"type": "string",
							"description": "Canonical name of the target entity"
						},
						"relation_type": {
							"type": "string",
							"description": "Concise lowercase snake_case relation verb phrase"
						},
						"description": {
							"type": ["string", "null"],
							"description": "Short contextual description of the relationship"
						},
						"confidence": {
							"type": "number",
							"minimum": 0,
							"maximum": 1,
							"description": "Confidence score from 0.0 to 1.0"
						}
					},
					"required": ["source_name", "target_name", "relation_type", "description", "confidence"],
					"additionalProperties": false
				}
			}
		},
		"required": ["entities", "relationships"],
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
	timeout   time.Duration
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
		maxTokens: 2048, // Sufficient for entity + relationship extraction responses
		timeout:   cfg.Entity.ExtractionTimeout,
	}, nil
}

// Extract extracts entities and typed relationships from the given text.
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

	// Explicit timeout opt-in (config entity.extraction_timeout). Without
	// it the call inherits the SDK's implicit default silently; the builder
	// only applies it when the caller supplied no deadline of its own.
	if e.timeout > 0 {
		builder = builder.Timeout(e.timeout)
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

	// Parse structured JSON response containing entities and optional relationships.
	entities, rawRels, err := parseExtractionPayload(resp.Output)
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

	// Validate and normalize LLM-extracted relationships (no O(n^2) co-occurrence clique).
	validRels := make([]ExtractedRelationship, 0, len(rawRels))
	for _, rel := range rawRels {
		if err := validateExtractedRelationship(&rel); err != nil {
			continue
		}
		normalizeExtractedRelationship(&rel)
		validRels = append(validRels, rel)
	}

	return &ExtractionResult{
		Entities:      validEntities,
		Relationships: validRels,
		SourceText:    text,
	}, nil
}

// parseStructuredEntities parses the LLM response, handling both the
// object-wrapped form ({"entities": [...]}, matching entityExtractionSchema)
// and the raw array form ([...]) returned by providers that ignore the
// schema root.
func parseStructuredEntities(content string) ([]ExtractedEntity, error) {
	entities, _, err := parseExtractionPayload(content)
	return entities, err
}

// parseExtractionPayload parses both entities and typed relationships from the
// LLM response, falling back cleanly when a provider returns a legacy entity
// array or embeds JSON inside prose/markdown fences.
func parseExtractionPayload(content string) ([]ExtractedEntity, []ExtractedRelationship, error) {
	content = strings.TrimSpace(content)
	content = stripMarkdownFence(content)

	if entities, rels, err := tryParsePayload(content); err == nil {
		return entities, rels, nil
	}

	for i := 0; i < len(content); i++ {
		if content[i] != '[' && content[i] != '{' {
			continue
		}
		end := scanBalanced(content, i)
		if end < 0 {
			continue
		}
		if entities, rels, err := tryParsePayload(content[i:end]); err == nil {
			return entities, rels, nil
		}
	}

	return nil, nil, fmt.Errorf("no valid JSON entities found in response")
}

// flexAttributes unmarshals entity attributes from either the strict
// structured-output array form ([{"key": "k", "value": "v"}]), null, or
// the legacy object map form ({"k": "v"}).
type flexAttributes map[string]string

func (f *flexAttributes) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*f = nil
		return nil
	}

	if trimmed[0] == '[' {
		var pairs []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(data, &pairs); err != nil {
			return err
		}
		attrs := make(map[string]string, len(pairs))
		for _, p := range pairs {
			k := strings.TrimSpace(p.Key)
			if k == "" {
				continue
			}
			attrs[k] = p.Value
		}
		*f = attrs
		return nil
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	*f = m
	return nil
}

type wireExtractedEntity struct {
	Name       string           `json:"name"`
	Type       types.EntityType `json:"type"`
	Aliases    []string         `json:"aliases,omitempty"`
	Summary    string           `json:"summary,omitempty"`
	Attributes flexAttributes   `json:"attributes,omitempty"`
	Confidence float64          `json:"confidence"`
}

func (w wireExtractedEntity) toExtractedEntity() ExtractedEntity {
	return ExtractedEntity{
		Name:       w.Name,
		Type:       w.Type,
		Aliases:    w.Aliases,
		Summary:    w.Summary,
		Attributes: map[string]string(w.Attributes),
		Confidence: w.Confidence,
	}
}

func convertWireEntities(wire []wireExtractedEntity) []ExtractedEntity {
	out := make([]ExtractedEntity, len(wire))
	for i, w := range wire {
		out[i] = w.toExtractedEntity()
	}
	return out
}

func tryParsePayload(s string) ([]ExtractedEntity, []ExtractedRelationship, error) {
	var wrapper struct {
		Entities      []wireExtractedEntity   `json:"entities"`
		Relationships []ExtractedRelationship `json:"relationships"`
	}
	if err := json.Unmarshal([]byte(s), &wrapper); err == nil && wrapper.Entities != nil {
		return convertWireEntities(wrapper.Entities), wrapper.Relationships, nil
	}

	entities, err := tryParseEntities(s)
	if err != nil {
		return nil, nil, err
	}
	return entities, nil, nil
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
		Entities []wireExtractedEntity `json:"entities"`
	}
	if err := json.Unmarshal([]byte(s), &wrapper); err == nil && wrapper.Entities != nil {
		return convertWireEntities(wrapper.Entities), nil
	}

	var entities []wireExtractedEntity
	if err := json.Unmarshal([]byte(s), &entities); err == nil {
		return convertWireEntities(entities), nil
	}

	var single wireExtractedEntity
	if err := json.Unmarshal([]byte(s), &single); err == nil && single.Name != "" {
		return []ExtractedEntity{single.toExtractedEntity()}, nil
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

	ent.Summary = strings.TrimSpace(ent.Summary)

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

func validateExtractedRelationship(rel *ExtractedRelationship) error {
	src := strings.TrimSpace(rel.SourceName)
	tgt := strings.TrimSpace(rel.TargetName)
	if src == "" || tgt == "" {
		return fmt.Errorf("relationship endpoints cannot be empty")
	}
	if strings.EqualFold(src, tgt) {
		return fmt.Errorf("self-referential relationship ignored")
	}
	if strings.TrimSpace(rel.RelationType) == "" {
		return fmt.Errorf("relation_type cannot be empty")
	}
	return nil
}

func normalizeExtractedRelationship(rel *ExtractedRelationship) {
	rel.SourceName = strings.TrimSpace(rel.SourceName)
	rel.TargetName = strings.TrimSpace(rel.TargetName)
	rel.Description = strings.TrimSpace(rel.Description)

	rt := strings.ToLower(strings.TrimSpace(rel.RelationType))
	rt = strings.ReplaceAll(rt, " ", "_")
	rt = strings.ReplaceAll(rt, "-", "_")
	for strings.Contains(rt, "__") {
		rt = strings.ReplaceAll(rt, "__", "_")
	}
	rel.RelationType = strings.Trim(rt, "_")
	if rel.RelationType == "" {
		rel.RelationType = "related_to"
	}

	if rel.Confidence <= 0 {
		rel.Confidence = 0.7
	} else if rel.Confidence > 1 {
		rel.Confidence = 1
	}
}

// isValidEntityType checks if a type string is a valid entity type.
// Uses the engine's ValidEntityTypes as the single source of truth.
func isValidEntityType(t string) bool {
	t = strings.ToLower(strings.TrimSpace(t))
	return ValidEntityTypes[types.EntityType(t)]
}

// extractRelationships creates relationships between co-mentioned entities.
// Kept for backwards-compatible unit testing; Extract() now uses LLM-extracted
// typed relationships directly instead of O(n^2) co-occurrence cliques.
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
