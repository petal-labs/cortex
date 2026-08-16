package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	_ "github.com/mattn/go-sqlite3"

	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
	"github.com/petal-labs/cortex/pkg/types"
)

// mockEmbeddingProvider implements embedding.Provider for testing.
type mockEmbeddingProvider struct {
	dimensions int
}

func (m *mockEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, embedding.ErrEmptyInput
	}
	vec := make([]float32, m.dimensions)
	for i := range vec {
		vec[i] = float32(i) * 0.01
	}
	return vec, nil
}

func (m *mockEmbeddingProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := m.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		results[i] = vec
	}
	return results, nil
}

func (m *mockEmbeddingProvider) Dimensions() int {
	return m.dimensions
}

func (m *mockEmbeddingProvider) Close() error {
	return nil
}

// testServer creates an MCP server with all engines for testing.
func testServer(t *testing.T, allowedNamespace string) *Server {
	t.Helper()

	// Create in-memory SQLite database
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=ON")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	// SQLite requires single connection for in-memory databases
	// to ensure all operations see the same data
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Create backend and run migrations
	backend := sqlite.NewWithDB(db)
	if err := backend.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	// Create mock embedding provider
	emb := &mockEmbeddingProvider{dimensions: 384}

	// Create engines
	convEngine, err := conversation.NewEngine(backend, emb, nil)
	if err != nil {
		t.Fatalf("failed to create conversation engine: %v", err)
	}

	knowEngine, err := knowledge.NewEngine(backend, emb, nil)
	if err != nil {
		t.Fatalf("failed to create knowledge engine: %v", err)
	}

	ctxEngine, err := ctxengine.NewEngine(backend, nil)
	if err != nil {
		t.Fatalf("failed to create context engine: %v", err)
	}

	entityEngine, err := entity.NewEngine(backend, emb, nil)
	if err != nil {
		t.Fatalf("failed to create entity engine: %v", err)
	}

	// Create MCP server
	cfg := &Config{
		Name:             "cortex-test",
		Version:          "1.0.0-test",
		AllowedNamespace: allowedNamespace,
	}

	return New(cfg, convEngine, knowEngine, ctxEngine, entityEngine)
}

// makeToolRequest creates a mock CallToolRequest for testing.
func makeToolRequest(name string, args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	}
}

func TestServerCreation(t *testing.T) {
	srv := testServer(t, "")
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
	if srv.mcp == nil {
		t.Error("expected non-nil MCP server")
	}
}

func TestNamespaceEnforcement(t *testing.T) {
	// Server with restricted namespace
	srv := testServer(t, "allowed-ns")

	tests := []struct {
		name      string
		namespace string
		wantErr   bool
	}{
		{"allowed namespace", "allowed-ns", false},
		{"different namespace", "other-ns", true},
		{"empty namespace", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := srv.checkNamespace(tt.namespace)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}

	// Server with no namespace restriction
	openSrv := testServer(t, "")
	if err := openSrv.checkNamespace("any-namespace"); err != nil {
		t.Errorf("open server should allow any namespace: %v", err)
	}
}

func TestConversationAppend(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	req := makeToolRequest("conversation_append", map[string]any{
		"namespace": "test-ns",
		"thread_id": "thread-1",
		"role":      "user",
		"content":   "Hello, world!",
	})

	result, err := srv.handleConversationAppend(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(result))
	}

	// Verify message was appended
	histReq := makeToolRequest("conversation_history", map[string]any{
		"namespace": "test-ns",
		"thread_id": "thread-1",
	})

	histResult, err := srv.handleConversationHistory(ctx, histReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if histResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(histResult))
	}

	// Parse result and verify
	var histData struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(getTextContent(histResult)), &histData); err != nil {
		t.Fatalf("failed to parse history result: %v", err)
	}
	if len(histData.Messages) == 0 {
		t.Error("expected at least one message in history")
	}
}

