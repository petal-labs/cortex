package entity

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/petal-labs/cortex/pkg/types"
)

// TestEntityExtractionSchemaObjectRoot verifies the schema uses an object
// root (not an array), which OpenAI-style structured output requires — an
// array root is rejected at request time, silently killing extraction.
func TestEntityExtractionSchemaObjectRoot(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(entityExtractionSchema.Schema, &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}

	if schema["type"] != "object" {
		t.Errorf("expected schema root type 'object', got %v", schema["type"])
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected schema root to have properties")
	}
	if _, ok := props["entities"]; !ok {
		t.Error("expected schema to have an 'entities' property")
	}
	if entities, ok := props["entities"].(map[string]any); ok {
		if entities["type"] != "array" {
			t.Errorf("expected 'entities' to be an array, got %v", entities["type"])
		}
	}

	required, ok := schema["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "entities" {
		t.Errorf("expected required: [\"entities\"], got %v", schema["required"])
	}

	if ap, ok := schema["additionalProperties"].(bool); !ok || ap {
		t.Error("expected additionalProperties: false at schema root")
	}
}

func TestParseStructuredEntities(t *testing.T) {
	t.Run("parses object-wrapped response", func(t *testing.T) {
		response := `{"entities": [
			{"name": "Acme Corp", "type": "organization", "confidence": 0.9},
			{"name": "Jane", "type": "person", "confidence": 0.8}
		]}`

		entities, err := parseStructuredEntities(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entities) != 2 {
			t.Fatalf("expected 2 entities, got %d", len(entities))
		}
		if entities[0].Name != "Acme Corp" {
			t.Errorf("expected 'Acme Corp', got %q", entities[0].Name)
		}
	})

	t.Run("falls back to raw array response", func(t *testing.T) {
		response := `[
			{"name": "Acme Corp", "type": "organization", "confidence": 0.9}
		]`

		entities, err := parseStructuredEntities(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entities) != 1 {
			t.Fatalf("expected 1 entity, got %d", len(entities))
		}
	})

	t.Run("falls back when entities is null", func(t *testing.T) {
		response := `{"entities": null, "note": "model returned malformed object"}`
		// The fallback parser finds no array and no valid single entity.
		if _, err := parseStructuredEntities(response); err == nil {
			t.Error("expected error for unparseable response, got nil")
		}
	})

	t.Run("errors on garbage", func(t *testing.T) {
		if _, err := parseStructuredEntities("not json at all"); err == nil {
			t.Error("expected error for non-JSON response, got nil")
		}
	})

	t.Run("markdown code block with array", func(t *testing.T) {
		response := "```json\n[{\"name\": \"X Corp\", \"type\": \"organization\", \"confidence\": 0.9}]\n```"
		entities, err := parseStructuredEntities(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entities) != 1 || entities[0].Name != "X Corp" {
			t.Errorf("expected fallback parser to handle markdown, got %v", entities)
		}
	})
}

