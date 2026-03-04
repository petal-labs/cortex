# Cortex Implementation Plan

## Overview

This document outlines the implementation plan for Cortex, broken into small, focused tasks. Each milestone includes verification steps to ensure correctness before proceeding.

---

## Phase 1: Foundation

### Milestone 1.1: Project Setup & Go Module
**Goal:** Establish project structure and dependencies

#### Tasks

**1.1.1 Initialize Go Module**
- Create `go.mod` with module path `github.com/petal-labs/cortex`
- Set Go version 1.22+
- Verification: `go mod verify` succeeds

**1.1.2 Create Directory Structure**
```
cortex/
├── cmd/
│   └── cortex/
│       └── main.go
├── internal/
│   ├── config/
│   ├── server/
│   ├── conversation/
│   ├── knowledge/
│   ├── context/
│   ├── entity/
│   ├── embedding/
│   └── storage/
├── pkg/
│   └── types/
├── go.mod
└── go.sum
```
- Verification: All directories exist with placeholder `.gitkeep` or initial files

**1.1.3 Add Core Dependencies**
- `github.com/spf13/cobra` - CLI framework
- `github.com/spf13/viper` - Configuration
- `github.com/mattn/go-sqlite3` - SQLite driver
- `modernc.org/sqlite` or CGO sqlite with vec0 extension
- `github.com/google/uuid` - UUID generation
- `go.uber.org/zap` - Structured logging
- Verification: `go mod tidy && go build ./...` succeeds

**1.1.4 Create Configuration System**
- Define `Config` struct in `internal/config/config.go`
- Support YAML file loading from `~/.cortex/config.yaml`
- Support environment variable overrides
- Include all config fields from FRD section 7.2
- Verification: Unit test loads sample config and validates defaults

**1.1.5 Create Shared Type Definitions**
- Define core types in `pkg/types/`:
  - `Message`, `Thread` (conversation)
  - `Document`, `Chunk`, `Collection`, `ChunkConfig` (knowledge)
  - `ContextEntry` (context)
  - `Entity`, `EntityMention`, `EntityRelationship` (entity)
- Verification: Types compile with proper JSON tags

---

### Milestone 1.2: Storage Interface & SQLite Backend
**Goal:** Define storage abstraction and implement SQLite+vec0

#### Tasks

**1.2.1 Define Storage Backend Interface**
- Create `internal/storage/backend.go` with `Backend` interface
- Include all methods from FRD section 5.1
- Define supporting types: `SearchOpts`, `ChunkResult`, `MessageResult`, etc.
- Verification: Interface compiles

**1.2.2 Create SQLite Backend Scaffold**
- Create `internal/storage/sqlite/sqlite.go`
- Implement constructor `NewSQLiteBackend(cfg Config) (*SQLiteBackend, error)`
- Handle database file creation in data directory
- Verification: Backend can be instantiated with temp directory

**1.2.3 Implement SQLite Schema Migrations**
- Create `internal/storage/sqlite/migrations.go`
- Define all tables from FRD section 5.2:
  - `threads`, `messages`, `message_embeddings`
  - `collections`, `documents`, `chunks`, `chunk_embeddings`
  - `context_entries`, `context_history`
  - `entities`, `entity_aliases`, `entity_embeddings`
  - `entity_mentions`, `entity_relationships`
  - `entity_extraction_queue`
- Implement `Migrate()` method
- Verification: Test creates database with all tables

**1.2.4 Implement SQLite Conversation Storage**
- Implement `AppendMessage`, `GetMessages`, `ListThreads`, `UpdateThread`, `DeleteThread`
- Skip `SearchMessages` (requires embeddings - later task)
- Verification: Unit tests for CRUD operations on messages/threads

**1.2.5 Implement SQLite Knowledge Storage**
- Implement `InsertDocument`, `InsertChunks`, `GetDocument`, `DeleteDocument`
- Implement `GetAdjacentChunks`, `ListCollections`, `CreateCollection`, `DeleteCollection`
- Implement `CollectionStats`
- Skip `SearchChunks` (requires embeddings - later task)
- Verification: Unit tests for document/chunk CRUD

