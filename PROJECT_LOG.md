# Cortex Project Log

## Project Overview
**Name:** Cortex - PetalFlow Memory & Knowledge Service
**Type:** Go service (MCP server)
**Created:** 2026-03-04
**Status:** Development Phase - Phase 1 Complete

## Purpose
Cortex provides persistent context, vector-backed knowledge retrieval, and conversation memory for PetalFlow agents. It implements four memory primitives:
1. Conversation Memory - Agent dialogue history
2. Knowledge Store - Vector-indexed documents (RAG)
3. Workflow Context - Shared state across tasks/runs
4. Entity Memory - Auto-extracted knowledge graph

## Technical Stack
- **Language:** Go
- **Storage:** SQLite + vec0 (default), PostgreSQL + pgvector (production)
- **Protocol:** MCP (Model Context Protocol)
- **Embeddings:** Via Iris service

## FRD Location
`.project/petalflow-cortex-frd.md`

---

## Session Log

### 2026-03-04 - Initial Planning & Phase 1 Start
- Read FRD document (2182 lines)
- Created detailed implementation plan (88 tasks across 4 phases)
- Created task checklist for tracking progress
- Project initialized

**Milestone 1.1 Completed:**
- Go module initialized
- Directory structure created
- Core dependencies added (cobra, viper, zap, uuid, sqlite3, pgx)
- Configuration system implemented with tests
- Shared type definitions created for all 4 memory primitives

**Milestone 1.2 In Progress:**
- Storage backend interface defined
- SQLite backend scaffold created
- Schema migrations implemented (all 15 tables)
- Conversation storage implemented with full test coverage
  - AppendMessage, GetMessages, ListThreads, GetThread
  - UpdateThread, DeleteThread, StoreMessageEmbedding
  - MarkMessagesSummarized
- Knowledge storage implemented with full test coverage
  - CreateCollection, GetCollection, ListCollections, DeleteCollection
  - InsertDocument, GetDocument, DeleteDocument
  - InsertChunks (batch with embeddings), GetAdjacentChunks
  - CollectionStats, SearchChunks (placeholder for vec0)
- Context storage implemented with full test coverage
  - GetContext, SetContext (with optimistic concurrency)
  - ListContextKeys (with prefix filtering), DeleteContext
  - GetContextHistory (version audit trail)
  - CleanupExpiredContext, CleanupRunContext
- Entity storage implemented with full test coverage
  - UpsertEntity, GetEntityByID, GetEntityByName, ResolveAlias
  - ListEntities (with type filter and sorting), DeleteEntity
  - InsertMention, GetMentions, MergeEntities
  - UpsertRelationship, GetRelationships, RegisterAlias
  - StoreEntityEmbedding, SearchEntities
  - EnqueueExtraction, DequeueExtraction, CompleteExtraction, GetExtractionQueueStats

**Milestone 1.2 Completed:**
- sqlite-vec extension integrated for vector similarity search
  - Added sqlite-vec-go-bindings CGO dependency
  - Implemented binary encoding for embeddings (little-endian float32)
  - SearchMessages: semantic search across conversation messages
  - SearchChunks: semantic search across knowledge chunks with metadata filters
  - SearchEntities: semantic search across entity summaries
  - All search methods use vec_distance_cosine() for brute-force KNN
  - Cosine distance converted to similarity score (0-1 range)
  - Support for TopK, MinScore filtering, and type-specific filters
- Full test coverage added for vector search functionality

**Milestone 1.3 Completed:**
- Embedding provider interface defined
  - Provider interface with Embed() and EmbedBatch() methods
  - EmbeddingRequest/Response types for Iris API contract
- Iris embedding client implemented
  - HTTP client for Iris /embeddings endpoint
  - Batch support with configurable size limits
  - Handles empty inputs and error responses
- LRU embedding cache implemented
  - CachedProvider wraps any Provider with caching
  - SHA-256 text hashing for cache keys
  - Cache immutability to prevent mutation issues
  - Stats and clear operations for monitoring
- Full test coverage with mock server tests