func TestParseExtractionResponse(t *testing.T) {
	t.Run("parses valid JSON array", func(t *testing.T) {
		response := `[
			{
				"name": "Acme Corp",
				"type": "organization",
				"aliases": ["Acme", "ACME Corporation"],
				"attributes": {"industry": "tech"},
				"confidence": 0.9
			},
			{
				"name": "John Doe",
				"type": "person",
				"confidence": 0.85
			}
		]`

		entities, err := parseExtractionResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(entities) != 2 {
			t.Errorf("expected 2 entities, got %d", len(entities))
		}

		if entities[0].Name != "Acme Corp" {
			t.Errorf("expected name 'Acme Corp', got %s", entities[0].Name)
		}

		if len(entities[0].Aliases) != 2 {
			t.Errorf("expected 2 aliases, got %d", len(entities[0].Aliases))
		}
	})

	t.Run("parses JSON with markdown code block", func(t *testing.T) {
		response := "```json\n[{\"name\": \"Test Entity\", \"type\": \"concept\", \"confidence\": 0.8}]\n```"

		entities, err := parseExtractionResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(entities) != 1 {
			t.Errorf("expected 1 entity, got %d", len(entities))
		}

		if entities[0].Name != "Test Entity" {
			t.Errorf("expected name 'Test Entity', got %s", entities[0].Name)
		}
	})

	t.Run("parses JSON with surrounding text", func(t *testing.T) {
		response := "Here are the extracted entities:\n[{\"name\": \"Test\", \"type\": \"location\", \"confidence\": 0.7}]\nEnd of entities."

		entities, err := parseExtractionResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(entities) != 1 {
			t.Errorf("expected 1 entity, got %d", len(entities))
		}
	})

	t.Run("ignores brackets in prose before the JSON array", func(t *testing.T) {
		// The old first-'['-to-last-']' slice started at "[listed below]"
		// and produced unparseable output.
		response := "entities [listed below]: [{\"name\": \"Acme Corp\", \"type\": \"organization\", \"confidence\": 0.9}]"

		entities, err := parseExtractionResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entities) != 1 {
			t.Fatalf("expected 1 entity, got %d", len(entities))
		}
		if entities[0].Name != "Acme Corp" {
			t.Errorf("expected 'Acme Corp', got %q", entities[0].Name)
		}
	})

	t.Run("handles closing bracket inside attribute value", func(t *testing.T) {
		response := "[{\"name\": \"Acme Corp\", \"type\": \"organization\", \"attributes\": {\"note\": \"see appendix [3] for details\"}, \"confidence\": 0.9}]"

		entities, err := parseExtractionResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entities) != 1 {
			t.Fatalf("expected 1 entity, got %d", len(entities))
		}
		if entities[0].Attributes["note"] != "see appendix [3] for details" {
			t.Errorf("attribute value corrupted: %q", entities[0].Attributes["note"])
		}
	})

	t.Run("handles brackets in trailing prose after the array", func(t *testing.T) {
		response := "[{\"name\": \"Acme Corp\", \"type\": \"organization\", \"confidence\": 0.9}]\n\nNote: other models [GPT-4] may differ."

		entities, err := parseExtractionResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entities) != 1 {
			t.Fatalf("expected 1 entity, got %d", len(entities))
		}
		if entities[0].Name != "Acme Corp" {
			t.Errorf("expected 'Acme Corp', got %q", entities[0].Name)
		}
	})

	t.Run("skips unrelated JSON values in prose", func(t *testing.T) {
		response := "Confidence bands [0.2, 0.5] were considered.\nEntities: [{\"name\": \"Jane\", \"type\": \"person\", \"confidence\": 0.8}]"

		entities, err := parseExtractionResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entities) != 1 {
			t.Fatalf("expected 1 entity, got %d", len(entities))
		}
		if entities[0].Name != "Jane" {
			t.Errorf("expected 'Jane', got %q", entities[0].Name)
		}
	})

	t.Run("handles escaped quotes containing brackets", func(t *testing.T) {
		response := "[{\"name\": \"Quote \\\" [weird] \\\" Corp\", \"type\": \"organization\", \"confidence\": 0.9}]"

		entities, err := parseExtractionResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entities) != 1 {
			t.Fatalf("expected 1 entity, got %d", len(entities))
		}
		if !strings.Contains(entities[0].Name, "[weird]") {
			t.Errorf("expected name to contain '[weird]', got %q", entities[0].Name)
		}
	})

	t.Run("finds object-wrapped entities embedded in prose", func(t *testing.T) {
		response := "Sure! Here is the result:\n{\"entities\": [{\"name\": \"Acme Corp\", \"type\": \"organization\", \"confidence\": 0.9}]}\nHope that helps."

		entities, err := parseExtractionResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(entities) != 1 {
			t.Fatalf("expected 1 entity, got %d", len(entities))
		}
		if entities[0].Name != "Acme Corp" {
			t.Errorf("expected 'Acme Corp', got %q", entities[0].Name)
		}
	})

	t.Run("parses empty array", func(t *testing.T) {
		response := "[]"

		entities, err := parseExtractionResponse(response)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(entities) != 0 {
			t.Errorf("expected 0 entities, got %d", len(entities))
		}
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		response := "this is not json"

		_, err := parseExtractionResponse(response)
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}

func TestValidateExtractedEntity(t *testing.T) {
	t.Run("validates valid entity", func(t *testing.T) {
		ent := &ExtractedEntity{
			Name:       "Test Entity",
			Type:       "person",
			Confidence: 0.9,
		}

		err := validateExtractedEntity(ent)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("rejects empty name", func(t *testing.T) {
		ent := &ExtractedEntity{
			Name:       "",
			Type:       "person",
			Confidence: 0.9,
		}

		err := validateExtractedEntity(ent)
		if err == nil {
			t.Error("expected error for empty name")
		}
	})

	t.Run("rejects invalid type", func(t *testing.T) {
		ent := &ExtractedEntity{
			Name:       "Test",
			Type:       "invalid_type",
			Confidence: 0.9,
		}

		err := validateExtractedEntity(ent)
		if err == nil {
			t.Error("expected error for invalid type")
		}
	})
}

func TestNormalizeExtractedEntity(t *testing.T) {
	t.Run("normalizes entity fields", func(t *testing.T) {
		ent := &ExtractedEntity{
			Name:       "  Test Entity  ",
			Type:       "PERSON",
			Aliases:    []string{"  Test  ", "Test Entity", "Test"}, // Duplicate and whitespace
			Confidence: 1.5,                                         // Out of range
		}

		normalizeExtractedEntity(ent)

		if ent.Name != "Test Entity" {
			t.Errorf("expected trimmed name, got '%s'", ent.Name)
		}

		if ent.Type != "person" {
			t.Errorf("expected lowercase type, got '%s'", ent.Type)
		}

		// Should remove duplicate "Test" and the one matching name
		if len(ent.Aliases) != 1 || ent.Aliases[0] != "Test" {
			t.Errorf("expected 1 unique alias 'Test', got %v", ent.Aliases)
		}

		if ent.Confidence != 1 {
			t.Errorf("expected clamped confidence 1, got %f", ent.Confidence)
		}
	})

	t.Run("initializes nil attributes", func(t *testing.T) {
		ent := &ExtractedEntity{
			Name: "Test",
			Type: "concept",
		}

		normalizeExtractedEntity(ent)

		if ent.Attributes == nil {
			t.Error("expected attributes to be initialized")
		}
	})
}

func TestExtractRelationships(t *testing.T) {
	t.Run("creates relationships for co-mentioned entities", func(t *testing.T) {
		entities := []ExtractedEntity{
			{Name: "Entity A", Type: "person"},
			{Name: "Entity B", Type: "organization"},
			{Name: "Entity C", Type: "location"},
		}

		relationships := extractRelationships(entities, "sample text")

		// 3 entities = 3 relationships (A-B, A-C, B-C)
		if len(relationships) != 3 {
			t.Errorf("expected 3 relationships, got %d", len(relationships))
		}

		// All should be "related_to" type with 0.5 confidence
		for _, rel := range relationships {
			if rel.RelationType != "related_to" {
				t.Errorf("expected relation_type 'related_to', got '%s'", rel.RelationType)
			}
			if rel.Confidence != 0.5 {
				t.Errorf("expected confidence 0.5, got %f", rel.Confidence)
			}
		}
	})

	t.Run("returns nil for single entity", func(t *testing.T) {
		entities := []ExtractedEntity{
			{Name: "Entity A", Type: "person"},
		}

		relationships := extractRelationships(entities, "sample text")

		if relationships != nil {
			t.Errorf("expected nil relationships for single entity, got %v", relationships)
		}
	})

	t.Run("returns nil for empty entities", func(t *testing.T) {
		relationships := extractRelationships([]ExtractedEntity{}, "sample text")

		if relationships != nil {
			t.Errorf("expected nil relationships for empty entities")
		}
	})
}

func TestToEntityType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"person", "person"},
		{"PERSON", "person"},
		{"organization", "organization"},
		{"ORGANIZATION", "organization"},
		{"product", "product"},
		{"location", "location"},
		{"concept", "concept"},
		{"event", "event"},
		{"other", "other"},
		{"unknown", "concept"}, // Unreachable for validated input
		{"", "concept"},
	}

	for _, tt := range tests {
		result := ToEntityType(tt.input)
		if string(result) != tt.expected {
			t.Errorf("ToEntityType(%q) = %s, expected %s", tt.input, result, tt.expected)
		}
	}
}