**1.2.6 Implement SQLite Context Storage**
- Implement `GetContext`, `SetContext`, `ListContextKeys`, `DeleteContext`
- Implement `ContextHistory` (append-only log)
- Implement `CleanupExpired` for TTL entries
- Verification: Unit tests for context operations, TTL cleanup

**1.2.7 Implement SQLite Entity Storage**
- Implement `UpsertEntity`, `GetEntityByName`, `ResolveAlias`, `ListEntities`, `DeleteEntity`
- Implement `MergeEntities`, `InsertMention`, `GetMentions`
- Implement `UpsertRelationship`, `GetRelationships`, `RegisterAlias`
- Implement `EnqueueExtraction`, `DequeueExtraction`, `CompleteExtraction`
- Skip `SearchEntities` (requires embeddings - later task)
- Verification: Unit tests for entity CRUD and queue operations

**1.2.8 Integrate vec0 Extension for Vector Search**
- Add vec0 virtual tables to migrations
- Implement `SearchChunks` using vec0 similarity search
- Implement `SearchMessages` using vec0
- Implement `SearchEntities` using vec0
- Verification: Vector search returns results sorted by similarity score

---

### Milestone 1.3: Iris Embedding Integration
**Goal:** Connect to Iris for embedding generation

#### Tasks

**1.3.1 Create Embedding Provider Interface**
- Create `internal/embedding/provider.go`
- Define `Provider` interface with `Embed(text)` and `EmbedBatch(texts)`
- Verification: Interface compiles

**1.3.2 Implement Iris Embedding Client**
- Create `internal/embedding/iris.go`
- Implement HTTP client to Iris embedding endpoint
- Handle batch embedding requests
- Parse response and return `[]float32` vectors
- Verification: Integration test against mock Iris server

**1.3.3 Add Embedding Cache**
- Create `internal/embedding/cache.go`
- Implement LRU cache using `github.com/hashicorp/golang-lru/v2`
- Cache key = hash of input text
- Configurable cache size (default: 1000)
- Verification: Cache hit returns same embedding, cache eviction works

---

### Milestone 1.4: Conversation Memory Engine
**Goal:** Implement conversation memory logic layer

#### Tasks

**1.4.1 Create Conversation Engine**
- Create `internal/conversation/engine.go`
- Define `Engine` struct with storage backend and embedding provider dependencies
- Verification: Engine instantiates with dependencies

**1.4.2 Implement Append Operation**
- Implement `Append(namespace, threadID, role, content, metadata, maxContentLength)`
- Create thread if doesn't exist
- Truncate content if exceeds `maxContentLength`
- Generate embedding if `semantic_search_enabled`
- Store message and embedding
- Verification: Test append creates thread and message

**1.4.3 Implement History Operation**
- Implement `History(namespace, threadID, lastN, includeSummary, cursor)`
- Return messages in chronological order
- Prepend summary if available and requested
- Support cursor-based pagination
- Verification: Test retrieves correct messages in order

**1.4.4 Implement Search Operation**
- Implement `Search(namespace, query, threadID, topK)`
- Generate query embedding
- Search message embeddings via storage backend
- Return results with scores
- Verification: Test returns semantically similar messages

**1.4.5 Implement Clear Operation**
- Implement `Clear(namespace, threadID)`
- Delete all messages in thread
- Delete thread record
- Verification: Test clears all messages

**1.4.6 Implement Threads List Operation**
- Implement `ListThreads(namespace)`
- Return all threads in namespace with metadata
- Verification: Test lists threads correctly

---

### Milestone 1.5: Knowledge Store Engine
**Goal:** Implement knowledge store logic layer with chunking

#### Tasks

**1.5.1 Create Knowledge Engine**
- Create `internal/knowledge/engine.go`
- Define `Engine` struct with dependencies
- Verification: Engine instantiates