**Milestone 1.4 Completed:**
- Conversation engine implemented with all core operations
  - Append: message creation with auto thread creation, content truncation, embedding generation
  - History: retrieve messages with cursor pagination, optional summary
  - Search: semantic search across messages using embeddings
  - Clear: delete thread and all messages
  - ListThreads: paginated thread listing
  - GetThread/UpdateThread: thread metadata management
  - MarkSummarized: track summarized messages
- Role validation (user, assistant, system, tool)
- Config-driven semantic search (can be disabled)
- Full test coverage (12 tests)

**Milestone 1.5 Completed:**
- Knowledge store engine implemented with all core operations
  - Ingest: document ingestion with chunking and embedding generation
  - Search: semantic search across chunks with context window expansion
  - Get/Delete: document retrieval and removal
  - Collections: create, list, get, delete with stats
- Text chunking strategies implemented
  - Fixed: word-based chunking with configurable overlap
  - Sentence: splits on sentence boundaries, cascades to fixed for long sentences
  - Paragraph: splits on double newlines, cascades to sentence for long paragraphs
- Batch embedding generation during ingest
- Graceful degradation when embeddings fail
- Full test coverage (44 tests across chunker and engine)

**Milestone 1.6 Completed:**
- Workflow context engine implemented with all core operations
  - Get/Set: key-value storage with version tracking
  - Optimistic concurrency control via expected version
  - TTL support with automatic expiration
  - Run-scoped vs persistent context separation
  - Key listing with prefix filtering and pagination
  - Version history retrieval
- Merge operations with multiple strategies
  - replace: simple value replacement
  - append: array concatenation
  - max/min: numeric comparison
  - sum: numeric accumulation
  - deep_merge: recursive map merging with array strategy options
- Full test coverage (31 tests)

**Milestone 1.7 Completed:**
- Entity memory engine implemented with all core operations
  - Create/Get/Update/Delete: full CRUD with validation
  - GetByName: lookup by canonical name
  - Resolve: name-or-alias resolution (tries canonical name first, then aliases)
  - List: paginated listing with optional type filter
  - Search: semantic search across entity summaries using embeddings
- Alias management implemented
  - AddAlias: register alternative names for entities
  - Resolution prioritizes canonical name over aliases
- Relationship management implemented
  - AddRelationship: create typed relationships between entities
  - GetRelationships: retrieve relationships with direction filtering
  - Supports bidirectional and unidirectional relationships
- Mention tracking implemented
  - RecordMention: track entity mentions with source references
  - GetMentions: retrieve mentions with pagination
  - Automatic mention count tracking on entities
- Entity merging implemented
  - Merge: combine two entities, preserving aliases and relationships
  - Automatic alias creation from source entity name
  - Attribute/metadata merging from both entities
- Extraction queue implemented
  - EnqueueExtraction: add messages for entity extraction processing
  - DequeueExtraction: retrieve next pending item for processing
  - CompleteExtraction: mark items as processed (completed/failed)
  - Stats: queue metrics for monitoring
- Query operation for combined entity retrieval
  - Returns entity with relationships and recent mentions in single call
- Full test coverage (28 tests)

**Milestone 1.8 Completed:**
- MCP Server implemented with stdio transport
  - Uses mark3labs/mcp-go v0.44.1 library
  - All 16 tool definitions from FRD section 4.1 implemented
  - Functional options pattern for tool parameters
- Conversation tools implemented
  - conversation_append: add messages to threads
  - conversation_history: retrieve messages with pagination
  - conversation_search: semantic search across messages
- Knowledge tools implemented
  - knowledge_ingest: document ingestion with chunking
  - knowledge_search: semantic search with context window
  - knowledge_collections: create/list/delete collections
- Context tools implemented
  - context_get: retrieve values by key
  - context_set: store values with TTL and versioning
  - context_merge: merge values with strategy
  - context_list: list keys with prefix filtering
- Entity tools implemented
  - entity_query: lookup by name/alias with mentions
  - entity_search: semantic search across entities
  - entity_relationships: get entity relationships
  - entity_update: modify entity attributes/aliases
  - entity_merge: combine duplicate entities
  - entity_list: paginated listing with filters
- Namespace enforcement via allowedNamespace config
  - Validates namespace on every tool call
  - Supports open mode (all namespaces) or restricted mode
- JSON result serialization for all tool responses
- Full test coverage (16 tests)

