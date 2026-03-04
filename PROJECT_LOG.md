# Cortex Project Log

## Project Overview
**Name:** Cortex - PetalFlow Memory & Knowledge Service
**Type:** Go service (MCP server)
**Created:** 2026-03-04
**Status:** Development Phase - Phase 1

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
- `pkg/types/*.go` - Shared type definitions

---

## Implementation Status

### Phase 1: Foundation
- [x] Project Structure & Go Module
- [x] Storage Interface & SQLite Backend
- [x] Embedding Provider (Iris Integration)
- [x] Conversation Memory Engine
- [x] Knowledge Store Engine
- [ ] Workflow Context Engine
- [ ] Entity Memory Engine
- [ ] MCP Server

### Phase 2: Advanced Features
- [ ] Conversation Summarization
- [ ] Semantic Chunking
- [ ] Context Version History
- [ ] Entity Extraction Pipeline
- [ ] Embedding Cache
- [ ] CLI Commands

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
