package entity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petal-labs/cortex/internal/config"
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
			Confidence: 1.5, // Out of range
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

func TestExtractorExtract(t *testing.T) {
	t.Run("extracts entities from text", func(t *testing.T) {
		// Create mock server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/completions" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}

			response := CompletionResponse{
				Content: `[{"name": "Google", "type": "organization", "aliases": ["Alphabet"], "confidence": 0.95}]`,
			}
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := config.DefaultConfig()
		cfg.Iris.Endpoint = server.URL
		cfg.Entity.ExtractionModel = "test-model"

		extractor := NewExtractor(cfg)

		result, err := extractor.Extract(context.Background(), "Google announced new AI features today.")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Entities) != 1 {
			t.Errorf("expected 1 entity, got %d", len(result.Entities))
		}

		if result.Entities[0].Name != "Google" {
			t.Errorf("expected name 'Google', got %s", result.Entities[0].Name)
		}
	})

	t.Run("returns empty result for empty text", func(t *testing.T) {
		cfg := config.DefaultConfig()
		extractor := NewExtractor(cfg)

		result, err := extractor.Extract(context.Background(), "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(result.Entities) != 0 {
			t.Errorf("expected 0 entities, got %d", len(result.Entities))
		}
	})

	t.Run("handles server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal error"))
		}))
		defer server.Close()

		cfg := config.DefaultConfig()
		cfg.Iris.Endpoint = server.URL

		extractor := NewExtractor(cfg)

		_, err := extractor.Extract(context.Background(), "Some text")
		if err == nil {
			t.Error("expected error for server failure")
		}
	})

	t.Run("filters invalid entities", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Return mix of valid and invalid entities
			response := CompletionResponse{
				Content: `[
					{"name": "Valid Entity", "type": "person", "confidence": 0.9},
					{"name": "", "type": "person", "confidence": 0.9},
					{"name": "Invalid Type", "type": "alien", "confidence": 0.9}
				]`,
			}
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := config.DefaultConfig()
		cfg.Iris.Endpoint = server.URL

		extractor := NewExtractor(cfg)

		result, err := extractor.Extract(context.Background(), "Test text")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Only the valid entity should be returned
		if len(result.Entities) != 1 {
			t.Errorf("expected 1 valid entity, got %d", len(result.Entities))
		}

		if result.Entities[0].Name != "Valid Entity" {
			t.Errorf("expected 'Valid Entity', got '%s'", result.Entities[0].Name)
		}
	})
}
