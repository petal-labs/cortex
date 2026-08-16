package dto

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/pkg/types"
)

// TestEntityCollectionsNotNull verifies nil Aliases/Attributes from storage
// marshal as [] and {} — never null — so clients can iterate directly
// (entity.aliases.map(...) etc.) without throwing.
func TestEntityCollectionsNotNull(t *testing.T) {
	ent := &types.Entity{
		ID: "ent-1", Namespace: "ns", Name: "Bare", Type: types.EntityTypePerson,
		// Aliases and Attributes deliberately nil.
	}

	for name, marshal := range map[string]func() any{
		"entity_update": func() any { return NewEntityUpdate(ent) },
		"entity_query":  func() any { return NewEntityQuery(&types.EntityQueryResponse{Entity: ent, Found: true}) },
		"entity_list": func() any {
			return NewEntityList(&entity.ListResult{Entities: []*types.Entity{ent}, Count: 1})
		},
		"entity_merge": func() any {
			return NewEntityMerge(&entity.MergeResult{KeptEntity: ent})
		},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(marshal())
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			s := string(data)
			if !strings.Contains(s, `"aliases":[]`) {
				t.Errorf("expected aliases to be [], got: %s", s)
			}
			if !strings.Contains(s, `"attributes":{}`) {
				t.Errorf("expected attributes to be {}, got: %s", s)
			}
			if bytes.Contains(data, []byte("null")) {
				t.Errorf("unexpected null in response: %s", s)
			}
		})
	}
}

// TestContextListKeysNotNull verifies an empty context list emits [] for
// keys rather than null.
func TestContextListKeysNotNull(t *testing.T) {
	data, err := json.Marshal(NewContextList(&ctxengine.ListResult{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"keys":[]`) {
		t.Errorf("expected keys to be [], got: %s", s)
	}
	if strings.Contains(s, "null") {
		t.Errorf("unexpected null in response: %s", s)
	}
}

// TestContextListNilResult verifies the nil-result guard still emits [].
func TestContextListNilResult(t *testing.T) {
	data, err := json.Marshal(NewContextList(nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"keys":[]`) {
		t.Errorf("expected keys to be [], got: %s", data)
	}
}

// TestNilResultMappersEmitEmptyCollections verifies every constructor's
// nil-result guard emits empty collections rather than null for its
// non-omitempty slice/map fields.
func TestNilResultMappersEmitEmptyCollections(t *testing.T) {
	cases := map[string]func() any{
		"conversation_history":  func() any { return NewConversationHistory(nil) },
		"conversation_search":   func() any { return NewConversationSearch(nil) },
		"context_history":       func() any { return NewContextHistory(nil) },
		"context_list":          func() any { return NewContextList(nil) },
		"knowledge_search":      func() any { return NewKnowledgeSearch(nil) },
		"knowledge_bulk_ingest": func() any { return NewKnowledgeBulkIngest(nil) },
		"entity_search":         func() any { return NewEntitySearch(nil) },
		"entity_list":           func() any { return NewEntityList(nil) },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(fn())
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(data), "null") {
				t.Errorf("nil-result response contains null: %s", data)
			}
		})
	}
}

// TestNoNullCollectionsAcrossGoldenShapes recursively scans every golden
// case for null values, guarding against future DTO fields reintroducing
// the null-collections bug. Scalar any-typed fields (context values) are
// exempt: a null value there is legitimate data, not a collection shape.
func TestNoNullCollectionsAcrossGoldenShapes(t *testing.T) {
	// Fields where null is semantically valid (raw context values, nullable
	// payload fields), not a collection-shape bug.
	allowedNullPaths := map[string]bool{
		"context_get.value":          true,
		"context_merge.merged_value": true,
	}

	var check func(v any, path string) error
	check = func(v any, path string) error {
		switch val := v.(type) {
		case map[string]any:
			for k, child := range val {
				childPath := path + "." + k
				if child == nil {
					if !allowedNullPaths[childPath] {
						return errNullAt{childPath}
					}
					continue
				}
				if err := check(child, childPath); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range val {
				if child == nil {
					return errNullAt{path}
				}
				if err := check(child, path); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for name, v := range goldenCases() {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var decoded any
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if err := check(decoded, name); err != nil {
				t.Error(err)
			}
		})
	}
}

type errNullAt struct{ path string }

func (e errNullAt) Error() string {
	return "unexpected null at " + e.path + " (collections must marshal as []/{}, not null)"
}

// TestSearchResultsStripEmbeddings verifies that chunks carrying populated
// embedding vectors (as returned by storage search) never serialize them
// into search responses — a top_k:20 result with 1536-dim vectors would
// otherwise emit ~30k floats of token-bloating noise.
func TestSearchResultsStripEmbeddings(t *testing.T) {
	vec := make([]float32, 1536)
	for i := range vec {
		vec[i] = float32(i) * 0.001
	}
	result := NewKnowledgeSearch(&knowledge.SearchResult{
		Results: []*types.ChunkResult{
			{
				Chunk: &types.Chunk{
					ID: "chunk-1", DocumentID: "doc-1", Namespace: "ns", CollectionID: "col-1",
					Content: "content", Index: 0, Embedding: vec,
				},
				Score: 0.9, DocumentTitle: "Doc",
			},
		},
		Query: "q", TotalFound: 1,
	})

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if strings.Contains(s, "embedding") {
		t.Error("embedding leaked into search response")
	}
	// Spot-check the float noise is absent (first/last vector values as decimals).
	if strings.Contains(s, "0.001") || strings.Contains(s, "1.535") {
		t.Error("vector floats leaked into search response")
	}
	// The useful chunk fields remain.
	for _, want := range []string{`"id":"chunk-1"`, `"content":"content"`, `"score":0.9`} {
		if !strings.Contains(s, want) {
			t.Errorf("expected %s in response, got: %s", want, s)
		}
	}
}

// TestNoZeroTimestampsAcrossGoldenShapes verifies no DTO response carries a
// zero time.Time, which serializes as the misleading
// "0001-01-01T00:00:00Z". Timestamps on the wire must be either populated
// or omitted — never the zero value.
func TestNoZeroTimestampsAcrossGoldenShapes(t *testing.T) {
	for name, v := range goldenCases() {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(data), "0001-01-01") {
				t.Errorf("zero-value timestamp leaked into %s response: %s", name, data)
			}
		})
	}
}