**1.5.2 Implement Fixed Chunking Strategy**
- Create `internal/knowledge/chunker.go`
- Implement `FixedChunker` - split by token count with overlap
- Use simple word-based tokenization (or tiktoken if available)
- Verification: Test chunks document correctly with overlap

**1.5.3 Implement Sentence Chunking Strategy**
- Implement `SentenceChunker` - split on sentence boundaries
- Group sentences up to max token limit
- Verification: Test preserves sentence boundaries

**1.5.4 Implement Paragraph Chunking Strategy**
- Implement `ParagraphChunker` - split on `\n\n`
- Verification: Test splits on paragraph boundaries

**1.5.5 Implement Ingest Operation**
- Implement `Ingest(namespace, collectionID, title, content, contentType, source, metadata, chunkConfig)`
- Create collection if doesn't exist
- Chunk document using configured strategy
- Generate embeddings for all chunks (batch)
- Store document and chunks
- Verification: Test ingests document with correct chunks

**1.5.6 Implement Search Operation**
- Implement `Search(namespace, query, collectionID, topK, minScore, filters, includeContext, contextWindow)`
- Generate query embedding
- Search chunk embeddings
- Apply metadata filters (post-retrieval)
- Fetch adjacent chunks if requested
- Verification: Test returns relevant chunks with context

**1.5.7 Implement Get and Delete Operations**
- Implement `Get(namespace, docID)` - retrieve document
- Implement `Delete(namespace, docID)` - delete document and chunks
- Verification: Test CRUD operations

**1.5.8 Implement Collections Management**
- Implement `ListCollections(namespace)`
- Implement `CreateCollection(namespace, name, description, chunkConfig)`
- Implement `DeleteCollection(namespace, collectionID)`
- Implement `Stats(namespace, collectionID)`
- Verification: Test collection operations

---

### Milestone 1.6: Workflow Context Engine
**Goal:** Implement workflow context logic layer with merge strategies

#### Tasks

**1.6.1 Create Context Engine**
- Create `internal/context/engine.go`
- Define `Engine` struct with storage backend dependency
- Verification: Engine instantiates

**1.6.2 Implement Get and Set Operations**
- Implement `Get(namespace, key, runID)`
- Implement `Set(namespace, key, value, runID, ttlSeconds, expectedVersion)`
- Handle optimistic concurrency via version check
- Verification: Test get/set with version tracking

**1.6.3 Implement Deep Merge Strategy**
- Create `internal/context/merge.go`
- Implement `DeepMerge(existing, new, arrayStrategy)` - recursive object merge
- Support array strategies: `concat`, `concat_unique`, `replace`
- Verification: Test deep merge with nested objects

**1.6.4 Implement Other Merge Strategies**
- Implement `Append` - treat as array, append new value
- Implement `Replace` - overwrite entirely
- Implement `Max`, `Min`, `Sum` - numeric reducers
- Verification: Test each merge strategy

**1.6.5 Implement Merge Operation**
- Implement `Merge(namespace, key, value, strategy, runID, expectedVersion, arrayStrategy)`
- Get existing value, apply merge strategy, set result
- Increment version
- Verification: Test merge with different strategies

**1.6.6 Implement List and Delete Operations**
- Implement `List(namespace, prefix, runID, cursor, limit)` - list keys
- Implement `Delete(namespace, key, runID)` - remove key
- Verification: Test list with prefix filter, deletion

**1.6.7 Implement History Operation**
- Implement `History(namespace, key, runID, cursor)` - version history
- Query context_history table
- Verification: Test shows all versions of a key

---

### Milestone 1.7: Entity Memory Engine (Core Operations)
**Goal:** Implement entity memory core operations (extraction in Phase 2)

#### Tasks

**1.7.1 Create Entity Engine**
- Create `internal/entity/engine.go`
- Define `Engine` struct with dependencies
- Verification: Engine instantiates