func TestConversationSearch(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// First append some messages
	for i, content := range []string{
		"Machine learning is fascinating",
		"Neural networks can solve complex problems",
		"Deep learning requires lots of data",
	} {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		req := makeToolRequest("conversation_append", map[string]any{
			"namespace": "test-ns",
			"thread_id": "ml-thread",
			"role":      role,
			"content":   content,
		})
		if _, err := srv.handleConversationAppend(ctx, req); err != nil {
			t.Fatalf("failed to append message: %v", err)
		}
	}

	// Search for messages
	searchReq := makeToolRequest("conversation_search", map[string]any{
		"namespace": "test-ns",
		"query":     "neural networks AI",
		"top_k":     float64(5),
	})

	result, err := srv.handleConversationSearch(ctx, searchReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(result))
	}
}

func TestConversationSummarize(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// Add messages to create a thread
	for i := 0; i < 5; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		req := makeToolRequest("conversation_append", map[string]any{
			"namespace": "test-ns",
			"thread_id": "summ-thread",
			"role":      role,
			"content":   "Test message content",
		})
		if _, err := srv.handleConversationAppend(ctx, req); err != nil {
			t.Fatalf("failed to append message: %v", err)
		}
	}

	// Try to summarize - should fail because no summarizer is configured
	summReq := makeToolRequest("conversation_summarize", map[string]any{
		"namespace":   "test-ns",
		"thread_id":   "summ-thread",
		"keep_recent": float64(2),
	})

	result, err := srv.handleConversationSummarize(ctx, summReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should return a tool error because no summarizer is set
	if !result.IsError {
		t.Error("expected tool error when no summarizer is configured")
	}

	// The error message should indicate summarizer is not set
	errorText := getTextContent(result)
	if errorText == "" {
		t.Error("expected error message in result")
	}
}

func TestKnowledgeIngestAndSearch(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// Create a collection first
	collReq := makeToolRequest("knowledge_collections", map[string]any{
		"namespace": "test-ns",
		"action":    "create",
		"name":      "docs",
	})

	collResult, err := srv.handleKnowledgeCollections(ctx, collReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if collResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(collResult))
	}

	// Parse collection ID
	var collData struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(getTextContent(collResult)), &collData); err != nil {
		t.Fatalf("failed to parse collection result: %v", err)
	}

	// Ingest a document
	ingestReq := makeToolRequest("knowledge_ingest", map[string]any{
		"namespace":     "test-ns",
		"collection_id": collData.ID,
		"content":       "Golang is a statically typed, compiled programming language designed at Google.",
		"title":         "Introduction to Go",
	})

	ingestResult, err := srv.handleKnowledgeIngest(ctx, ingestReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ingestResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(ingestResult))
	}

	// Search the knowledge base
	searchReq := makeToolRequest("knowledge_search", map[string]any{
		"namespace": "test-ns",
		"query":     "Go programming language",
		"top_k":     float64(5),
	})

	searchResult, err := srv.handleKnowledgeSearch(ctx, searchReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if searchResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(searchResult))
	}
}

func TestKnowledgeCollections(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// Create a collection
	createReq := makeToolRequest("knowledge_collections", map[string]any{
		"namespace":   "test-ns",
		"action":      "create",
		"name":        "test-collection",
		"description": "A test collection",
	})

	createResult, err := srv.handleKnowledgeCollections(ctx, createReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if createResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(createResult))
	}

	// Parse collection ID
	var collData struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(getTextContent(createResult)), &collData); err != nil {
		t.Fatalf("failed to parse collection result: %v", err)
	}

	// List collections
	listReq := makeToolRequest("knowledge_collections", map[string]any{
		"namespace": "test-ns",
		"action":    "list",
	})

	listResult, err := srv.handleKnowledgeCollections(ctx, listReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if listResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(listResult))
	}

	// Delete collection
	deleteReq := makeToolRequest("knowledge_collections", map[string]any{
		"namespace":     "test-ns",
		"action":        "delete",
		"collection_id": collData.ID,
	})

	deleteResult, err := srv.handleKnowledgeCollections(ctx, deleteReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleteResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(deleteResult))
	}
}