// TestIsValidEntityTypeEventOther verifies extraction validation accepts the
// full advertised type set (single source of truth: engine ValidEntityTypes).
func TestIsValidEntityTypeEventOther(t *testing.T) {
	for _, valid := range []string{"person", "organization", "product", "location", "concept", "event", "other", "Event", " OTHER "} {
		if !isValidEntityType(valid) {
			t.Errorf("expected %q to be valid", valid)
		}
	}
	for _, invalid := range []string{"", "bogus", "company"} {
		if isValidEntityType(invalid) {
			t.Errorf("expected %q to be invalid", invalid)
		}
	}
}

// Integration tests - require API key to run

func TestExtractorExtract_Integration(t *testing.T) {
	// Skip if no API key - this is an integration test
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY or OPENAI_API_KEY not set, skipping integration test")
	}

	// This test would require a real LLM provider, so we skip it by default
	t.Skip("Integration test requires real LLM provider - run manually with API key")
}

func TestExtractorExtractEmpty(t *testing.T) {
	// Test that empty text returns empty result without needing LLM
	// This works because the extractor short-circuits on empty input

	// We can't create an Extractor without a valid provider config,
	// but this test verifies the behavior documented in the code
	t.Skip("Requires provider configuration - empty input handling tested via Extract method")
}