**1.7.2 Implement Query Operation**
- Implement `Query(namespace, name, includeMentions, mentionLimit)`
- Resolve name via alias lookup
- Return entity with summary, attributes, relationships
- Include recent mentions if requested
- Verification: Test queries entity by name and alias

**1.7.3 Implement Search Operation**
- Implement `Search(namespace, query, entityType, topK)`
- Generate query embedding
- Search entity summary embeddings
- Filter by type if provided
- Verification: Test semantic search across entities

**1.7.4 Implement Relationships Operation**
- Implement `Relationships(namespace, entityName, relationType, direction)`
- Get all relationships for entity
- Filter by type and direction
- Verification: Test retrieves relationships correctly

**1.7.5 Implement Update Operation**
- Implement `Update(namespace, entityName, attributes, aliases, entityType)`
- Update entity attributes (merge)
- Add new aliases
- Update type if provided
- Verification: Test updates entity attributes

**1.7.6 Implement Merge Operation**
- Implement `MergeEntities(namespace, sourceEntity, targetEntity)`
- Combine mentions from source into target
- Combine relationships (update source/target references)
- Merge attributes (target wins on conflicts)
- Add source name as alias of target
- Delete source entity
- Verification: Test merges two entities correctly

**1.7.7 Implement List and Delete Operations**
- Implement `List(namespace, entityType, sortBy, limit, cursor)`
- Implement `Delete(namespace, entityName)`
- Verification: Test list with filters and pagination

---

### Milestone 1.8: MCP Server Implementation
**Goal:** Implement MCP server exposing all tools

#### Tasks

**1.8.1 Create MCP Server Scaffold**
- Create `internal/server/mcp.go`
- Implement MCP protocol basics: initialize, shutdown
- Use stdio transport initially
- Verification: Server starts and responds to initialize

**1.8.2 Implement tools/list Handler**
- Return all 17 tools defined in FRD section 4.1
- Include proper inputSchema for each tool
- Verification: tools/list returns correct tool definitions

**1.8.3 Implement Conversation Tool Handlers**
- `conversation_append` → conversation engine
- `conversation_history` → conversation engine
- `conversation_search` → conversation engine
- Verification: MCP tool calls route to conversation engine

**1.8.4 Implement Knowledge Tool Handlers**
- `knowledge_ingest` → knowledge engine
- `knowledge_search` → knowledge engine
- `knowledge_collections` → knowledge engine
- Verification: MCP tool calls route to knowledge engine

**1.8.5 Implement Context Tool Handlers**
- `context_get` → context engine
- `context_set` → context engine
- `context_merge` → context engine
- `context_list` → context engine
- Verification: MCP tool calls route to context engine

**1.8.6 Implement Entity Tool Handlers**
- `entity_query` → entity engine
- `entity_search` → entity engine
- `entity_relationships` → entity engine
- `entity_update` → entity engine
- `entity_merge` → entity engine
- `entity_list` → entity engine
- Verification: MCP tool calls route to entity engine

**1.8.7 Implement Namespace Enforcement**
- Accept `allowed_namespace` during MCP initialize
- Reject tool calls for namespaces outside allowed scope
- Verification: Test namespace restriction works

---

### Milestone 1.9: CLI Implementation (Basic)
**Goal:** Implement basic CLI for serve command

#### Tasks

**1.9.1 Create CLI Framework**
- Create `cmd/cortex/main.go` with cobra root command
- Add version, help commands
- Verification: `cortex --help` works

**1.9.2 Implement Serve Command**
- Add `serve` command with `--mcp` flag
- Wire up config loading, storage, embedding provider, engines
- Start MCP server
- Verification: `cortex serve --mcp` starts server

**1.9.3 Implement Config Command**
- Add `config show` - display current config
- Add `config test` - verify Iris connectivity
- Verification: Config commands work

---

### Phase 1 Verification Checkpoint
**Goal:** Validate Phase 1 is complete and functional

#### Verification Tasks

**V1.1 End-to-End Conversation Test**
- Start Cortex MCP server
- Send `conversation_append` tool call
- Send `conversation_history` tool call
- Verify messages are persisted and retrieved
- Test semantic search across messages