**Files Created:**
- `.project/IMPLEMENTATION_PLAN.md` - Detailed plan with task descriptions
- `.project/TASK_CHECKLIST.md` - Quick-reference checklist
- `go.mod`, `go.sum` - Go module
- `cmd/cortex/main.go` - CLI entrypoint
- `internal/cmd/root.go` - Cobra root command
- `internal/config/config.go` - Configuration system
- `internal/config/config_test.go` - Config tests
- `internal/storage/backend.go` - Storage interface
- `internal/storage/sqlite/sqlite.go` - SQLite backend
- `internal/storage/sqlite/migrations.go` - Schema migrations
- `internal/storage/sqlite/conversation.go` - Conversation storage ops
- `internal/storage/sqlite/conversation_test.go` - Conversation tests
- `internal/storage/sqlite/knowledge.go` - Knowledge storage ops
- `internal/storage/sqlite/knowledge_test.go` - Knowledge tests
- `internal/storage/sqlite/context.go` - Context storage ops
- `internal/storage/sqlite/context_test.go` - Context tests
- `internal/storage/sqlite/entity.go` - Entity storage ops
- `internal/storage/sqlite/entity_test.go` - Entity tests
- `internal/storage/sqlite/vector_search_test.go` - Vector search tests
- `internal/embedding/provider.go` - Embedding provider interface
- `internal/embedding/iris.go` - Iris HTTP client implementation
- `internal/embedding/cache.go` - LRU embedding cache
- `internal/embedding/provider_test.go` - Embedding tests
- `internal/conversation/engine.go` - Conversation memory engine
- `internal/conversation/engine_test.go` - Conversation engine tests
- `internal/knowledge/chunker.go` - Text chunking strategies
- `internal/knowledge/chunker_test.go` - Chunker tests
- `internal/knowledge/engine.go` - Knowledge store engine
- `internal/knowledge/engine_test.go` - Knowledge engine tests
- `internal/context/engine.go` - Workflow context engine
- `internal/context/engine_test.go` - Context engine tests
- `internal/entity/engine.go` - Entity memory engine
- `internal/entity/engine_test.go` - Entity engine tests
- `internal/server/mcp.go` - MCP server implementation
- `internal/server/mcp_test.go` - MCP server tests
- `pkg/types/*.go` - Shared type definitions

**Milestone 1.9 Completed:**
- CLI serve command implemented
  - Wires config, storage, embedding, and all engines
  - Supports --namespace flag for restricted mode
  - Supports --mcp flag (default true)
- `internal/cmd/serve.go` - serve command implementation

**Milestone 2.1 Completed:**
- Summarization client implemented
  - HTTP client for Iris /v1/completions endpoint
  - SummarizeMessages convenience method
  - System prompt for conversation summarization
- Conversation engine Summarize operation
  - Splits messages into "to summarize" and "to keep"
  - Stores summary in thread record
  - Marks summarized messages
  - Rolling summary incorporates previous summaries
- Auto-summarization trigger in History method
  - Checks message count vs threshold
  - Triggers summarization automatically when exceeded
  - SkipAutoSummarize option for internal use
- MCP handler for conversation_summarize
  - Accepts namespace, thread_id, keep_recent parameters
  - Returns summary and message counts
- Serve command wires up summarizer
  - Creates summarization client when Iris is configured
  - Sets summarizer on conversation engine
- Full test coverage (new tests: 10+)

**New Files:**
- `internal/summarization/client.go` - LLM completion client
- `internal/summarization/client_test.go` - Client tests

**Milestone 2.2 Completed:**
- Semantic chunking implemented with embedding-based topic detection
  - SemanticChunker uses sliding windows of sentences
  - Embeds each window and computes cosine similarity between adjacent windows
  - Detects breakpoints where similarity drops below threshold
  - Configurable via WithSimilarityThreshold() and WithWindowSize()
  - Falls back to sentence chunking for short content or embedding failures
- Integrated with knowledge engine
  - Engine dispatches to SemanticChunker when strategy is "semantic"
  - Graceful fallback to sentence chunking on errors
  - Semantic chunker only initialized when embedding provider is available
- Full test coverage (15+ new tests)

