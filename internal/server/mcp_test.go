package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	_ "github.com/mattn/go-sqlite3"

	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
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
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

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
	}{
		{"person", "person"},
		{"PERSON", "person"},
		{"organization", "organization"},
		{"Organization", "organization"},
		{"product", "product"},
		{"location", "location"},
		{"concept", "concept"},
		{"event", "concept"},   // event maps to concept
		{"other", "product"},   // other maps to product
		{"unknown", "product"}, // unknown maps to product
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := mapEntityType(tt.input)
			if string(result) != tt.expected {
				t.Errorf("mapEntityType(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
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