func TestKnowledgeBulkIngest(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// Create a collection first
	collReq := makeToolRequest("knowledge_collections", map[string]any{
		"namespace": "test-ns",
		"action":    "create",
		"name":      "bulk-test-collection",
	})

	collResult, err := srv.handleKnowledgeCollections(ctx, collReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var collData struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(getTextContent(collResult)), &collData); err != nil {
		t.Fatalf("failed to parse collection result: %v", err)
	}

	// Bulk ingest multiple documents
	bulkReq := makeToolRequest("knowledge_bulk_ingest", map[string]any{
		"namespace":     "test-ns",
		"collection_id": collData.ID,
		"documents": []any{
			map[string]any{
				"content": "First document about machine learning and neural networks.",
				"title":   "ML Basics",
				"source":  "test://doc1",
			},
			map[string]any{
				"content": "Second document about database optimization techniques.",
				"title":   "DB Optimization",
			},
			map[string]any{
				"content":  "Third document covering API design patterns and best practices.",
				"title":    "API Design",
				"metadata": map[string]any{"topic": "api"},
			},
		},
		"concurrency": float64(2),
	})

	bulkResult, err := srv.handleKnowledgeBulkIngest(ctx, bulkReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bulkResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(bulkResult))
	}

	// Parse result
	var result struct {
		TotalDocuments int `json:"total_documents"`
		Succeeded      int `json:"succeeded"`
		Failed         int `json:"failed"`
		TotalChunks    int `json:"total_chunks"`
	}
	if err := json.Unmarshal([]byte(getTextContent(bulkResult)), &result); err != nil {
		t.Fatalf("failed to parse bulk result: %v", err)
	}

	if result.TotalDocuments != 3 {
		t.Errorf("expected 3 total documents, got %d", result.TotalDocuments)
	}
	if result.Succeeded != 3 {
		t.Errorf("expected 3 succeeded, got %d", result.Succeeded)
	}
	if result.Failed != 0 {
		t.Errorf("expected 0 failed, got %d", result.Failed)
	}
	if result.TotalChunks == 0 {
		t.Error("expected at least some chunks")
	}
}

func TestContextSetAndGet(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// Set a context value
	setReq := makeToolRequest("context_set", map[string]any{
		"namespace": "test-ns",
		"key":       "user_prefs",
		"value":     map[string]any{"theme": "dark", "language": "en"},
	})

	setResult, err := srv.handleContextSet(ctx, setReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if setResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(setResult))
	}

	// Get the context value
	getReq := makeToolRequest("context_get", map[string]any{
		"namespace": "test-ns",
		"key":       "user_prefs",
	})

	getResult, err := srv.handleContextGet(ctx, getReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if getResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(getResult))
	}

	// Verify the value
	var getData struct {
		Value  map[string]any `json:"value"`
		Exists bool           `json:"exists"`
	}
	if err := json.Unmarshal([]byte(getTextContent(getResult)), &getData); err != nil {
		t.Fatalf("failed to parse get result: %v", err)
	}
	if !getData.Exists {
		t.Error("expected context value to exist")
	}
	if getData.Value["theme"] != "dark" {
		t.Errorf("expected theme=dark, got %v", getData.Value["theme"])
	}
}

func TestContextMerge(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// Set initial value
	setReq := makeToolRequest("context_set", map[string]any{
		"namespace": "test-ns",
		"key":       "config",
		"value":     map[string]any{"a": 1, "b": 2},
	})

	if _, err := srv.handleContextSet(ctx, setReq); err != nil {
		t.Fatalf("failed to set initial value: %v", err)
	}

	// Merge additional value
	mergeReq := makeToolRequest("context_merge", map[string]any{
		"namespace": "test-ns",
		"key":       "config",
		"value":     map[string]any{"c": 3},
		"strategy":  "deep_merge",
	})

	mergeResult, err := srv.handleContextMerge(ctx, mergeReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mergeResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(mergeResult))
	}

	// Verify merged value
	getReq := makeToolRequest("context_get", map[string]any{
		"namespace": "test-ns",
		"key":       "config",
	})

	getResult, err := srv.handleContextGet(ctx, getReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var getData struct {
		Value map[string]any `json:"value"`
	}
	if err := json.Unmarshal([]byte(getTextContent(getResult)), &getData); err != nil {
		t.Fatalf("failed to parse get result: %v", err)
	}

	// Should have all three keys
	if len(getData.Value) != 3 {
		t.Errorf("expected 3 keys after merge, got %d", len(getData.Value))
	}
}