**New Files:**
- `internal/knowledge/semantic_chunker.go` - Semantic chunker implementation
- `internal/knowledge/semantic_chunker_test.go` - Semantic chunker tests

**Milestone 2.3 Completed:**
- Entity extraction client implemented
  - LLM-powered entity extraction via Iris completion API
  - JSON response parsing with validation
  - Entity normalization and filtering
  - Co-mentioned entity relationship extraction
- Name resolver implemented
  - Blocking strategy (first 3 chars, initialism matching)
  - Fuzzy matching using Levenshtein distance
  - Multi-phase resolution: exact → alias → fuzzy → new
- Queue processor implemented
  - Background worker with configurable polling interval
  - Batch processing with retry and exponential backoff
  - Support for extraction modes: off, sampled, whitelist, full
  - ProcessSingle for on-demand extraction
  - ExtractionEnqueuerAdapter for bridging engine interfaces
- Extraction hooks added to engines
  - Conversation engine queues messages for extraction on append
  - Knowledge engine queues chunks for extraction on ingest
  - Fire-and-forget pattern - extraction failures don't block operations
- Serve command wiring
  - Creates extractor, resolver, queue processor when Iris configured
  - Sets extraction enqueuer on conversation and knowledge engines
  - Starts queue processor in background
- Full test coverage (35+ new tests)

**New Files:**
- `internal/entity/extractor.go` - Entity extraction client
- `internal/entity/extractor_test.go` - Extractor tests
- `internal/entity/resolver.go` - Name resolution with blocking/fuzzy
- `internal/entity/resolver_test.go` - Resolver tests
- `internal/entity/queue.go` - Queue processor with enqueuer adapter
- `internal/entity/queue_test.go` - Queue processor tests

**Milestone 2.4 Completed:**
- Context version history MCP handler implemented
  - `context_history` tool returns version history for a key
  - Supports run_id scoping, cursor pagination, and limit
- Version conflict detection already in place
  - `expected_version` parameter on context_set and context_merge
  - Returns version conflict error on mismatch
- Full test coverage (2 new tests)

**Modified Files:**
- `internal/server/mcp.go` - Added context_history tool and handler
- `internal/server/mcp_test.go` - Added context history tests

**Milestone 2.5 Completed:**
- CLI Management Commands implemented for all memory primitives
- Knowledge CLI: ingest, ingest-dir, search, stats, collections, create-collection
- Conversation CLI: history, append, search, list, clear, summarize
- Context CLI: get, set, delete, list, history, cleanup
- Entity CLI: create, get, delete, list, search, add-alias, add-relationship, merge, queue-stats
- Namespace CLI: stats, delete (with confirmation)
- All commands support --namespace flag for isolation
- JSON output support where applicable

**New Files:**
- `internal/cmd/knowledge.go` - Knowledge store CLI commands
- `internal/cmd/conversation.go` - Conversation memory CLI commands
- `internal/cmd/context.go` - Workflow context CLI commands
- `internal/cmd/entity.go` - Entity memory CLI commands
- `internal/cmd/namespace.go` - Namespace management CLI commands

---

## Implementation Status

### Phase 1: Foundation
- [x] Project Structure & Go Module
- [x] Storage Interface & SQLite Backend
- [x] Embedding Provider (Iris Integration)
- [x] Conversation Memory Engine
- [x] Knowledge Store Engine
- [x] Workflow Context Engine
- [x] Entity Memory Engine
- [x] MCP Server
- [x] CLI Serve Command

### Phase 2: Advanced Features
- [x] Conversation Summarization
- [x] Semantic Chunking
- [x] Entity Extraction Pipeline
- [x] Context Version History
- [x] CLI Commands
- [ ] Embedding Cache (deferred - already have basic LRU cache)

### Phase 3: Production Hardening
- [ ] pgvector Backend
- [ ] SSE Transport
- [ ] Prometheus Metrics
- [ ] Retention Policies

### Phase 4: Ecosystem Integration
- [ ] Hybrid Search
- [ ] Bulk Ingest
- [ ] PetalFlow UI Integration

---

## Known Issues
None yet.

## Notes
- Project follows the Cortex FRD specification exactly
- Each phase has verification tests before moving forward