**V1.2 End-to-End Knowledge Test**
- Start Cortex MCP server
- Send `knowledge_collections` (create)
- Send `knowledge_ingest` with sample document
- Send `knowledge_search` with query
- Verify relevant chunks returned with scores

**V1.3 End-to-End Context Test**
- Start Cortex MCP server
- Send `context_set` with JSON value
- Send `context_get` - verify value
- Send `context_merge` with deep_merge
- Verify merged result

**V1.4 End-to-End Entity Test**
- Start Cortex MCP server
- Manually create entity via `entity_update`
- Send `entity_query` - verify retrieval
- Send `entity_search` - verify semantic search
- Test `entity_merge` - verify deduplication

**V1.5 MCP Protocol Compliance Test**
- Test initialize/shutdown lifecycle
- Test tools/list response format
- Test error responses for invalid inputs
- Verify JSON-RPC message format

---

## Phase 2: Advanced Features

### Milestone 2.1: Conversation Summarization
**Goal:** Implement LLM-powered conversation summarization

#### Tasks

**2.1.1 Create Summarization Client**
- Create `internal/summarization/client.go`
- Implement Iris completion API client
- Define summarization system prompt from FRD
- Verification: Test generates summary from messages

**2.1.2 Implement Summarize Operation**
- Add `Summarize(namespace, threadID, keepRecent)` to conversation engine
- Separate messages into "to summarize" and "to keep"
- Call summarization client
- Store summary in thread record
- Mark summarized messages
- Verification: Test summarizes old messages

**2.1.3 Add Auto-Summarization Trigger**
- Check message count on `conversation.history`
- Trigger summarization if exceeds threshold
- Verification: Test auto-triggers summarization

**2.1.4 Add MCP Handler**
- Implement `conversation_summarize` tool handler
- Verification: MCP tool call triggers summarization

---

### Milestone 2.2: Semantic Chunking
**Goal:** Implement embedding-based semantic chunking

#### Tasks

**2.2.1 Implement Semantic Chunker**
- Create `SemanticChunker` in chunker.go
- Split into candidate chunks at sentence boundaries
- Compute embeddings for adjacent chunks
- Find natural breakpoints where similarity drops
- Verification: Test finds semantic boundaries

**2.2.2 Integrate with Knowledge Engine**
- Add `semantic` as chunking strategy option
- Verification: Ingest with semantic chunking works

---

### Milestone 2.3: Entity Extraction Pipeline
**Goal:** Implement async LLM-powered entity extraction

#### Tasks

**2.3.1 Create Entity Extractor**
- Create `internal/entity/extractor.go`
- Define extraction prompt from FRD section 3.4
- Parse LLM response into entity objects
- Verification: Test extracts entities from sample text

**2.3.2 Create Name Resolver**
- Create `internal/entity/resolver.go`
- Implement blocking strategy (first 3 chars, initialism matching)
- Implement fuzzy alias matching (Levenshtein distance)
- Resolve extracted names to existing entities
- Verification: Test resolves "IBM" to "International Business Machines"

**2.3.3 Create Queue Processor**
- Create `internal/entity/queue.go`
- Implement background worker that polls extraction queue
- Process items in batches
- Handle retry with exponential backoff
- Verification: Test processes queue items

**2.3.4 Add Extraction Hooks**
- Hook into `conversation.append` - queue message for extraction
- Hook into `knowledge.ingest` - queue chunks for extraction
- Verification: Append/ingest triggers extraction queue

**2.3.5 Implement Extraction Modes**
- Support `off`, `sampled`, `whitelist`, `full` modes
- Apply sample rate or keyword filtering
- Verification: Test each extraction mode

**2.3.6 Implement Entity Summary Regeneration**
- Track mention count changes
- Regenerate summary when threshold exceeded
- Re-embed summary after regeneration
- Verification: Test auto-regenerates summary

---

