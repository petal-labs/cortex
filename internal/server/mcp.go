// Package server implements the MCP (Model Context Protocol) server for Cortex.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/knowledge"
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
		mcp.WithDescription("Semantic search across conversation history in a namespace."),
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
	)
	s.mcp.AddTool(searchTool, s.handleConversationSearch)
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

	// Optional metadata
	if metadata, ok := req.GetArguments()["metadata"].(map[string]any); ok {
		opts.Metadata = toStringMap(metadata)
	}

	// Optional max_content_length
	if maxLen, ok := req.GetArguments()["max_content_length"].(float64); ok {
		opts.MaxContentLength = int(maxLen)
	}

	result, err := s.conversation.Append(ctx, namespace, threadID, role, content, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

	if lastN, ok := req.GetArguments()["last_n"].(float64); ok {
		opts.LastN = int(lastN)
	}

	if includeSummary, ok := req.GetArguments()["include_summary"].(bool); ok {
		opts.IncludeSummary = includeSummary
	}

	if cursor, ok := req.GetArguments()["cursor"].(string); ok {
		opts.Cursor = cursor
	}

	result, err := s.conversation.History(ctx, namespace, threadID, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

	if threadID, ok := req.GetArguments()["thread_id"].(string); ok {
		opts.ThreadID = &threadID
	}

	if topK, ok := req.GetArguments()["top_k"].(float64); ok {
		opts.TopK = int(topK)
	}

	result, err := s.conversation.Search(ctx, namespace, query, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

	if title, ok := req.GetArguments()["title"].(string); ok {
		opts.Title = title
	}

	if contentType, ok := req.GetArguments()["content_type"].(string); ok {
		opts.ContentType = contentType
	}

	if source, ok := req.GetArguments()["source"].(string); ok {
		opts.Source = source
	}

	if metadata, ok := req.GetArguments()["metadata"].(map[string]any); ok {
		opts.Metadata = toStringMap(metadata)
	}

	if chunkConfig, ok := req.GetArguments()["chunk_config"].(map[string]any); ok {
		opts.ChunkConfig = parseChunkConfig(chunkConfig)
	}

	result, err := s.knowledge.Ingest(ctx, namespace, collectionID, content, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

	if collectionID, ok := req.GetArguments()["collection_id"].(string); ok {
		opts.CollectionID = &collectionID
	}

	if topK, ok := req.GetArguments()["top_k"].(float64); ok {
		opts.TopK = int(topK)
	}

	if minScore, ok := req.GetArguments()["min_score"].(float64); ok {
		opts.MinScore = minScore
	}

	if filters, ok := req.GetArguments()["filters"].(map[string]any); ok {
		opts.Filters = toStringMap(filters)
	}

	// include_context controls whether to use context window
	// When true (default), use context_window value; when false, set to 0
	includeContext := true
	if v, ok := req.GetArguments()["include_context"].(bool); ok {
		includeContext = v
	}

	if contextWindow, ok := req.GetArguments()["context_window"].(float64); ok && includeContext {
		opts.ContextWindow = int(contextWindow)
	} else if includeContext {
		opts.ContextWindow = 1 // default context window
	}

	result, err := s.knowledge.Search(ctx, namespace, query, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
			return mcp.NewToolResultError(err.Error()), nil
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
		if desc, ok := req.GetArguments()["description"].(string); ok {
			opts.Description = desc
		}
		if chunkConfig, ok := req.GetArguments()["chunk_config"].(map[string]any); ok {
			opts.ChunkConfig = parseChunkConfig(chunkConfig)
		}

		result, err := s.knowledge.CreateCollection(ctx, namespace, opts)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(result)

	case "delete":
		collectionID, ok := req.GetArguments()["collection_id"].(string)
		if !ok || collectionID == "" {
			return mcp.NewToolResultError("collection_id is required for delete action"), nil
		}

		err := s.knowledge.DeleteCollection(ctx, namespace, collectionID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"deleted": true})

	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action: %s", action)), nil
	}
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
	if runID, ok := req.GetArguments()["run_id"].(string); ok {
		opts.RunID = &runID
	}

	result, err := s.context.Get(ctx, namespace, key, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

	opts := &ctxengine.SetOpts{}
	if runID, ok := req.GetArguments()["run_id"].(string); ok {
		opts.RunID = &runID
	}
	if ttlSeconds, ok := req.GetArguments()["ttl_seconds"].(float64); ok {
		opts.TTL = time.Duration(ttlSeconds) * time.Second
	}
	if expectedVersion, ok := req.GetArguments()["expected_version"].(float64); ok {
		v := int64(expectedVersion)
		opts.ExpectedVersion = &v
	}

	result, err := s.context.Set(ctx, namespace, key, value, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

	opts := &ctxengine.MergeOpts{}
	if strategy, ok := req.GetArguments()["strategy"].(string); ok {
		opts.Strategy = types.MergeStrategy(strategy)
	}
	if runID, ok := req.GetArguments()["run_id"].(string); ok {
		opts.RunID = &runID
	}
	if expectedVersion, ok := req.GetArguments()["expected_version"].(float64); ok {
		v := int64(expectedVersion)
		opts.ExpectedVersion = &v
	}

	result, err := s.context.Merge(ctx, namespace, key, value, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
	if prefix, ok := req.GetArguments()["prefix"].(string); ok {
		opts.Prefix = &prefix
	}
	if runID, ok := req.GetArguments()["run_id"].(string); ok {
		opts.RunID = &runID
	}
	if cursor, ok := req.GetArguments()["cursor"].(string); ok {
		opts.Cursor = cursor
	}
	if limit, ok := req.GetArguments()["limit"].(float64); ok {
		opts.Limit = int(limit)
	}

	result, err := s.context.List(ctx, namespace, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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

	// Default include_mentions to true, mention_limit to 10
	mentionLimit := 10

	if v, ok := req.GetArguments()["include_mentions"].(bool); ok && !v {
		mentionLimit = 0
	}
	if v, ok := req.GetArguments()["mention_limit"].(float64); ok {
		mentionLimit = int(v)
	}

	result, err := s.entity.Query(ctx, namespace, name, mentionLimit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
	if entityType, ok := req.GetArguments()["type"].(string); ok {
		t := mapEntityType(entityType)
		opts.EntityType = &t
	}
	if topK, ok := req.GetArguments()["top_k"].(float64); ok {
		opts.TopK = int(topK)
	}

	result, err := s.entity.Search(ctx, namespace, query, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &entity.GetRelationshipsOpts{}
	if relationType, ok := req.GetArguments()["relation_type"].(string); ok {
		opts.RelationType = &relationType
	}
	if direction, ok := req.GetArguments()["direction"].(string); ok {
		opts.Direction = types.RelationshipDirection(direction)
	}

	result, err := s.entity.GetRelationships(ctx, namespace, ent.ID, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Build update options
	opts := entity.UpdateOpts{}
	if attributes, ok := req.GetArguments()["attributes"].(map[string]any); ok {
		opts.Attributes = toStringMap(attributes)
	}

	// Update the entity
	result, err := s.entity.Update(ctx, namespace, ent.ID, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
		return mcp.NewToolResultError(err.Error()), nil
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
	if entityType, ok := req.GetArguments()["type"].(string); ok {
		t := mapEntityType(entityType)
		opts.EntityType = &t
	}
	if sortBy, ok := req.GetArguments()["sort_by"].(string); ok {
		opts.SortBy = types.EntitySortBy(sortBy)
	}
	if limit, ok := req.GetArguments()["limit"].(float64); ok {
		opts.Limit = int(limit)
	}
	if cursor, ok := req.GetArguments()["cursor"].(string); ok {
		opts.Cursor = cursor
	}

	result, err := s.entity.List(ctx, namespace, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
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
	if strategy, ok := m["strategy"].(string); ok {
		cfg.Strategy = strategy
	}
	if maxTokens, ok := m["max_tokens"].(float64); ok {
		cfg.MaxTokens = int(maxTokens)
	}
	if overlap, ok := m["overlap"].(float64); ok {
		cfg.Overlap = int(overlap)
	}
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

// mapEntityType converts FRD entity types to internal types.
// FRD uses: person, organization, product, location, concept, event, other
// Internal types: person, organization, product, location, concept
func mapEntityType(frdType string) types.EntityType {
	switch strings.ToLower(frdType) {
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
		// Event not in internal types, use concept as closest match
		return types.EntityTypeConcept
	default:
		// "other" and unknown types default to product
		return types.EntityTypeProduct
	}
}
