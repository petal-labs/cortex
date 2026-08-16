// Package server implements the MCP (Model Context Protocol) server for Cortex.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"

	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/observability"
	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

// Server is the MCP server for Cortex.
type Server struct {
	mcp              *server.MCPServer
	conversation     *conversation.Engine
	knowledge        *knowledge.Engine
	context          *ctxengine.Engine
	entity           *entity.Engine
	allowedNamespace string
}

// Config holds configuration for the MCP server.
type Config struct {
	Name             string
	Version          string
	AllowedNamespace string // Empty means all namespaces allowed
}

// New creates a new MCP server with all Cortex tools registered.
func New(
	cfg *Config,
	conversationEngine *conversation.Engine,
	knowledgeEngine *knowledge.Engine,
	contextEngine *ctxengine.Engine,
	entityEngine *entity.Engine,
) *Server {
	s := &Server{
		conversation:     conversationEngine,
		knowledge:        knowledgeEngine,
		context:          contextEngine,
		entity:           entityEngine,
		allowedNamespace: cfg.AllowedNamespace,
	}

	// Create MCP server
	s.mcp = server.NewMCPServer(
		cfg.Name,
		cfg.Version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
		server.WithResourceRecovery(),
	)

	// Register all tools
	s.registerConversationTools()
	s.registerKnowledgeTools()
	s.registerContextTools()
	s.registerEntityTools()

	return s
}

// ServeStdio starts the MCP server using stdio transport.
func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.mcp)
}

// ServeSSE starts the MCP server using Server-Sent Events transport.
// The server listens on the specified address (e.g., ":9810").
func (s *Server) ServeSSE(addr string) error {
	sseServer := server.NewSSEServer(s.mcp,
		server.WithSSEEndpoint("/sse"),
		server.WithMessageEndpoint("/message"),
		server.WithKeepAlive(true),
	)
	return sseServer.Start(addr)
}

// SSEServer returns the underlying SSE server for custom HTTP integration.
// This allows embedding the SSE endpoints in an existing HTTP server.
func (s *Server) SSEServer() *server.SSEServer {
	return server.NewSSEServer(s.mcp,
		server.WithSSEEndpoint("/sse"),
		server.WithMessageEndpoint("/message"),
		server.WithKeepAlive(true),
	)
}

// checkNamespace validates that the namespace is allowed.
func (s *Server) checkNamespace(namespace string) error {
	if s.allowedNamespace != "" && namespace != s.allowedNamespace {
		return fmt.Errorf("namespace %q not allowed (allowed: %q)", namespace, s.allowedNamespace)
	}
	return nil
}