### Milestone 2.4: Context Version History
**Goal:** Implement context version tracking and optimistic concurrency

#### Tasks

**2.4.1 Implement Version Conflict Detection**
- On `context.set` and `context.merge`, check expected_version
- Return conflict error if version mismatch
- Verification: Test concurrent writes detect conflicts

**2.4.2 Implement History Retrieval**
- Implement `context.history` MCP handler
- Return version history from context_history table
- Verification: Test retrieves version history

---

### Milestone 2.5: CLI Management Commands
**Goal:** Implement CLI commands for data management

#### Tasks

**2.5.1 Knowledge CLI Commands**
- `cortex knowledge ingest --namespace X --collection Y --file Z`
- `cortex knowledge ingest-dir --namespace X --collection Y --dir Z --glob "**/*.md"`
- `cortex knowledge search --namespace X --query Y`
- `cortex knowledge stats --namespace X`
- Verification: CLI commands work

**2.5.2 Conversation CLI Commands**
- `cortex conversation list --namespace X`
- `cortex conversation export --namespace X --thread-id Y`
- Verification: CLI commands work

**2.5.3 Context CLI Commands**
- `cortex context list --namespace X`
- `cortex context get --namespace X --key Y`
- `cortex context set --namespace X --key Y --value Z`
- Verification: CLI commands work

**2.5.4 Entity CLI Commands**
- `cortex entity list --namespace X [--type Y]`
- `cortex entity query --namespace X --name Y`
- `cortex entity search --namespace X --query Y`
- `cortex entity merge --namespace X --source Y --target Z`
- `cortex entity export --namespace X --format json`
- Verification: CLI commands work

**2.5.5 Namespace CLI Commands**
- `cortex namespace list`
- `cortex namespace delete X`
- `cortex gc --expired-ttl --orphaned-chunks`
- Verification: CLI commands work

---

### Phase 2 Verification Checkpoint

**V2.1 Summarization Test**
- Append 60 messages to a thread
- Call `conversation.history`
- Verify summary is prepended
- Verify only recent messages returned in full

**V2.2 Entity Extraction Test**
- Ingest document mentioning companies and people
- Wait for extraction queue to process
- Query entities - verify extraction worked
- Check relationships between co-mentioned entities

**V2.3 Context Concurrency Test**
- Set context key with version 1
- Attempt set with expected_version=1 (should succeed)
- Attempt set with expected_version=1 again (should conflict)

---

## Phase 3: Production Hardening

### Milestone 3.1: PostgreSQL + pgvector Backend
**Goal:** Implement production-grade storage backend

#### Tasks

**3.1.1 Create pgvector Backend Scaffold**
- Create `internal/storage/pgvector/pgvector.go`
- Implement constructor with connection pooling
- Verification: Backend connects to PostgreSQL

**3.1.2 Implement pgvector Schema Migrations**
- Create tables matching SQLite schema
- Use PostgreSQL-native types (JSONB, vector, TIMESTAMPTZ)
- Create HNSW indexes on vector columns
- Create GIN indexes on JSONB metadata
- Verification: Test creates all tables with indexes

**3.1.3 Implement All Storage Operations**
- Port all operations from SQLite backend
- Use pgvector operators for similarity search
- Use GIN indexes for metadata pre-filtering
- Verification: All unit tests pass with pgvector

**3.1.4 Add Backend Selection**
- Detect backend from config (`storage.backend`)
- Instantiate correct backend implementation
- Verification: Config switches between backends

---

### Milestone 3.2: SSE Transport
**Goal:** Add Server-Sent Events MCP transport

#### Tasks

**3.2.1 Implement SSE Server**
- Add HTTP server with SSE endpoint
- Implement MCP-over-SSE protocol
- Verification: SSE client can connect

**3.2.2 Add Transport Selection**
- Support `--transport stdio` and `--transport sse --port 9810`
- Verification: Both transports work

---

### Milestone 3.3: Observability
**Goal:** Add Prometheus metrics and structured logging

#### Tasks

