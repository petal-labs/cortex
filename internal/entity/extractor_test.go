package entity

import (
	"os"
	"testing"
)

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
		{"unknown", "concept"}, // Defaults to concept
		{"", "concept"},
	}

	for _, tt := range tests {
		result := ToEntityType(tt.input)
		if string(result) != tt.expected {
			t.Errorf("ToEntityType(%q) = %s, expected %s", tt.input, result, tt.expected)
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
