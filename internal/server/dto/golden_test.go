package dto

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixed times so goldens are deterministic.
var (
	ts1 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ts2 = time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC)
)

// goldenCases locks the wire format of every top-level response DTO. Run
// `go test ./internal/server/dto -update` only when intentionally changing
// the contract — that diff is the breaking-change review.
func goldenCases() map[string]any {
	return map[string]any{
		"conversation_append":          NewConversationAppend(&messageFixture),
		"conversation_history":         NewConversationHistory(conversationHistoryInput),
		"conversation_search":          NewConversationSearch(searchInput),
		"conversation_summarize":       NewConversationSummarize(summarizeInput),
		"knowledge_ingest":             NewKnowledgeIngest(ingestInput),
		"knowledge_search":             NewKnowledgeSearch(knowledgeSearchInput),
		"knowledge_collections_list":   NewKnowledgeCollectionList(collectionListInput, ""),
		"knowledge_collections_create": NewKnowledgeCollectionCreated(collectionFixture),
		"knowledge_collections_delete": NewKnowledgeCollectionDeleted(),
		"knowledge_bulk_ingest":        NewKnowledgeBulkIngest(bulkIngestInput),
		"context_get":                  NewContextGet(contextGetInput),
		"context_set":                  NewContextSet(contextSetInput),
		"context_merge":                NewContextMerge(contextMergeInput),
		"context_list":                 NewContextList(contextListInput),
		"context_history":              NewContextHistory(contextHistoryInput),
		"entity_query":                 NewEntityQuery(entityQueryInput),
		"entity_search":                NewEntitySearch(entitySearchInput),
		"entity_relationships":         NewEntityRelationships(relationshipListInput),
		"entity_update":                NewEntityUpdate(entityPtrFixture),
		"entity_merge":                 NewEntityMerge(entityMergeInput),
		"entity_list":                  NewEntityList(entityListInput),
	}
}

func TestGoldenContracts(t *testing.T) {
	for name, v := range goldenCases() {
		t.Run(name, func(t *testing.T) {
			data, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			data = append(data, '\n')

			goldenPath := filepath.Join("testdata", name+".golden.json")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(goldenPath, data, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (run `go test ./internal/server/dto -update` to create): %v", err)
			}
			if string(want) != string(data) {
				t.Errorf("wire contract changed for %s.\nIf intentional: `go test ./internal/server/dto -update`, review the diff, and bump SchemaVersion if breaking.\n--- golden ---\n%s--- current ---\n%s",
					name, want, data)
			}
		})
	}
}

// TestContractVersionStamped verifies every golden response carries the
// current schema_version at the top level.
func TestContractVersionStamped(t *testing.T) {
	for name, v := range goldenCases() {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		var probe struct {
			SchemaVersion int `json:"schema_version"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			t.Fatalf("unmarshal %s: %v", name, err)
		}
		if probe.SchemaVersion != SchemaVersion {
			t.Errorf("%s: schema_version = %d, want %d", name, probe.SchemaVersion, SchemaVersion)
		}
	}
}