**3.3.1 Add Prometheus Metrics**
- Expose metrics endpoint on configured port
- Implement all metrics from FRD section 11.1
- Verification: Metrics endpoint returns valid Prometheus format

**3.3.2 Implement Structured Logging**
- Add request_id propagation from MCP headers
- Log with namespace, thread_id, run_id, entity_id context
- Verification: Logs contain structured fields

---

### Milestone 3.4: Retention & Garbage Collection
**Goal:** Implement data lifecycle management

#### Tasks

**3.4.1 Implement Background GC Worker**
- Run on configurable interval
- Delete expired TTL entries
- Delete old conversations
- Prune stale entities
- Remove orphaned chunks
- Verification: GC cleans up expired data

**3.4.2 Add CLI GC Command**
- `cortex gc --expired-ttl --orphaned-chunks`
- Verification: Manual GC works

---

### Milestone 3.5: Backup & Restore
**Goal:** Implement backup capabilities

#### Tasks

**3.5.1 Implement SQLite Backup**
- `cortex backup --output /path/to/backup.db`
- Use SQLite online backup API
- Verification: Backup creates valid database

**3.5.2 Implement Export/Import**
- `cortex export --namespace X --format json`
- `cortex import --file export.json`
- Verification: Round-trip preserves data

---

### Phase 3 Verification Checkpoint

**V3.1 pgvector Test**
- Run full test suite against PostgreSQL
- Verify HNSW index is used for similarity search
- Verify metadata filtering uses GIN index

**V3.2 SSE Transport Test**
- Start server with SSE transport
- Connect with MCP client over SSE
- Execute tool calls

**V3.3 Metrics Test**
- Perform various operations
- Verify metrics counters increment
- Verify histograms record latencies

**V3.4 GC Test**
- Create context entries with TTL
- Wait for expiration
- Run GC
- Verify entries deleted

---

## Phase 4: Ecosystem Integration

### Milestone 4.1: Hybrid Search
**Goal:** Combine vector + keyword search

#### Tasks

**4.1.1 Add Full-Text Search Indexes**
- SQLite: FTS5 virtual tables
- pgvector: tsvector columns with GIN indexes
- Verification: FTS queries return results

**4.1.2 Implement Reciprocal Rank Fusion**
- Combine vector and FTS results
- Apply RRF scoring
- Verification: Hybrid search improves recall

---

### Milestone 4.2: Bulk Ingest Optimization
**Goal:** Optimize high-volume ingest

#### Tasks

**4.2.1 Implement Batch Processing**
- Accept multiple documents in single ingest call
- Process embeddings in large batches
- Use bulk INSERT for chunks
- Verification: 1000 docs ingest in reasonable time

**4.2.2 Add Progress Reporting**
- CLI shows progress bar for ingest-dir
- Verification: Progress updates during bulk ingest

---

### Milestone 4.3: HTTP API Mode
**Goal:** Standalone HTTP API for non-MCP clients

#### Tasks

**4.3.1 Implement HTTP Server**
- REST endpoints mirroring MCP tools
- JSON request/response format
- Verification: curl commands work

---

## Summary: Task Count by Phase

| Phase | Milestones | Tasks | Verification |
|-------|------------|-------|--------------|
| 1 - Foundation | 9 | ~45 | 5 |
| 2 - Advanced | 5 | ~20 | 3 |
| 3 - Hardening | 5 | ~15 | 4 |
| 4 - Integration | 3 | ~8 | - |
| **Total** | **22** | **~88** | **12** |

## Implementation Order

1. Start with Milestone 1.1 (Project Setup)
2. Complete each milestone in order within Phase 1
3. Run Phase 1 verification before proceeding
4. Continue through Phase 2, 3, 4 with verification checkpoints

## Notes

- Each task is small (typically 1-3 hours of work)
- Verification tasks ensure correctness before moving forward
- Entity extraction (2.3) is the most complex milestone
- pgvector (3.1) can be deferred if SQLite meets initial needs