// TestExtractedEntityCanonicalShape locks the wire shape of the canonical
// types.ExtractedEntity/ExtractedRelationship (consolidated in P26): it must
// match the extraction structured-output schema — name, type, aliases,
// attributes, confidence — with confidence always emitted (schema-required)
// and relationships carrying source_name/target_name. Any drift here breaks
// LLM response parsing, so it fails loudly instead.
func TestExtractedEntityCanonicalShape(t *testing.T) {
	// Round-trip a schema-shaped LLM response through the canonical type.
	schemaShaped := `{
		"name": "Acme Corp",
		"type": "organization",
		"aliases": ["Acme"],
		"attributes": {"industry": "tech"},
		"confidence": 0.9
	}`
	var ent ExtractedEntity
	if err := json.Unmarshal([]byte(schemaShaped), &ent); err != nil {
		t.Fatalf("schema-shaped JSON must parse into the canonical type: %v", err)
	}
	if ent.Name != "Acme Corp" || string(ent.Type) != "organization" || ent.Confidence != 0.9 {
		t.Errorf("unexpected parse: %+v", ent)
	}

	// Confidence must always be emitted (extraction schema marks it
	// required), never omitted.
	data, err := json.Marshal(&ExtractedEntity{Name: "X", Type: "person"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"confidence":0`) {
		t.Errorf("confidence dropped from wire shape: %s", data)
	}

	// Aliases/attributes stay omitempty (optional in the schema).
	if strings.Contains(string(data), "aliases") || strings.Contains(string(data), "attributes") {
		t.Errorf("optional fields should be omitted when empty: %s", data)
	}

	// Relationship shape: source_name and target_name both present.
	relData, err := json.Marshal(&ExtractedRelationship{
		SourceName: "a", TargetName: "b", RelationType: "works_at", Confidence: 0.5,
	})
	if err != nil {
		t.Fatalf("marshal relationship: %v", err)
	}
	for _, want := range []string{`"source_name":"a"`, `"target_name":"b"`, `"relation_type":"works_at"`, `"confidence":0.5`} {
		if !strings.Contains(string(relData), want) {
			t.Errorf("expected %s in relationship wire shape, got: %s", want, relData)
		}
	}

	// The aliases must be the pkg/types definitions — one canonical type,
	// not a duplicate. This assignment only compiles while ExtractedEntity
	// remains an alias of types.ExtractedEntity.
	var typeCheck = types.ExtractedEntity{}
	var _ ExtractedEntity = typeCheck
}