func TestContextList(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// Set multiple context values
	for _, key := range []string{"user:name", "user:email", "settings:theme"} {
		setReq := makeToolRequest("context_set", map[string]any{
			"namespace": "test-ns",
			"key":       key,
			"value":     "test-value",
		})
		if _, err := srv.handleContextSet(ctx, setReq); err != nil {
			t.Fatalf("failed to set context: %v", err)
		}
	}

	// List all keys
	listReq := makeToolRequest("context_list", map[string]any{
		"namespace": "test-ns",
	})

	listResult, err := srv.handleContextList(ctx, listReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if listResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(listResult))
	}

	// List with prefix filter
	prefixReq := makeToolRequest("context_list", map[string]any{
		"namespace": "test-ns",
		"prefix":    "user:",
	})

	prefixResult, err := srv.handleContextList(ctx, prefixReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prefixResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(prefixResult))
	}
}

func TestContextHistory(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// Set a context value multiple times to create history
	for i := 1; i <= 3; i++ {
		setReq := makeToolRequest("context_set", map[string]any{
			"namespace": "test-ns",
			"key":       "versioned-key",
			"value":     map[string]any{"version": i},
		})
		_, err := srv.handleContextSet(ctx, setReq)
		if err != nil {
			t.Fatalf("failed to set context: %v", err)
		}
	}

	// Get history
	historyReq := makeToolRequest("context_history", map[string]any{
		"namespace": "test-ns",
		"key":       "versioned-key",
	})

	historyResult, err := srv.handleContextHistory(ctx, historyReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if historyResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(historyResult))
	}

	// Verify result contains history
	content := getTextContent(historyResult)
	if content == "" {
		t.Error("expected non-empty history result")
	}
}

func TestContextHistoryWithRunID(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// Set context values with run_id
	for i := 1; i <= 2; i++ {
		setReq := makeToolRequest("context_set", map[string]any{
			"namespace": "test-ns",
			"key":       "run-scoped-key",
			"value":     map[string]any{"iteration": i},
			"run_id":    "run-123",
		})
		_, err := srv.handleContextSet(ctx, setReq)
		if err != nil {
			t.Fatalf("failed to set context: %v", err)
		}
	}

	// Get history for specific run
	historyReq := makeToolRequest("context_history", map[string]any{
		"namespace": "test-ns",
		"key":       "run-scoped-key",
		"run_id":    "run-123",
	})

	historyResult, err := srv.handleContextHistory(ctx, historyReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if historyResult.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(historyResult))
	}
}

func TestEntityQuery(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// First, add some conversation with entity mentions
	req := makeToolRequest("conversation_append", map[string]any{
		"namespace": "test-ns",
		"thread_id": "entity-thread",
		"role":      "user",
		"content":   "I met John Smith at Google headquarters in Mountain View.",
	})

	if _, err := srv.handleConversationAppend(ctx, req); err != nil {
		t.Fatalf("failed to append message: %v", err)
	}

	// Query for an entity (may not find it since extraction is async)
	queryReq := makeToolRequest("entity_query", map[string]any{
		"namespace": "test-ns",
		"name":      "John Smith",
	})

	// This may return "not found" which is ok for this test
	result, err := srv.handleEntityQuery(ctx, queryReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Result could be error (not found) or success
	_ = result
}

func TestEntitySearch(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// Search for entities (empty namespace should return empty results)
	searchReq := makeToolRequest("entity_search", map[string]any{
		"namespace": "test-ns",
		"query":     "software engineer",
		"top_k":     float64(10),
	})

	result, err := srv.handleEntitySearch(ctx, searchReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(result))
	}
}