// registerConversationTools registers conversation memory tools.
func (s *Server) registerConversationTools() {
	// conversation_append
	appendTool := mcp.NewTool("conversation_append",
		mcp.WithDescription("Append a message to a conversation thread. Creates the thread if it doesn't exist."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope (e.g., workflow ID, user ID)"),
		),
		mcp.WithString("thread_id",
			mcp.Required(),
			mcp.Description("Conversation thread identifier"),
		),
		mcp.WithString("role",
			mcp.Required(),
			mcp.Description("Message role"),
			mcp.Enum("user", "assistant", "system", "tool"),
		),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("Message content"),
		),
		mcp.WithObject("metadata",
			mcp.Description("Optional key-value metadata"),
		),
		mcp.WithNumber("max_content_length",
			mcp.Description("Truncate content exceeding this character count (optional, useful for large tool outputs)"),
		),
	)
	s.mcp.AddTool(appendTool, s.handleConversationAppend)

	// conversation_history
	historyTool := mcp.NewTool("conversation_history",
		mcp.WithDescription("Retrieve recent messages from a conversation thread, including any summarized context."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("thread_id",
			mcp.Required(),
			mcp.Description("Conversation thread identifier"),
		),
		mcp.WithNumber("last_n",
			mcp.Description("Number of recent messages to return (default: 20)"),
		),
		mcp.WithBoolean("include_summary",
			mcp.Description("Prepend thread summary if available (default: true)"),
		),
		mcp.WithString("cursor",
			mcp.Description("Pagination cursor from previous response (optional)"),
		),
	)
	s.mcp.AddTool(historyTool, s.handleConversationHistory)

	// conversation_search
	searchTool := mcp.NewTool("conversation_search",
		mcp.WithDescription("Search across conversation history in a namespace. Supports vector (semantic), hybrid (vector+text), or text-only search modes."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Natural language search query"),
		),
		mcp.WithString("thread_id",
			mcp.Description("Limit to a specific thread (optional)"),
		),
		mcp.WithNumber("top_k",
			mcp.Description("Max results (default: 5)"),
		),
		mcp.WithString("search_mode",
			mcp.Description("Search mode: vector (semantic similarity), hybrid (vector+text with RRF), or text (BM25 full-text)"),
			mcp.Enum("vector", "hybrid", "text"),
		),
		mcp.WithNumber("alpha",
			mcp.Description("Hybrid search weight: 0=pure text, 1=pure vector, 0.5=equal (default: 0.5)"),
		),
	)
	s.mcp.AddTool(searchTool, s.handleConversationSearch)

	// conversation_summarize
	summarizeTool := mcp.NewTool("conversation_summarize",
		mcp.WithDescription("Compress older messages into a summary via LLM to manage context window limits."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("thread_id",
			mcp.Required(),
			mcp.Description("Conversation thread identifier"),
		),
		mcp.WithNumber("keep_recent",
			mcp.Description("Number of recent messages to keep unsummarized (default: 10)"),
		),
	)
	s.mcp.AddTool(summarizeTool, s.handleConversationSummarize)
}

// registerKnowledgeTools registers knowledge store tools.
func (s *Server) registerKnowledgeTools() {
	// knowledge_ingest
	ingestTool := mcp.NewTool("knowledge_ingest",
		mcp.WithDescription("Ingest a document into the knowledge store. The document is chunked, embedded, and indexed for retrieval."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("collection_id",
			mcp.Required(),
			mcp.Description("Collection to add the document to"),
		),
		mcp.WithString("title",
			mcp.Description("Document title"),
		),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("Document text content"),
		),
		mcp.WithString("content_type",
			mcp.Description("Content format (default: text)"),
			mcp.Enum("text", "markdown", "html"),
		),
		mcp.WithString("source",
			mcp.Description("Origin URL or file path"),
		),
		mcp.WithObject("metadata",
			mcp.Description("Filterable key-value metadata"),
		),
		mcp.WithObject("chunk_config",
			mcp.Description("Override collection's default chunking (optional)"),
		),
	)
	s.mcp.AddTool(ingestTool, s.handleKnowledgeIngest)

	// knowledge_search
	searchTool := mcp.NewTool("knowledge_search",
		mcp.WithDescription("Search the knowledge store using semantic similarity. Returns relevant document chunks with context."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Natural language search query"),
		),
		mcp.WithString("collection_id",
			mcp.Description("Limit to a specific collection (optional)"),
		),
		mcp.WithNumber("top_k",
			mcp.Description("Max results (default: 5)"),
		),
		mcp.WithNumber("min_score",
			mcp.Description("Minimum similarity threshold 0.0-1.0 (default: 0.0)"),
		),
		mcp.WithObject("filters",
			mcp.Description("Metadata key-value filters (optional)"),
		),
		mcp.WithBoolean("include_context",
			mcp.Description("Include adjacent chunks for context (default: true)"),
		),
		mcp.WithNumber("context_window",
			mcp.Description("Number of adjacent chunks to include (default: 1)"),
		),
		mcp.WithString("search_mode",
			mcp.Description("Search mode: vector (semantic similarity), hybrid (vector+text with RRF), or text (BM25 full-text)"),
			mcp.Enum("vector", "hybrid", "text"),
		),
		mcp.WithNumber("alpha",
			mcp.Description("Hybrid search weight: 0=pure text, 1=pure vector, 0.5=equal (default: 0.5)"),
		),
	)
	s.mcp.AddTool(searchTool, s.handleKnowledgeSearch)

	// knowledge_collections
	collectionsTool := mcp.NewTool("knowledge_collections",
		mcp.WithDescription("List or create collections in a namespace."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("action",
			mcp.Required(),
			mcp.Description("Operation to perform"),
			mcp.Enum("list", "create", "delete"),
		),
		mcp.WithString("name",
			mcp.Description("Collection name (required for create)"),
		),
		mcp.WithString("description",
			mcp.Description("Collection description (for create)"),
		),
		mcp.WithString("collection_id",
			mcp.Description("Collection to delete (for delete)"),
		),
		mcp.WithObject("chunk_config",
			mcp.Description("Default chunk config for new documents (for create)"),
		),
	)
	s.mcp.AddTool(collectionsTool, s.handleKnowledgeCollections)

	// knowledge_bulk_ingest
	bulkIngestTool := mcp.NewTool("knowledge_bulk_ingest",
		mcp.WithDescription("Ingest multiple documents into the knowledge store in a single operation. Provides progress reporting and per-document results."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("collection_id",
			mcp.Required(),
			mcp.Description("Collection to add documents to"),
		),
		mcp.WithArray("documents",
			mcp.Required(),
			mcp.Description("Array of documents to ingest. Each document should have: content (required), title, source, content_type, metadata"),
		),
		mcp.WithNumber("concurrency",
			mcp.Description("Number of concurrent workers (default: 4, max: 10)"),
		),
		mcp.WithBoolean("continue_on_error",
			mcp.Description("Continue processing if individual documents fail (default: true)"),
		),
		mcp.WithObject("chunk_config",
			mcp.Description("Override collection's default chunking for all documents (optional)"),
		),
	)
	s.mcp.AddTool(bulkIngestTool, s.handleKnowledgeBulkIngest)
}

// registerContextTools registers workflow context tools.
func (s *Server) registerContextTools() {
	// context_get
	getTool := mcp.NewTool("context_get",
		mcp.WithDescription("Retrieve a value from workflow context by key."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("key",
			mcp.Required(),
			mcp.Description("Context key"),
		),
		mcp.WithString("run_id",
			mcp.Description("Scope to a specific run (omit for persistent context)"),
		),
	)
	s.mcp.AddTool(getTool, s.handleContextGet)

	// context_set
	setTool := mcp.NewTool("context_set",
		mcp.WithDescription("Store a value in workflow context. Overwrites any existing value at this key."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("key",
			mcp.Required(),
			mcp.Description("Context key"),
		),
		mcp.WithAny("value",
			mcp.Required(),
			mcp.Description("Any JSON-serializable value"),
		),
		mcp.WithString("run_id",
			mcp.Description("Scope to a specific run (omit for persistent context)"),
		),
		mcp.WithNumber("ttl_seconds",
			mcp.Description("Auto-expire after this many seconds (optional)"),
		),
		mcp.WithNumber("expected_version",
			mcp.Description("Optimistic concurrency check (optional)"),
		),
	)
	s.mcp.AddTool(setTool, s.handleContextSet)

	// context_merge
	mergeTool := mcp.NewTool("context_merge",
		mcp.WithDescription("Merge a value into an existing workflow context key using a specified strategy."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("key",
			mcp.Required(),
			mcp.Description("Context key"),
		),
		mcp.WithAny("value",
			mcp.Required(),
			mcp.Description("Value to merge"),
		),
		mcp.WithString("strategy",
			mcp.Description("Merge strategy (default: deep_merge)"),
			mcp.Enum("deep_merge", "append", "replace", "max", "min", "sum"),
		),
		mcp.WithString("run_id",
			mcp.Description("Scope to a specific run (omit for persistent context)"),
		),
		mcp.WithNumber("expected_version",
			mcp.Description("Optimistic concurrency check (optional)"),
		),
	)
	s.mcp.AddTool(mergeTool, s.handleContextMerge)

	// context_list
	listTool := mcp.NewTool("context_list",
		mcp.WithDescription("List keys in a workflow context namespace."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("prefix",
			mcp.Description("Key prefix filter (optional)"),
		),
		mcp.WithString("run_id",
			mcp.Description("Scope to a specific run (omit for persistent context)"),
		),
		mcp.WithString("cursor",
			mcp.Description("Pagination cursor from previous response (optional)"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max results per page (default: 50)"),
		),
	)
	s.mcp.AddTool(listTool, s.handleContextList)

	// context_history
	historyTool := mcp.NewTool("context_history",
		mcp.WithDescription("Get version history of a workflow context key. Returns all previous values with timestamps."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("key",
			mcp.Required(),
			mcp.Description("Context key"),
		),
		mcp.WithString("run_id",
			mcp.Description("Scope to a specific run (omit for persistent context)"),
		),
		mcp.WithString("cursor",
			mcp.Description("Pagination cursor from previous response (optional)"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max results per page (default: 50)"),
		),
	)
	s.mcp.AddTool(historyTool, s.handleContextHistory)
}

// registerEntityTools registers entity memory tools.
func (s *Server) registerEntityTools() {
	// entity_query
	queryTool := mcp.NewTool("entity_query",
		mcp.WithDescription("Look up an entity by name or alias. Returns the entity's summary, attributes, relationships, and recent mentions."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Entity name or alias to look up"),
		),
		mcp.WithBoolean("include_mentions",
			mcp.Description("Include recent mentions with source context (default: true)"),
		),
		mcp.WithNumber("mention_limit",
			mcp.Description("Max mentions to return (default: 10)"),
		),
	)
	s.mcp.AddTool(queryTool, s.handleEntityQuery)

	// entity_search
	searchTool := mcp.NewTool("entity_search",
		mcp.WithDescription("Semantic search across entities by description, attributes, or summary content."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Natural language search query"),
		),
		mcp.WithString("type",
			mcp.Description("Filter by entity type (optional)"),
			mcp.Enum("person", "organization", "product", "location", "concept", "event", "other"),
		),
		mcp.WithNumber("top_k",
			mcp.Description("Max results (default: 10)"),
		),
		mcp.WithString("search_mode",
			mcp.Description("Search mode: vector (semantic similarity), hybrid (vector+text with RRF), or text (BM25 full-text)"),
			mcp.Enum("vector", "hybrid", "text"),
		),
		mcp.WithNumber("alpha",
			mcp.Description("Hybrid search weight: 0=pure text, 1=pure vector, 0.5=equal (default: 0.5)"),
		),
	)
	s.mcp.AddTool(searchTool, s.handleEntitySearch)

	// entity_relationships
	relationshipsTool := mcp.NewTool("entity_relationships",
		mcp.WithDescription("Get all relationships for an entity, optionally filtered by relationship type."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("entity_name",
			mcp.Required(),
			mcp.Description("Entity name or alias"),
		),
		mcp.WithString("relation_type",
			mcp.Description("Filter by relation type (optional)"),
		),
		mcp.WithString("direction",
			mcp.Description("Relationship direction (default: both)"),
			mcp.Enum("outgoing", "incoming", "both"),
		),
	)
	s.mcp.AddTool(relationshipsTool, s.handleEntityRelationships)

	// entity_update
	updateTool := mcp.NewTool("entity_update",
		mcp.WithDescription("Manually add or correct attributes, aliases, or type on an existing entity."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("entity_name",
			mcp.Required(),
			mcp.Description("Entity name or alias"),
		),
		mcp.WithObject("attributes",
			mcp.Description("Key-value attributes to set or update"),
		),
		mcp.WithArray("aliases",
			mcp.Description("Additional aliases to add"),
			mcp.Items(map[string]any{"type": "string"}),
		),
		mcp.WithString("type",
			mcp.Description("Entity type"),
			mcp.Enum("person", "organization", "product", "location", "concept", "event", "other"),
		),
	)
	s.mcp.AddTool(updateTool, s.handleEntityUpdate)

	// entity_merge
	mergeTool := mcp.NewTool("entity_merge",
		mcp.WithDescription("Merge two entities that refer to the same real-world thing. Combines mentions, relationships, and attributes."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("source_entity",
			mcp.Required(),
			mcp.Description("Entity name to merge FROM (will be deleted)"),
		),
		mcp.WithString("target_entity",
			mcp.Required(),
			mcp.Description("Entity name to merge INTO (will be kept)"),
		),
	)
	s.mcp.AddTool(mergeTool, s.handleEntityMerge)

	// entity_list
	listTool := mcp.NewTool("entity_list",
		mcp.WithDescription("List entities in a namespace with optional type filter and sorting."),
		mcp.WithString("namespace",
			mcp.Required(),
			mcp.Description("Isolation scope"),
		),
		mcp.WithString("type",
			mcp.Description("Filter by entity type"),
			mcp.Enum("person", "organization", "product", "location", "concept", "event", "other"),
		),
		mcp.WithString("sort_by",
			mcp.Description("Sort order (default: mention_count)"),
			mcp.Enum("name", "mention_count", "last_seen"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max results (default: 50)"),
		),
		mcp.WithString("cursor",
			mcp.Description("Pagination cursor from previous response (optional)"),
		),
	)
	s.mcp.AddTool(listTool, s.handleEntityList)
}

// Tool handlers

// clientErrors are sentinel errors that are safe to expose to MCP clients
// because they describe client-side problems (bad input, not found, etc.)
// rather than internal server details.
var clientErrors = []error{
	storage.ErrNotFound,
	storage.ErrAlreadyExists,
	storage.ErrVersionConflict,
	conversation.ErrEmptyContent,
	conversation.ErrInvalidRole,
	conversation.ErrThreadNotFound,
	conversation.ErrNothingToSummarize,
	conversation.ErrSummarizerNotSet,
	knowledge.ErrEmptyContent,
	knowledge.ErrCollectionNotFound,
	knowledge.ErrDocumentNotFound,
	knowledge.ErrCollectionExists,
	knowledge.ErrEmbeddingRequired,
	knowledge.ErrEmbeddingFailed,
	knowledge.ErrInvalidChunkConfig,
	entity.ErrEntityNotFound,
	entity.ErrEmptyName,
	entity.ErrInvalidType,
	entity.ErrSelfMerge,
	entity.ErrEmbeddingRequired,
	entity.ErrEmptyQuery,
	entity.ErrEmptySourceID,
	ctxengine.ErrKeyNotFound,
	ctxengine.ErrVersionConflict,
	ctxengine.ErrInvalidMerge,
	ctxengine.ErrEmptyKey,
	embedding.ErrEmptyInput,
	embedding.ErrBatchTooLarge,
}

// toolError classifies an error from an engine or storage operation. If the
// error matches a known client-facing sentinel, its message is returned to
// the caller. Otherwise the error is logged server-side and a sanitized
// "internal server error" is returned, preventing SQL, URLs, and other
// internal details from leaking to MCP clients.
func toolError(err error) (*mcp.CallToolResult, error) {
	for _, sentinel := range clientErrors {
		if errors.Is(err, sentinel) {
			return mcp.NewToolResultError(err.Error()), nil
		}
	}
	observability.Error(context.Background(), "internal error in MCP tool handler",
		zap.Error(err))
	return mcp.NewToolResultError("internal server error"), nil
}

// argTypeName returns a human-readable type name for an MCP argument value.
func argTypeName(v any) string {
	switch v.(type) {
	case float64:
		return "number"
	case string:
		return "string"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// optInt extracts an optional integer argument. If the key is absent the
// default is returned. If the key is present but the value is not a number,
// a tool error result is returned.
func optInt(args map[string]any, key string, defaultVal int) (int, *mcp.CallToolResult) {
	v, ok := args[key]
	if !ok {
		return defaultVal, nil
	}
	f, ok := v.(float64)
	if !ok {
		return defaultVal, mcp.NewToolResultError(fmt.Sprintf("parameter %q must be a number, got %s", key, argTypeName(v)))
	}
	return int(f), nil
}

// optFloat extracts an optional float64 argument.
func optFloat(args map[string]any, key string, defaultVal float64) (float64, *mcp.CallToolResult) {
	v, ok := args[key]
	if !ok {
		return defaultVal, nil
	}
	f, ok := v.(float64)
	if !ok {
		return defaultVal, mcp.NewToolResultError(fmt.Sprintf("parameter %q must be a number, got %s", key, argTypeName(v)))
	}
	return f, nil
}

// optString extracts an optional string argument.
func optString(args map[string]any, key string, defaultVal string) (string, *mcp.CallToolResult) {
	v, ok := args[key]
	if !ok {
		return defaultVal, nil
	}
	s, ok := v.(string)
	if !ok {
		return defaultVal, mcp.NewToolResultError(fmt.Sprintf("parameter %q must be a string, got %s", key, argTypeName(v)))
	}
	return s, nil
}

// optStringPtr extracts an optional string argument as a pointer.
// Returns nil if the key is absent.
func optStringPtr(args map[string]any, key string) (*string, *mcp.CallToolResult) {
	v, ok := args[key]
	if !ok {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, mcp.NewToolResultError(fmt.Sprintf("parameter %q must be a string, got %s", key, argTypeName(v)))
	}
	return &s, nil
}

// optBool extracts an optional boolean argument.
func optBool(args map[string]any, key string, defaultVal bool) (bool, *mcp.CallToolResult) {
	v, ok := args[key]
	if !ok {
		return defaultVal, nil
	}
	b, ok := v.(bool)
	if !ok {
		return defaultVal, mcp.NewToolResultError(fmt.Sprintf("parameter %q must be a boolean, got %s", key, argTypeName(v)))
	}
	return b, nil
}

// maxExactJSONInt is the largest integer magnitude exactly representable in
// a float64 (2^53). JSON numbers decode to float64, so any integer-valued
// number above this bound may not equal what the client actually sent.
const maxExactJSONInt = float64(1) * float64(1<<53)

// validateJSONIntPrecision walks a decoded JSON value and rejects
// integer-valued numbers whose magnitude exceeds 2^53. JSON arguments
// decode every number to float64 before handler code runs, so such values
// cannot be trusted to round-trip (9007199254740993 arrives as
// 9007199254740992) — silently storing them would corrupt data such as
// Snowflake IDs. Fractional floats are unaffected: they are honest floats.
// Clients sending large integers are directed to use strings.
func validateJSONIntPrecision(v any, path string) *mcp.CallToolResult {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			childPath := path + "." + k
			if errResult := validateJSONIntPrecision(child, childPath); errResult != nil {
				return errResult
			}
		}
	case []any:
		for i, child := range val {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if errResult := validateJSONIntPrecision(child, childPath); errResult != nil {
				return errResult
			}
		}
	case float64:
		if val == math.Trunc(val) && math.Abs(val) > maxExactJSONInt {
			return mcp.NewToolResultError(fmt.Sprintf(
				"integer at %s exceeds 2^53 (%g) and cannot be transported safely as a JSON number; send large integers as strings",
				path, val))
		}
	}
	return nil
}

// parseExpectedVersion extracts the optional expected_version argument with
// precision guards: it drives optimistic-concurrency checks, so a value
// corrupted by float64 decoding (or a fractional value) must be rejected
// rather than silently compared against the wrong version. Versions are
// server-generated counters; anything above 2^53 is treated as unsafe.
func parseExpectedVersion(args map[string]any) (*int64, *mcp.CallToolResult) {
	v, ok := args["expected_version"]
	if !ok {
		return nil, nil
	}
	f, ok := v.(float64)
	if !ok {
		return nil, mcp.NewToolResultError(fmt.Sprintf(
			"parameter \"expected_version\" must be a number, got %s", argTypeName(v)))
	}
	if f != math.Trunc(f) {
		return nil, mcp.NewToolResultError(fmt.Sprintf(
			"parameter \"expected_version\" must be a whole number, got %v", f))
	}
	if math.Abs(f) > maxExactJSONInt {
		return nil, mcp.NewToolResultError(fmt.Sprintf(
			"parameter \"expected_version\" exceeds 2^53 (%v) and cannot be compared safely", int64(f)))
	}
	iv := int64(f)
	if iv <= 0 {
		return nil, nil
	}
	return &iv, nil
}

func (s *Server) handleConversationAppend(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	threadID, err := req.RequireString("thread_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	role, err := req.RequireString("role")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	content, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &conversation.AppendOpts{}
	args := req.GetArguments()

	if metadata, ok := args["metadata"].(map[string]any); ok {
		opts.Metadata = toStringMap(metadata)
	}

	maxLen, errResult := optInt(args, "max_content_length", 0)
	if errResult != nil {
		return errResult, nil
	}
	opts.MaxContentLength = maxLen

	result, err := s.conversation.Append(ctx, namespace, threadID, role, content, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleConversationHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	threadID, err := req.RequireString("thread_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &conversation.HistoryOpts{}
	args := req.GetArguments()

	lastN, errResult := optInt(args, "last_n", 0)
	if errResult != nil {
		return errResult, nil
	}
	opts.LastN = lastN

	opts.IncludeSummary, errResult = optBool(args, "include_summary", false)
	if errResult != nil {
		return errResult, nil
	}

	opts.Cursor, errResult = optString(args, "cursor", "")
	if errResult != nil {
		return errResult, nil
	}

	result, err := s.conversation.History(ctx, namespace, threadID, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleConversationSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &conversation.SearchOpts{}
	args := req.GetArguments()
	var errResult *mcp.CallToolResult

	opts.ThreadID, errResult = optStringPtr(args, "thread_id")
	if errResult != nil {
		return errResult, nil
	}

	opts.TopK, errResult = optInt(args, "top_k", 0)
	if errResult != nil {
		return errResult, nil
	}

	searchMode, errResult := optString(args, "search_mode", "")
	if errResult != nil {
		return errResult, nil
	}
	if searchMode != "" {
		opts.SearchMode = conversation.SearchMode(searchMode)
	}

	opts.Alpha, errResult = optFloat(args, "alpha", 0)
	if errResult != nil {
		return errResult, nil
	}

	result, err := s.conversation.Search(ctx, namespace, query, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleConversationSummarize(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	threadID, err := req.RequireString("thread_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &conversation.SummarizeOpts{}
	args := req.GetArguments()

	keepRecent, errResult := optInt(args, "keep_recent", 0)
	if errResult != nil {
		return errResult, nil
	}
	opts.KeepRecent = keepRecent

	result, err := s.conversation.Summarize(ctx, namespace, threadID, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleKnowledgeIngest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	collectionID, err := req.RequireString("collection_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	content, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &knowledge.IngestOpts{}
	args := req.GetArguments()
	var errResult *mcp.CallToolResult

	opts.Title, errResult = optString(args, "title", "")
	if errResult != nil {
		return errResult, nil
	}

	opts.ContentType, errResult = optString(args, "content_type", "")
	if errResult != nil {
		return errResult, nil
	}

	opts.Source, errResult = optString(args, "source", "")
	if errResult != nil {
		return errResult, nil
	}

	if metadata, ok := args["metadata"].(map[string]any); ok {
		opts.Metadata = toStringMap(metadata)
	}

	if chunkConfig, ok := args["chunk_config"].(map[string]any); ok {
		opts.ChunkConfig = parseChunkConfig(chunkConfig)
	}

	result, err := s.knowledge.Ingest(ctx, namespace, collectionID, content, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleKnowledgeSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &knowledge.SearchOpts{}
	args := req.GetArguments()
	var errResult *mcp.CallToolResult

	opts.CollectionID, errResult = optStringPtr(args, "collection_id")
	if errResult != nil {
		return errResult, nil
	}

	opts.TopK, errResult = optInt(args, "top_k", 0)
	if errResult != nil {
		return errResult, nil
	}

	opts.MinScore, errResult = optFloat(args, "min_score", 0)
	if errResult != nil {
		return errResult, nil
	}

	if filters, ok := args["filters"].(map[string]any); ok {
		opts.Filters = toStringMap(filters)
	}

	includeContext, errResult := optBool(args, "include_context", true)
	if errResult != nil {
		return errResult, nil
	}

	contextWindow, errResult := optInt(args, "context_window", 0)
	if errResult != nil {
		return errResult, nil
	}
	if includeContext {
		if contextWindow > 0 {
			opts.ContextWindow = contextWindow
		} else {
			opts.ContextWindow = 1 // default context window
		}
	}

	searchMode, errResult := optString(args, "search_mode", "")
	if errResult != nil {
		return errResult, nil
	}
	switch searchMode {
	case "hybrid":
		opts.SearchMode = knowledge.SearchModeHybrid
	case "text":
		opts.SearchMode = knowledge.SearchModeText
	default:
		opts.SearchMode = knowledge.SearchModeVector
	}

	opts.Alpha, errResult = optFloat(args, "alpha", 0)
	if errResult != nil {
		return errResult, nil
	}

	result, err := s.knowledge.Search(ctx, namespace, query, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleKnowledgeCollections(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	action, err := req.RequireString("action")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	switch action {
	case "list":
		collections, nextCursor, err := s.knowledge.ListCollections(ctx, namespace, "", 0)
		if err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{
			"collections": collections,
			"next_cursor": nextCursor,
		})

	case "create":
		name, ok := req.GetArguments()["name"].(string)
		if !ok || name == "" {
			return mcp.NewToolResultError("name is required for create action"), nil
		}

		opts := knowledge.CreateCollectionOpts{
			Name: name,
		}
		args := req.GetArguments()
		desc, errResult := optString(args, "description", "")
		if errResult != nil {
			return errResult, nil
		}
		opts.Description = desc
		if chunkConfig, ok := args["chunk_config"].(map[string]any); ok {
			opts.ChunkConfig = parseChunkConfig(chunkConfig)
		}

		result, err := s.knowledge.CreateCollection(ctx, namespace, opts)
		if err != nil {
			return toolError(err)
		}
		return jsonResult(result)

	case "delete":
		collectionID, ok := req.GetArguments()["collection_id"].(string)
		if !ok || collectionID == "" {
			return mcp.NewToolResultError("collection_id is required for delete action"), nil
		}

		err := s.knowledge.DeleteCollection(ctx, namespace, collectionID)
		if err != nil {
			return toolError(err)
		}
		return jsonResult(map[string]any{"deleted": true})

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action: %s", action)), nil
	}
}

func (s *Server) handleKnowledgeBulkIngest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	collectionID, err := req.RequireString("collection_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Parse documents array
	docsRaw, ok := req.GetArguments()["documents"].([]any)
	if !ok || len(docsRaw) == 0 {
		return mcp.NewToolResultError("documents array is required and cannot be empty"), nil
	}

	documents := make([]knowledge.BulkIngestDocument, 0, len(docsRaw))
	for i, docRaw := range docsRaw {
		docMap, ok := docRaw.(map[string]any)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("document %d: invalid format, expected object", i)), nil
		}

		content, ok := docMap["content"].(string)
		if !ok || content == "" {
			return mcp.NewToolResultError(fmt.Sprintf("document %d: content is required", i)), nil
		}

		doc := knowledge.BulkIngestDocument{
			Content: content,
		}

		if title, ok := docMap["title"].(string); ok {
			doc.Title = title
		}
		if source, ok := docMap["source"].(string); ok {
			doc.Source = source
		}
		if contentType, ok := docMap["content_type"].(string); ok {
			doc.ContentType = contentType
		}
		if metadata, ok := docMap["metadata"].(map[string]any); ok {
			doc.Metadata = toStringMap(metadata)
		}

		documents = append(documents, doc)
	}

	opts := &knowledge.BulkIngestOpts{
		ContinueOnError: true, // default
	}
	args := req.GetArguments()

	concurrency, errResult := optInt(args, "concurrency", 0)
	if errResult != nil {
		return errResult, nil
	}
	if concurrency > 10 {
		concurrency = 10 // Cap at 10
	}
	opts.Concurrency = concurrency

	opts.ContinueOnError, errResult = optBool(args, "continue_on_error", true)
	if errResult != nil {
		return errResult, nil
	}

	if chunkConfig, ok := args["chunk_config"].(map[string]any); ok {
		opts.ChunkConfig = parseChunkConfig(chunkConfig)
	}

	result, err := s.knowledge.BulkIngest(ctx, namespace, collectionID, documents, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleContextGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	key, err := req.RequireString("key")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &ctxengine.GetOpts{}
	args := req.GetArguments()
	var errResult *mcp.CallToolResult
	opts.RunID, errResult = optStringPtr(args, "run_id")
	if errResult != nil {
		return errResult, nil
	}

	result, err := s.context.Get(ctx, namespace, key, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleContextSet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	key, err := req.RequireString("key")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	value, ok := req.GetArguments()["value"]
	if !ok {
		return mcp.NewToolResultError("value is required"), nil
	}
	if errResult := validateJSONIntPrecision(value, "value"); errResult != nil {
		return errResult, nil
	}

	opts := &ctxengine.SetOpts{}
	args := req.GetArguments()
	var errResult *mcp.CallToolResult
	opts.RunID, errResult = optStringPtr(args, "run_id")
	if errResult != nil {
		return errResult, nil
	}
	ttlSeconds, errResult := optInt(args, "ttl_seconds", 0)
	if errResult != nil {
		return errResult, nil
	}
	if ttlSeconds > 0 {
		opts.TTL = time.Duration(ttlSeconds) * time.Second
	}
	opts.ExpectedVersion, errResult = parseExpectedVersion(args)
	if errResult != nil {
		return errResult, nil
	}

	result, err := s.context.Set(ctx, namespace, key, value, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleContextMerge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	key, err := req.RequireString("key")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	value, ok := req.GetArguments()["value"]
	if !ok {
		return mcp.NewToolResultError("value is required"), nil
	}
	if errResult := validateJSONIntPrecision(value, "value"); errResult != nil {
		return errResult, nil
	}

	opts := &ctxengine.MergeOpts{}
	args := req.GetArguments()
	strategy, errResult := optString(args, "strategy", "")
	if errResult != nil {
		return errResult, nil
	}
	if strategy != "" {
		opts.Strategy = types.MergeStrategy(strategy)
	}
	opts.RunID, errResult = optStringPtr(args, "run_id")
	if errResult != nil {
		return errResult, nil
	}
	opts.ExpectedVersion, errResult = parseExpectedVersion(args)
	if errResult != nil {
		return errResult, nil
	}

	result, err := s.context.Merge(ctx, namespace, key, value, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleContextList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &ctxengine.ListOpts{}
	args := req.GetArguments()
	var errResult *mcp.CallToolResult
	opts.Prefix, errResult = optStringPtr(args, "prefix")
	if errResult != nil {
		return errResult, nil
	}
	opts.RunID, errResult = optStringPtr(args, "run_id")
	if errResult != nil {
		return errResult, nil
	}
	opts.Cursor, errResult = optString(args, "cursor", "")
	if errResult != nil {
		return errResult, nil
	}
	opts.Limit, errResult = optInt(args, "limit", 0)
	if errResult != nil {
		return errResult, nil
	}

	result, err := s.context.List(ctx, namespace, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleContextHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	key, err := req.RequireString("key")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &ctxengine.HistoryOpts{}
	args := req.GetArguments()
	var errResult *mcp.CallToolResult
	opts.RunID, errResult = optStringPtr(args, "run_id")
	if errResult != nil {
		return errResult, nil
	}
	opts.Cursor, errResult = optString(args, "cursor", "")
	if errResult != nil {
		return errResult, nil
	}
	opts.Limit, errResult = optInt(args, "limit", 0)
	if errResult != nil {
		return errResult, nil
	}

	result, err := s.context.History(ctx, namespace, key, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleEntityQuery(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	args := req.GetArguments()

	mentionLimit := 10

	includeMentions, errResult := optBool(args, "include_mentions", true)
	if errResult != nil {
		return errResult, nil
	}
	if !includeMentions {
		mentionLimit = 0
	}
	mentionLimit, errResult = optInt(args, "mention_limit", mentionLimit)
	if errResult != nil {
		return errResult, nil
	}

	result, err := s.entity.Query(ctx, namespace, name, mentionLimit)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleEntitySearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &entity.SearchOpts{}
	args := req.GetArguments()
	var errResult *mcp.CallToolResult
	opts.EntityType, errResult = parseEntityTypeArg(args)
	if errResult != nil {
		return errResult, nil
	}
	opts.TopK, errResult = optInt(args, "top_k", 0)
	if errResult != nil {
		return errResult, nil
	}

	searchMode, errResult := optString(args, "search_mode", "")
	if errResult != nil {
		return errResult, nil
	}
	switch searchMode {
	case "hybrid":
		opts.SearchMode = entity.SearchModeHybrid
	case "text":
		opts.SearchMode = entity.SearchModeText
	default:
		opts.SearchMode = entity.SearchModeVector
	}

	opts.Alpha, errResult = optFloat(args, "alpha", 0)
	if errResult != nil {
		return errResult, nil
	}

	result, err := s.entity.Search(ctx, namespace, query, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleEntityRelationships(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	entityName, err := req.RequireString("entity_name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// First resolve the entity name to get entity ID
	ent, err := s.entity.Resolve(ctx, namespace, entityName)
	if err != nil {
		return toolError(err)
	}

	opts := &entity.GetRelationshipsOpts{}
	args := req.GetArguments()
	var errResult *mcp.CallToolResult
	opts.RelationType, errResult = optStringPtr(args, "relation_type")
	if errResult != nil {
		return errResult, nil
	}
	direction, errResult := optString(args, "direction", "")
	if errResult != nil {
		return errResult, nil
	}
	if direction != "" {
		opts.Direction = types.RelationshipDirection(direction)
	}

	result, err := s.entity.GetRelationships(ctx, namespace, ent.ID, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleEntityUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	entityName, err := req.RequireString("entity_name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// First resolve the entity name to get entity ID
	ent, err := s.entity.Resolve(ctx, namespace, entityName)
	if err != nil {
		return toolError(err)
	}

	// Build update options
	opts := entity.UpdateOpts{}
	if attributes, ok := req.GetArguments()["attributes"].(map[string]any); ok {
		opts.Attributes = toStringMap(attributes)
	}

	// Update the entity
	result, err := s.entity.Update(ctx, namespace, ent.ID, opts)
	if err != nil {
		return toolError(err)
	}

	// Add aliases if provided
	if aliasesRaw, ok := req.GetArguments()["aliases"].([]any); ok {
		for _, a := range aliasesRaw {
			if alias, ok := a.(string); ok && alias != "" {
				if err := s.entity.AddAlias(ctx, namespace, ent.ID, alias); err != nil {
					// Log but don't fail for duplicate aliases
					continue
				}
			}
		}
	}

	return jsonResult(result)
}

func (s *Server) handleEntityMerge(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sourceEntity, err := req.RequireString("source_entity")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	targetEntity, err := req.RequireString("target_entity")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Resolve source entity
	source, err := s.entity.Resolve(ctx, namespace, sourceEntity)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("source entity: %v", err)), nil
	}

	// Resolve target entity
	target, err := s.entity.Resolve(ctx, namespace, targetEntity)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("target entity: %v", err)), nil
	}

	result, err := s.entity.Merge(ctx, namespace, source.ID, target.ID)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

func (s *Server) handleEntityList(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	namespace, err := req.RequireString("namespace")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.checkNamespace(namespace); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &entity.ListOpts{}
	args := req.GetArguments()
	entityType, errResult := parseEntityTypeArg(args)
	if errResult != nil {
		return errResult, nil
	}
	opts.EntityType = entityType
	sortBy, errResult := optString(args, "sort_by", "")
	if errResult != nil {
		return errResult, nil
	}
	if sortBy != "" {
		opts.SortBy = types.EntitySortBy(sortBy)
	}
	opts.Limit, errResult = optInt(args, "limit", 0)
	if errResult != nil {
		return errResult, nil
	}
	opts.Cursor, errResult = optString(args, "cursor", "")
	if errResult != nil {
		return errResult, nil
	}

	result, err := s.entity.List(ctx, namespace, opts)
	if err != nil {
		return toolError(err)
	}

	return jsonResult(result)
}

// Helper functions

func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func parseChunkConfig(m map[string]any) *types.ChunkConfig {
	cfg := &types.ChunkConfig{}
	strategy, errResult := optString(m, "strategy", "")
	if errResult != nil {
		return cfg
	}
	cfg.Strategy = strategy
	maxTokens, errResult := optInt(m, "max_tokens", 0)
	if errResult != nil {
		return cfg
	}
	cfg.MaxTokens = maxTokens
	overlap, errResult := optInt(m, "overlap", 0)
	if errResult != nil {
		return cfg
	}
	cfg.Overlap = overlap
	return cfg
}

// toStringMap converts map[string]any to map[string]string.
func toStringMap(m map[string]any) map[string]string {
	result := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			result[k] = s
		} else {
			// Convert other types to string via JSON
			if data, err := json.Marshal(v); err == nil {
				result[k] = string(data)
			}
		}
	}
	return result
}

// mapEntityType converts an API entity type string to the internal
// types.EntityType. All seven advertised values (person, organization,
// product, location, concept, event, other) are real internal types.
// Unknown values return ok=false so callers reject them instead of
// silently remapping to an unrelated type.
func mapEntityType(frdType string) (types.EntityType, bool) {
	switch strings.ToLower(strings.TrimSpace(frdType)) {
	case "person":
		return types.EntityTypePerson, true
	case "organization":
		return types.EntityTypeOrganization, true
	case "product":
		return types.EntityTypeProduct, true
	case "location":
		return types.EntityTypeLocation, true
	case "concept":
		return types.EntityTypeConcept, true
	case "event":
		return types.EntityTypeEvent, true
	case "other":
		return types.EntityTypeOther, true
	default:
		return "", false
	}
}

// parseEntityTypeArg extracts and validates the optional "type" argument,
// returning a tool error when present but not one of the supported values.
func parseEntityTypeArg(args map[string]any) (*types.EntityType, *mcp.CallToolResult) {
	typeStr, errResult := optString(args, "type", "")
	if errResult != nil {
		return nil, errResult
	}
	if typeStr == "" {
		return nil, nil
	}
	t, ok := mapEntityType(typeStr)
	if !ok {
		return nil, mcp.NewToolResultError(fmt.Sprintf(
			"parameter \"type\" has unsupported value %q (supported: person, organization, product, location, concept, event, other)", typeStr))
	}
	return &t, nil
}