func TestEntityList(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// List entities (empty namespace should return empty results)
	listReq := makeToolRequest("entity_list", map[string]any{
		"namespace": "test-ns",
		"sort_by":   "mention_count",
		"limit":     float64(50),
	})

	result, err := srv.handleEntityList(ctx, listReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected tool error: %v", getTextContent(result))
	}
}

func TestNamespaceRestriction(t *testing.T) {
	srv := testServer(t, "allowed-namespace")
	ctx := context.Background()

	// Try to append with wrong namespace
	req := makeToolRequest("conversation_append", map[string]any{
		"namespace": "wrong-namespace",
		"thread_id": "thread-1",
		"role":      "user",
		"content":   "Hello",
	})

	result, err := srv.handleConversationAppend(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be an error result (namespace not allowed)
	if !result.IsError {
		t.Error("expected error for wrong namespace")
	}

	// Try with correct namespace
	reqCorrect := makeToolRequest("conversation_append", map[string]any{
		"namespace": "allowed-namespace",
		"thread_id": "thread-1",
		"role":      "user",
		"content":   "Hello",
	})
	result, err = srv.handleConversationAppend(ctx, reqCorrect)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected tool error for allowed namespace: %v", getTextContent(result))
	}
}

func TestMapEntityType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		ok       bool
	}{
		{"person", "person", true},
		{"PERSON", "person", true},
		{"organization", "organization", true},
		{"Organization", "organization", true},
		{"product", "product", true},
		{"location", "location", true},
		{"concept", "concept", true},
		{"event", "event", true}, // real type, no longer remapped to concept
		{"other", "other", true}, // real type, no longer remapped to product
		{"unknown", "", false},   // rejected, no longer remapped to product
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, ok := mapEntityType(tt.input)
			if ok != tt.ok {
				t.Errorf("mapEntityType(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if string(result) != tt.expected {
				t.Errorf("mapEntityType(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseEntityTypeArg(t *testing.T) {
	t.Run("absent returns nil", func(t *testing.T) {
		got, errResult := parseEntityTypeArg(map[string]any{})
		if errResult != nil {
			t.Fatalf("unexpected error: %s", getTextContent(errResult))
		}
		if got != nil {
			t.Errorf("expected nil type, got %v", *got)
		}
	})

	t.Run("event accepted", func(t *testing.T) {
		got, errResult := parseEntityTypeArg(map[string]any{"type": "event"})
		if errResult != nil {
			t.Fatalf("unexpected error: %s", getTextContent(errResult))
		}
		if got == nil || *got != "event" {
			t.Errorf("expected event, got %v", got)
		}
	})

	t.Run("unsupported value rejected", func(t *testing.T) {
		_, errResult := parseEntityTypeArg(map[string]any{"type": "bogus"})
		if errResult == nil {
			t.Fatal("expected error for unsupported type, got nil")
		}
		msg := getTextContent(errResult)
		if !strings.Contains(msg, "unsupported value") || !strings.Contains(msg, "bogus") {
			t.Errorf("expected clear unsupported-value message, got: %s", msg)
		}
	})

	t.Run("wrong type rejected", func(t *testing.T) {
		_, errResult := parseEntityTypeArg(map[string]any{"type": 123})
		if errResult == nil {
			t.Fatal("expected error for non-string type, got nil")
		}
	})
}

// TestEntitySearchTypeFilterNotRemapped verifies end to end that filtering
// by "event"/"other" matches only entities of that exact type — the old
// code silently remapped event→concept and other→product.
func TestEntitySearchTypeFilterNotRemapped(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	// Create one entity of each type directly through the engine.
	for _, name := range []string{"ev-1", "ot-1", "pr-1", "co-1"} {
		typ := map[string]types.EntityType{
			"ev-1": "event",
			"ot-1": "other",
			"pr-1": "product",
			"co-1": "concept",
		}[name]
		if _, err := srv.entity.Create(ctx, "test-ns", name, typ, nil); err != nil {
			t.Fatalf("failed to create %s: %v", name, err)
		}
	}

	list := func(typeArg string) []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} {
		t.Helper()
		result, err := srv.handleEntityList(ctx, makeToolRequest("entity_list", map[string]any{
			"namespace": "test-ns",
			"type":      typeArg,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected tool error: %s", getTextContent(result))
		}
		var resp struct {
			Entities []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"entities"`
		}
		if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
			t.Fatalf("failed to parse result: %v", err)
		}
		return resp.Entities
	}

	if ents := list("event"); len(ents) != 1 || ents[0].Name != "ev-1" || ents[0].Type != "event" {
		t.Errorf("event filter: expected exactly [ev-1/event], got %+v", ents)
	}
	if ents := list("other"); len(ents) != 1 || ents[0].Name != "ot-1" || ents[0].Type != "other" {
		t.Errorf("other filter: expected exactly [ot-1/other], got %+v", ents)
	}
	// The old bug: product filter would have returned ot-1 (remapped other).
	if ents := list("product"); len(ents) != 1 || ents[0].Name != "pr-1" {
		t.Errorf("product filter: expected exactly [pr-1], got %+v", ents)
	}
	// The old bug: concept filter would have returned ev-1 (remapped event).
	if ents := list("concept"); len(ents) != 1 || ents[0].Name != "co-1" {
		t.Errorf("concept filter: expected exactly [co-1], got %+v", ents)
	}
}

func TestToStringMap(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]string
	}{
		{
			name:     "string values",
			input:    map[string]any{"a": "hello", "b": "world"},
			expected: map[string]string{"a": "hello", "b": "world"},
		},
		{
			name:     "mixed types",
			input:    map[string]any{"s": "str", "n": 42, "b": true},
			expected: map[string]string{"s": "str", "n": "42", "b": "true"},
		},
		{
			name:     "empty map",
			input:    map[string]any{},
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toStringMap(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("length mismatch: got %d, want %d", len(result), len(tt.expected))
			}
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("key %q: got %q, want %q", k, result[k], v)
				}
			}
		})
	}
}

func TestParseChunkConfig(t *testing.T) {
	input := map[string]any{
		"strategy":   "recursive",
		"max_tokens": float64(500),
		"overlap":    float64(50),
	}

	cfg := parseChunkConfig(input)
	if cfg.Strategy != "recursive" {
		t.Errorf("expected strategy=recursive, got %s", cfg.Strategy)
	}
	if cfg.MaxTokens != 500 {
		t.Errorf("expected max_tokens=500, got %d", cfg.MaxTokens)
	}
	if cfg.Overlap != 50 {
		t.Errorf("expected overlap=50, got %d", cfg.Overlap)
	}
}

// getTextContent extracts text content from a tool result.
func getTextContent(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func TestToolErrorSanitizesInternalErrors(t *testing.T) {
	// A non-sentinel error (e.g. a raw DB error) should be sanitized.
	result, _ := toolError(fmt.Errorf("pq: connection refused at postgres://internal:5432"))

	if !result.IsError {
		t.Fatal("expected error result")
	}
	msg := getTextContent(result)
	if strings.Contains(msg, "postgres://") || strings.Contains(msg, "connection refused") {
		t.Errorf("internal error details leaked to client: %s", msg)
	}
	if !strings.Contains(msg, "internal server error") {
		t.Errorf("expected sanitized message, got: %s", msg)
	}
}

func TestToolErrorExposesClientErrors(t *testing.T) {
	// Known sentinel errors should be exposed as-is.
	result, _ := toolError(knowledge.ErrCollectionNotFound)

	if !result.IsError {
		t.Fatal("expected error result")
	}
	msg := getTextContent(result)
	if !strings.Contains(msg, "collection not found") {
		t.Errorf("expected client error message, got: %s", msg)
	}
}

func TestOptIntWrongType(t *testing.T) {
	args := map[string]any{"top_k": "5"} // string instead of number
	_, errResult := optInt(args, "top_k", 0)
	if errResult == nil {
		t.Fatal("expected error for string where number required")
	}
	if !strings.Contains(getTextContent(errResult), "must be a number") {
		t.Errorf("expected 'must be a number' in error, got: %s", getTextContent(errResult))
	}
}

func TestOptIntAbsent(t *testing.T) {
	args := map[string]any{}
	val, errResult := optInt(args, "top_k", 42)
	if errResult != nil {
		t.Fatalf("expected no error when absent, got: %s", getTextContent(errResult))
	}
	if val != 42 {
		t.Errorf("expected default 42, got %d", val)
	}
}

func TestOptStringPtrWrongType(t *testing.T) {
	args := map[string]any{"run_id": 123} // number instead of string
	_, errResult := optStringPtr(args, "run_id")
	if errResult == nil {
		t.Fatal("expected error for number where string required")
	}
}

func TestOptBoolWrongType(t *testing.T) {
	args := map[string]any{"include_summary": "yes"} // string instead of bool
	_, errResult := optBool(args, "include_summary", false)
	if errResult == nil {
		t.Fatal("expected error for string where boolean required")
	}
}

func TestValidateJSONIntPrecision(t *testing.T) {
	t.Run("small integers accepted", func(t *testing.T) {
		for _, v := range []any{float64(0), float64(42), float64(-12345), float64(1) * float64(1<<53)} {
			if errResult := validateJSONIntPrecision(v, "value"); errResult != nil {
				t.Errorf("expected %v accepted, got: %s", v, getTextContent(errResult))
			}
		}
	})

	t.Run("fractional floats accepted", func(t *testing.T) {
		for _, v := range []any{3.14, -0.5, 1e10 + 0.25} {
			if errResult := validateJSONIntPrecision(v, "value"); errResult != nil {
				t.Errorf("expected %v accepted, got: %s", v, getTextContent(errResult))
			}
		}
	})

	t.Run("integer above 2^53 rejected", func(t *testing.T) {
		// Above 2^53, float64 spacing is 2, so the smallest real decoded
		// value above the bound is 2^53+2. (2^53+1 literally cannot exist
		// in a float64 — it rounds to 2^53, which is accepted.)
		const twoPow53 = float64(1) * float64(1<<53)
		errResult := validateJSONIntPrecision(twoPow53+2, "value")
		if errResult == nil {
			t.Fatal("expected rejection for integer above 2^53")
		}
		msg := getTextContent(errResult)
		if !strings.Contains(msg, "exceeds 2^53") || !strings.Contains(msg, "as strings") {
			t.Errorf("expected actionable message, got: %s", msg)
		}
	})

	t.Run("large float magnitude rejected", func(t *testing.T) {
		// 1e20 is integer-valued in float64 — untrustworthy.
		if errResult := validateJSONIntPrecision(1e20, "value"); errResult == nil {
			t.Fatal("expected rejection for integer-valued 1e20")
		}
	})

	t.Run("nested map and array paths reported", func(t *testing.T) {
		const twoPow53 = float64(1) * float64(1<<53)
		v := map[string]any{
			"user": map[string]any{
				"ids": []any{float64(1), twoPow53, twoPow53 + 2},
			},
		}
		errResult := validateJSONIntPrecision(v, "value")
		if errResult == nil {
			t.Fatal("expected rejection for nested oversized integer")
		}
		msg := getTextContent(errResult)
		if !strings.Contains(msg, "value.user.ids[2]") {
			t.Errorf("expected precise path in message, got: %s", msg)
		}
	})

	t.Run("non-numeric types accepted", func(t *testing.T) {
		v := map[string]any{
			"name": "snowflake", "big": "9007199254740993",
			"ok": true, "nil": nil, "nested": []any{"a", 1.5},
		}
		if errResult := validateJSONIntPrecision(v, "value"); errResult != nil {
			t.Errorf("expected accepted, got: %s", getTextContent(errResult))
		}
	})
}

func TestParseExpectedVersion(t *testing.T) {
	t.Run("absent returns nil", func(t *testing.T) {
		got, errResult := parseExpectedVersion(map[string]any{})
		if errResult != nil {
			t.Fatalf("unexpected error: %s", getTextContent(errResult))
		}
		if got != nil {
			t.Errorf("expected nil, got %v", *got)
		}
	})

	t.Run("valid version", func(t *testing.T) {
		// JSON decoding yields float64 numbers; mirror that here.
		got, errResult := parseExpectedVersion(map[string]any{"expected_version": float64(7)})
		if errResult != nil {
			t.Fatalf("unexpected error: %s", getTextContent(errResult))
		}
		if got == nil || *got != 7 {
			t.Errorf("expected 7, got %v", got)
		}
	})

	t.Run("zero and negative treated as absent", func(t *testing.T) {
		for _, v := range []any{float64(0), float64(-3)} {
			got, errResult := parseExpectedVersion(map[string]any{"expected_version": v})
			if errResult != nil {
				t.Fatalf("unexpected error: %s", getTextContent(errResult))
			}
			if got != nil {
				t.Errorf("expected nil for %v, got %v", v, *got)
			}
		}
	})

	t.Run("fractional rejected", func(t *testing.T) {
		_, errResult := parseExpectedVersion(map[string]any{"expected_version": 2.5})
		if errResult == nil {
			t.Fatal("expected rejection for fractional version")
		}
	})

	t.Run("above 2^53 rejected", func(t *testing.T) {
		_, errResult := parseExpectedVersion(map[string]any{"expected_version": float64(1)*float64(1<<53) + 2})
		if errResult == nil {
			t.Fatal("expected rejection for version above 2^53")
		}
	})

	t.Run("non-number rejected", func(t *testing.T) {
		_, errResult := parseExpectedVersion(map[string]any{"expected_version": "3"})
		if errResult == nil {
			t.Fatal("expected rejection for string version")
		}
	})
}

// TestContextSetRejectsPrecisionLoss verifies end to end that a context
// value containing an integer above 2^53 is rejected with a clear error
// instead of being silently stored with corrupted precision.
func TestContextSetRejectsPrecisionLoss(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	const twoPow53 = float64(1) * float64(1<<53)
	result, err := srv.handleContextSet(ctx, makeToolRequest("context_set", map[string]any{
		"namespace": "test-ns",
		"key":       "snowflake",
		"value":     map[string]any{"user_id": twoPow53 + 2},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for oversized integer")
	}
	msg := getTextContent(result)
	if !strings.Contains(msg, "exceeds 2^53") {
		t.Errorf("expected precision error, got: %s", msg)
	}
}

// TestContextSetRoundTripsExactIntegers verifies integers within the exact
// float64 range survive the full set/get round trip unchanged.
func TestContextSetRoundTripsExactIntegers(t *testing.T) {
	srv := testServer(t, "")
	ctx := context.Background()

	const exact = 9007199254740992 // 2^53, exactly representable
	if _, err := srv.handleContextSet(ctx, makeToolRequest("context_set", map[string]any{
		"namespace": "test-ns",
		"key":       "ids",
		"value":     map[string]any{"max": float64(exact), "neg": float64(-exact), "small": 123456789},
	})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := srv.handleContextGet(ctx, makeToolRequest("context_get", map[string]any{
		"namespace": "test-ns",
		"key":       "ids",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", getTextContent(result))
	}

	var resp struct {
		Value struct {
			Max   float64 `json:"max"`
			Neg   float64 `json:"neg"`
			Small float64 `json:"small"`
		} `json:"value"`
	}
	if err := json.Unmarshal([]byte(getTextContent(result)), &resp); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}
	if resp.Value.Max != exact || resp.Value.Neg != -exact || resp.Value.Small != 123456789 {
		t.Errorf("round trip corrupted values: got %+v", resp.Value)
	}
}
