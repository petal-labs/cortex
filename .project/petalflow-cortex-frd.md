# Cortex: PetalFlow Memory & Knowledge Service — Functional Requirements Document

## TL;DR

PetalFlow workflows today are stateless across runs. Every execution starts from zero context. The Agent/Task analysis flagged this explicitly — LangGraph's typed shared state with reducers is a killer feature PetalFlow doesn't match, and agent workflows in production invariably need to remember prior interactions, accumulate knowledge, and maintain conversational context across sessions.

**The solution is Cortex** — a standalone Go service that provides persistent context, vector-backed knowledge retrieval, and conversation memory for PetalFlow agents. It runs as its own process, exposes itself as an MCP server (plugging directly into PetalFlow's tool contract), and uses Iris for embedding generation across providers. PetalFlow's core remains dependency-free. The memory layer is external, optional, and composable.

**Key decisions:**

- **Four memory primitives:** conversation memory (agent dialogue history), knowledge store (vector-indexed documents and facts), workflow context (shared state that accumulates across tasks and persists across runs), and entity memory (automatically extracted and linked entities — people, companies, products — that build a structured knowledge graph from unstructured interactions).
- **MCP-native.** Cortex is an MCP server. PetalFlow discovers it via `tools/list`, registers it through the standard tool contract, and invokes it like any other tool. No special-casing in the engine.
- **Iris for embeddings.** Cortex calls Iris's embedding API for vector generation, inheriting multi-provider support (OpenAI, Anthropic, Cohere, Voyage, etc.) without binding to a single provider.
- **Pluggable vector backends.** SQLite + vec0 for single-node deployments (ships with the binary). PostgreSQL + pgvector for production scale. The storage interface is abstract — adding Qdrant, Pinecone, or Weaviate is a driver, not a rewrite.
- **Namespace isolation.** Every memory operation is scoped to a flat namespace string (e.g., `acme/research/q3`). Namespaces are not hierarchical. Cross-namespace search is not supported. Namespace enforcement happens at the service layer, and PetalFlow can restrict the allowed namespace during MCP initialization.
- **No core contamination.** Cortex is a separate binary (`cortex`), a separate Go module, and a separate repository. PetalFlow's core never imports it.
- **Trusted deployment model.** Cortex v1.0 assumes trusted internal deployment. Authentication, authorization, and role-based namespace isolation are v1.1 features. This must be understood before deploying multi-tenant.

---

## 1. Why Agents Need Memory

Stateless agents are demos. Stateful agents are products. The gap between the two is precisely what memory solves.

### 1.1 The Problem Today

PetalFlow's port-based data flow handles in-run state well. Data passes along edges between nodes within a single workflow execution. But three scenarios are completely unsupported:

**Cross-run persistence.** An agent that researches a topic today has no memory of that research tomorrow. If the same workflow runs again, it starts from scratch. This wastes LLM tokens, produces inconsistent results, and makes iterative workflows (research → refine → research again) impossible without external plumbing.

**Accumulated knowledge.** Agents that need to reference a corpus — product documentation, company policies, prior reports, customer history — have no retrieval mechanism. Every piece of context must be injected via the workflow input, which doesn't scale past a few thousand tokens.

**Conversational continuity.** Interactive agent workflows (chatbots, assistants, copilots) need to maintain dialogue history. Without memory, every user interaction is a first interaction.

### 1.2 What Competitors Provide

LangGraph offers typed shared state with reducer functions — powerful but tightly coupled to the graph engine. CrewAI has a memory system with short-term, long-term, and entity memory — convenient but limited to sequential recall. AutoGen provides a ChatMemory abstraction for conversation history. Mem0 exists as a standalone memory layer but is Python-only and opinionated about its retrieval strategy.

Cortex takes a different approach: it's a service, not a library. It doesn't live inside the workflow engine. It's a tool that agents call, just like they call `web_search` or `s3_fetch`. This means it works with any PetalFlow workflow shape (Agent/Task or raw graph), can be shared across workflows, and can be replaced or scaled independently.

---

## 2. Architecture

### 2.1 System Context

Cortex implements the Model Context Protocol (MCP), the same pluggable tool protocol already used by PetalFlow for `web_search`, `s3_fetch`, and other external capabilities. From PetalFlow's perspective, Cortex is just another MCP server — no special-casing in the engine.

```
PetalFlow Engine ──(MCP tool calls)──► Cortex
                                        │
                 ├─► Conversation Memory
                 ├─► Knowledge Store (RAG)
                 ├─► Workflow Context (shared state)
                 └─► Entity Memory (emergent KG)
                                        │
                                 Storage (SQLite vec0 / pgvector)
                                        │
                                 Iris (embeddings + summarization)
```

**Detailed architecture:**

```
┌────────────────────────────────────────────────────────────────────────┐
│                        PetalFlow Engine                                │
│                                                                        │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────────────────┐  │
│  │ Agent Node A │    │ Agent Node B │    │ Execution Engine         │  │
│  │ (researcher) │    │ (writer)     │    │                          │  │
│  └──────┬───────┘    └──────┬───────┘    └──────────────────────────┘  │
│         │                   │                                          │
│         └─────────┬─────────┘                                          │
│                   │ tool calls                                         │
│                   ▼                                                     │
│  ┌────────────────────────────────┐                                    │
│  │   MCP Adapter (Tool Contract) │                                    │
│  └────────────────┬───────────────┘                                    │
│                   │                                                     │
└───────────────────┼─────────────────────────────────────────────────────┘
                    │ MCP protocol (stdio or SSE)
                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                         Cortex                                    │
│                                                                        │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌────────────┐ │
│  │ Conversation │  │  Knowledge   │  │  Workflow     │  │  Entity    │ │
│  │ Memory       │  │  Store       │  │  Context      │  │  Memory    │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬────────┘  └─────┬──────┘ │
│         │                 │                  │                 │        │
│         └─────────┬───────┴──────────────────┴─────────────────┘        │
│                   ▼                                                     │
│  ┌────────────────────────────────┐  ┌───────────────────────────────┐ │
│  │   Storage Backend             │  │   Iris (Embedding Provider)   │ │
│  │   (SQLite+vec0 / pgvector)    │  │   (OpenAI, Anthropic, etc.)  │ │
│  └────────────────────────────────┘  └───────────────────────────────┘ │
│                                                                        │
└────────────────────────────────────────────────────────────────────────┘
```

### 2.2 Design Principles

1. **External service, not embedded library.** Cortex runs as a separate process. PetalFlow's core binary never imports it. Communication is MCP over stdio or SSE. This preserves PetalFlow's "no core dependencies" principle.

2. **Four primitives, one interface.** Conversation memory, knowledge retrieval, workflow context, and entity memory are conceptually different but share a unified MCP tool surface. Agents don't need to understand storage internals — they call `cortex.conversation.append`, `cortex.knowledge.search`, `cortex.entity.query`, etc.

3. **Namespace-scoped.** All data is partitioned by a flat namespace string. Namespaces are not hierarchical — `acme/research` and `acme/support` are independent, unrelated strings. Cross-namespace search is not supported. Namespace enforcement happens at the service layer. When PetalFlow spawns the Cortex MCP server, it can pass an `allowed_namespace` during the MCP initialization handshake; Cortex rejects any tool call attempting to access a namespace outside of scope. This prevents cross-contamination between workflows and enables safe multi-tenant deployments.

    **Namespace convention:** `<tenant>/<project>/<scope>` — e.g., `acme/research/q3-analysis`, `acme/support/user-12345`. Single-tenant deployments can use simple strings like `research` or `default`. Collections and threads are flat within a namespace — nesting is not supported in v1.0.

4. **Embedding-provider agnostic.** Cortex generates embeddings by calling Iris's embedding API. Iris handles provider routing (OpenAI `text-embedding-3-small`, Anthropic Voyage, Cohere `embed-v4`, etc.). Switching embedding providers is a config change, not a code change.

5. **Storage-backend agnostic.** The storage layer is an interface. SQLite with the `vec0` extension handles single-node deployments with zero infrastructure. pgvector handles production scale. The interface is designed so additional backends (Qdrant, Pinecone, Milvus) can be added as drivers.

### 2.3 Component Overview

**MCP Server Layer.** Handles MCP protocol lifecycle (initialize, tools/list, tools/call). Exposes all memory actions as MCP tools. Supports both stdio and SSE transport modes.

**Conversation Memory Engine.** Manages ordered message sequences (dialogue history) per namespace. Supports append, retrieve (last N), summarize (compress history via LLM), and clear. Messages are stored with role, content, timestamp, and optional metadata.

**Knowledge Store Engine.** Manages vector-indexed documents and facts. Supports ingest (documents, text chunks, structured facts), search (vector similarity + optional metadata filters), update, and delete. Documents are chunked, embedded, and indexed on ingest.

**Workflow Context Engine.** Manages key-value state that accumulates across tasks within a run and persists across runs. Supports get, set, merge (with configurable merge strategy), list, and delete. This is the analog to LangGraph's shared state — but exposed as a tool rather than built into the graph engine.

**Entity Memory Engine.** Automatically extracts and maintains a structured graph of entities (people, organizations, products, concepts) and their relationships from conversation and knowledge content. Uses LLM-powered NER on ingest hooks to build the graph incrementally. Supports entity queries ("What do we know about Acme Corp?"), relationship traversal, and entity-scoped recall.

**Storage Backend.** Abstract interface implemented by SQLite+vec0 (default) and pgvector (production). Handles vector storage, metadata indexing, full-text search, and transactional writes.

**Iris Integration.** Calls Iris's Go SDK for embedding generation. Supports batch embedding for bulk ingest. Caches embeddings for repeated queries.

---

## 3. Memory Primitives

### 3.1 Conversation Memory

Conversation memory stores ordered message history for agent dialogues. It answers the question: "What has been said in this conversation so far?"

**Data model:**

```go
type Message struct {
    ID        string            `json:"id"`
    Namespace string            `json:"namespace"`
    ThreadID  string            `json:"thread_id"`
    Role      string            `json:"role"`      // "user", "assistant", "system", "tool"
    Content   string            `json:"content"`
    Metadata  map[string]string `json:"metadata,omitempty"`
    SourceUser string           `json:"source_user,omitempty"` // For future GDPR/PII compliance
    TenantID  string            `json:"tenant_id,omitempty"`   // For future multi-tenancy
    CreatedAt time.Time         `json:"created_at"`
}

type Thread struct {
    ID        string    `json:"id"`
    Namespace string    `json:"namespace"`
    Title     string    `json:"title,omitempty"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
    Summary   string    `json:"summary,omitempty"` // LLM-generated summary of the thread
    Metadata  map[string]string `json:"metadata,omitempty"`
}
```

**Operations:**

| Action | Description | Use case |
|---|---|---|
| `conversation.append` | Add a message to a thread | Agent recording its own output or user input |
| `conversation.history` | Retrieve last N messages from a thread | Agent loading context before generating a response |
| `conversation.summarize` | Compress older messages into a summary via LLM | Managing context window limits on long conversations |
| `conversation.search` | Semantic search across conversation history | "What did the user say about pricing last week?" |
| `conversation.threads` | List threads in a namespace | UI displaying conversation history |
| `conversation.clear` | Delete all messages in a thread | Starting a fresh conversation |

**Summarization strategy.** When a thread exceeds a configurable message count (default: 50), the `summarize` action compresses older messages into a summary stored in the Thread record. The original messages are retained but marked as summarized. When `conversation.history` is called, it returns the summary plus recent unsummarized messages, keeping the token count manageable.

Summarization calls Iris to generate the summary using a configurable model and system prompt. The default prompt instructs the LLM to preserve key decisions, action items, facts, and user preferences while discarding conversational filler.

**Semantic search indexing.** Conversation messages are optionally embedded on append for later semantic search. This is controlled by a configuration flag:

```yaml
conversation:
  semantic_search_enabled: true   # Embed messages on append (default: true)
  summarize_on_embed: false       # Re-embed thread summary when generated (default: false)
```

When `semantic_search_enabled` is false, `conversation.search` is unavailable and messages are stored without embedding generation, reducing cost. Summarized messages are not re-embedded by default — the summary is embedded as a single new vector when generated, and the original message embeddings remain for historical search.

**Tool output handling.** `conversation.append` accepts `role: "tool"`, but tool outputs are often large JSON blobs that consume significant tokens when replayed as context. Cortex supports an optional `max_content_length` parameter on append. Content exceeding this limit is truncated with a `[truncated]` marker. A future enhancement may support automatic summarization of tool outputs via LLM before storage.

**Pagination.** `conversation.history` and `conversation.threads` support cursor-based pagination via an optional `cursor` (string, opaque) parameter. The response includes a `next_cursor` field when more results are available. This supports workflows that need to audit or export large conversation histories.

### 3.2 Knowledge Store

The knowledge store manages vector-indexed documents, facts, and reference material. It answers the question: "What relevant information exists in the knowledge base?"

**Data model:**

```go
type Document struct {
    ID          string            `json:"id"`
    Namespace   string            `json:"namespace"`
    CollectionID string           `json:"collection_id"`
    Title       string            `json:"title,omitempty"`
    Content     string            `json:"content"`
    ContentType string            `json:"content_type"` // "text", "markdown", "html"
    Source      string            `json:"source,omitempty"`
    Metadata    map[string]string `json:"metadata,omitempty"`
    SourceUser  string            `json:"source_user,omitempty"` // For future GDPR/PII compliance
    TenantID    string            `json:"tenant_id,omitempty"`   // For future multi-tenancy
    CreatedAt   time.Time         `json:"created_at"`
    UpdatedAt   time.Time         `json:"updated_at"`
}

type Chunk struct {
    ID         string    `json:"id"`
    DocumentID string    `json:"document_id"`
    Namespace  string    `json:"namespace"`
    Content    string    `json:"content"`
    Embedding  []float32 `json:"embedding"`
    Index      int       `json:"index"`     // Position within the document
    Metadata   map[string]string `json:"metadata,omitempty"`
}

type Collection struct {
    ID          string    `json:"id"`
    Namespace   string    `json:"namespace"`
    Name        string    `json:"name"`
    Description string    `json:"description,omitempty"`
    ChunkConfig ChunkConfig `json:"chunk_config"`
    CreatedAt   time.Time `json:"created_at"`
}

type ChunkConfig struct {
    Strategy    string `json:"strategy"`     // "fixed", "sentence", "paragraph", "semantic"
    MaxTokens   int    `json:"max_tokens"`   // Max tokens per chunk (default: 512)
    Overlap     int    `json:"overlap"`      // Overlap tokens between chunks (default: 50)
    Separator   string `json:"separator,omitempty"` // Custom split delimiter
}
```

**Operations:**

| Action | Description | Use case |
|---|---|---|
| `knowledge.ingest` | Add a document (auto-chunked and embedded) | Indexing product docs, reports, policies |
| `knowledge.search` | Vector similarity search with optional filters | Agent retrieving relevant context before responding |
| `knowledge.get` | Retrieve a specific document by ID | Fetching a known reference document |
| `knowledge.delete` | Remove a document and its chunks | Retiring outdated content |
| `knowledge.collections` | List or manage collections | Organizing knowledge by domain |
| `knowledge.update` | Re-ingest a document (re-chunk, re-embed) | Updating a living document |
| `knowledge.stats` | Return collection statistics | Monitoring knowledge base health |

**Chunking strategies:**

- **fixed**: Split by token count with overlap. Simple, predictable. Default.
- **sentence**: Split on sentence boundaries, grouping sentences up to the token limit. Better semantic coherence.
- **paragraph**: Split on paragraph boundaries (`\n\n`). Best for structured documents.
- **semantic**: Use embedding similarity to find natural breakpoints. Highest quality, highest cost (requires an embedding call per candidate split point). Recommended for high-value corpora.

**Search behavior.** `knowledge.search` performs a retrieve → filter → assemble pipeline:

1. **Retrieve.** Query is embedded via Iris, then a k-nearest-neighbor search is run against the chunk embeddings. Returns top-k candidate chunks (default k=10).
2. **Filter.** If filters are provided (e.g., `collection_id`, `source`, custom metadata keys), candidates are filtered post-retrieval. (Pre-filter is available on backends that support it — pgvector with GIN indexes.)
3. **Assemble.** Matching chunks are returned with their parent document metadata, relevance scores, and surrounding context (configurable window of adjacent chunks).

**Pagination.** `knowledge.collections` and `entity.list` support cursor-based pagination via an optional `cursor` parameter. Search results are bounded by `top_k` and do not paginate — increase `top_k` for broader retrieval.

**Hybrid search (v1.1).** Combine vector similarity with BM25 full-text search using reciprocal rank fusion. SQLite's FTS5 and PostgreSQL's tsvector both support this natively.

### 3.3 Workflow Context

Workflow context is persistent key-value state that accumulates across tasks within a workflow run and optionally persists across runs. It answers the question: "What shared state has been established by this workflow?"

This is the primitive that directly addresses the LangGraph gap from the Agent/Task analysis. Instead of building typed state with reducers into PetalFlow's core (which would be a massive scope expansion), Cortex provides an external state service that agents call explicitly.

**Data model:**

```go
type ContextEntry struct {
    Namespace string    `json:"namespace"`
    RunID     string    `json:"run_id,omitempty"` // Nil for cross-run state
    Key       string    `json:"key"`
    Value     any       `json:"value"`
    Version   int64     `json:"version"`          // Optimistic concurrency
    UpdatedAt time.Time `json:"updated_at"`
    UpdatedBy string    `json:"updated_by,omitempty"` // Agent/task that last wrote
    TTL       *int64    `json:"ttl_seconds,omitempty"` // Auto-expire
}
```

**Operations:**

| Action | Description | Use case |
|---|---|---|
| `context.get` | Retrieve a value by key | Agent checking if prior research exists |
| `context.set` | Store or overwrite a value | Agent recording a decision or intermediate result |
| `context.merge` | Deep-merge a value with an existing key | Multiple agents contributing to a shared findings object |
| `context.list` | List keys in a namespace (with optional prefix) | Enumerating accumulated state |
| `context.delete` | Remove a key | Cleaning up temporary state |
| `context.history` | Get version history of a key | Debugging what changed and when |

**Merge strategies for `context.merge`:**

- **deep_merge** (default): Recursively merge objects. Scalars are overwritten by the new value. **Array behavior** is controlled by an optional `array_strategy` parameter:
  - `concat` (default): Concatenate arrays. Nested arrays are flattened one level. Order is not guaranteed to be preserved.
  - `concat_unique`: Concatenate and deduplicate (by value equality for primitives, by a configurable key field for objects).
  - `replace`: Replace the existing array entirely with the new value.
  This prevents the most common deep_merge footgun — unbounded array growth from duplicate appends.
- **append**: Treat the existing value as an array and append the new value. Useful for log-style accumulation.
- **replace**: Overwrite entirely (same as `context.set`). Explicit strategy for clarity.
- **max/min/sum**: Numeric reducers. Useful for counters, scores, and aggregations. These only apply when both the existing and new values are numbers. If either value is non-numeric, the operation falls back to `replace` and logs a warning.

**Pagination.** `context.list` and `context.history` support cursor-based pagination via an optional `cursor` parameter.

**Scoping:**

- **Run-scoped context** (`run_id` is set): State exists only for the duration of a single workflow run. Equivalent to LangGraph's per-invocation state. Automatically cleaned up after the run completes (configurable retention).
- **Persistent context** (`run_id` is nil): State persists across runs. An agent can write a research summary on Tuesday, and another run on Wednesday can read it. This is the "memory across sessions" capability.
- **TTL-scoped**: State auto-expires after a duration. Useful for caching intermediate results that lose relevance after a window.

**Optimistic concurrency.** Every context entry has a version number. `context.set` and `context.merge` accept an optional `expected_version`. If the current version doesn't match, the operation fails with a conflict error. This prevents lost updates when multiple agents write concurrently to the same key.

### 3.4 Entity Memory

Entity memory automatically extracts, stores, and links entities — people, organizations, products, locations, concepts — from unstructured text flowing through Cortex. It answers the question: "What do we know about this specific person, company, or thing across all our interactions?"

This is the feature that transforms Cortex from a storage layer into a knowledge-building system. Rather than relying on agents to manually tag and organize information, entity memory builds a structured knowledge graph incrementally as conversations happen and documents are ingested.

**Data model:**

```go
type Entity struct {
    ID          string            `json:"id"`
    Namespace   string            `json:"namespace"`
    Name        string            `json:"name"`         // Canonical name
    Type        string            `json:"type"`         // "person", "organization", "product", "location", "concept"
    Aliases     []string          `json:"aliases"`      // Alternative names, abbreviations
    Summary     string            `json:"summary"`      // LLM-generated summary of everything known
    Attributes  map[string]string `json:"attributes"`   // Structured facts (e.g., "role": "CEO", "founded": "2021")
    Metadata    map[string]string `json:"metadata,omitempty"`
    FirstSeenAt time.Time         `json:"first_seen_at"`
    LastSeenAt  time.Time         `json:"last_seen_at"`
    MentionCount int64            `json:"mention_count"`
}

type EntityMention struct {
    ID        string    `json:"id"`
    EntityID  string    `json:"entity_id"`
    Namespace string    `json:"namespace"`
    SourceType string   `json:"source_type"` // "conversation", "knowledge", "manual"
    SourceID  string    `json:"source_id"`   // Message ID, chunk ID, or manual entry ID
    Context   string    `json:"context"`     // Surrounding text where the entity was mentioned
    Snippet   string    `json:"snippet"`     // The exact mention text
    CreatedAt time.Time `json:"created_at"`
}

type EntityRelationship struct {
    ID            string    `json:"id"`
    Namespace     string    `json:"namespace"`
    SourceEntityID string   `json:"source_entity_id"`
    TargetEntityID string   `json:"target_entity_id"`
    RelationType  string    `json:"relation_type"`  // "works_at", "competes_with", "part_of", "related_to"
    Description   string    `json:"description"`    // Free-text description of the relationship
    Confidence    float64   `json:"confidence"`     // 0.0-1.0 extraction confidence
    FirstSeenAt   time.Time `json:"first_seen_at"`
    LastSeenAt    time.Time `json:"last_seen_at"`
    MentionCount  int64     `json:"mention_count"`
}
```

**Operations:**

| Action | Description | Use case |
|---|---|---|
| `entity.query` | Look up an entity by name or alias, returning its summary, attributes, and relationships | Agent retrieving everything known about a customer or competitor |
| `entity.search` | Semantic search across entities by description or attributes | "Find all people we've discussed who work in AI safety" |
| `entity.relationships` | Get all relationships for an entity, optionally filtered by type | "Who works at Acme Corp?" / "What companies compete with Acme?" |
| `entity.update` | Manually add or correct attributes on an entity | Agent or operator fixing extraction errors |
| `entity.merge` | Merge two entities that refer to the same real-world thing | Resolving "Google" and "Alphabet" or "Bob Smith" and "Robert Smith" |
| `entity.list` | List entities in a namespace with optional type filter | Browsing all known organizations |
| `entity.delete` | Remove an entity and its mentions/relationships | Cleaning up false positives |

**Extraction pipeline.** Entity extraction runs as an asynchronous post-processing hook triggered by two events:

1. **`conversation.append` hook.** When a message is added to a conversation, Cortex queues it for entity extraction. The extraction runs asynchronously — `conversation.append` returns immediately, and entities are extracted in the background.

2. **`knowledge.ingest` hook.** When a document is ingested and chunked, each chunk is queued for entity extraction alongside its embedding generation.

**Extraction modes.** Extracting entities on every append and every chunk is operationally expensive and hard to rate-limit. Cortex supports configurable extraction modes to control cost:

```yaml
entity:
  extraction_mode: full       # "off" | "sampled" | "whitelist" | "full"
  sample_rate: 0.3            # For "sampled" mode: probability of extraction per item (0.0-1.0)
  whitelist_keywords:         # For "whitelist" mode: only extract if text contains these
    - "partnership"
    - "acquisition"
    - "funding"
    - "competitor"
```

- **off**: No automatic extraction. Entities are only created manually via `entity.update`.
- **sampled**: Extract from a random sample of items. Useful for high-volume deployments where full extraction is cost-prohibitive. Entities still accumulate over time, just slower.
- **whitelist**: Only extract from content containing one or more trigger keywords. Good for domain-specific deployments where only certain topics matter.
- **full**: Extract from every conversation message and knowledge chunk. Current spec behavior. Best for low-to-medium volume deployments or high-value namespaces.

The extraction itself calls Iris's completion API with a structured extraction prompt:

```go
const entityExtractionPrompt = `Extract all named entities from the following text. For each entity, provide:
- name: The canonical name
- type: One of "person", "organization", "product", "location", "concept"
- aliases: Any alternative names or abbreviations used in the text
- attributes: Key-value pairs of factual information mentioned (e.g., role, title, founding year)
- relationships: Connections to other entities mentioned in the same text

Respond ONLY with a JSON array. If no entities are found, respond with an empty array.

Text:
%s`
```

The extraction response is parsed and reconciled against existing entities:

1. **Name resolution.** For each extracted entity, Cortex searches for existing entities with the same name or overlapping aliases (case-insensitive, with fuzzy matching). If a match is found, the entity is updated rather than duplicated.
2. **Attribute merging.** New attributes are merged into existing entities. Conflicts (e.g., a person's title changed) are resolved by keeping the most recent value and logging the change.
3. **Relationship creation.** Relationships between co-mentioned entities are created or have their mention counts incremented.
4. **Summary regeneration.** When an entity accumulates significant new information (configurable threshold, default: every 5 new mentions), its summary is regenerated via an LLM call that considers all mentions and attributes.

**Confidence, deduplication, and blocking.** Entity extraction is inherently noisy. Cortex mitigates this with:

- **Confidence scores** on relationships and attributes. Low-confidence extractions are stored but flagged.
- **Blocking strategy for resolution.** Before running expensive fuzzy matching against all entities, Cortex applies a blocking filter: only entities sharing the first 3 characters (case-insensitive) or matching an initialism (e.g., "IBM" matches "International Business Machines") are considered as candidates. This reduces the O(n) comparison space for name resolution.
- **Alias-based deduplication.** "IBM", "International Business Machines", and "Big Blue" resolve to the same entity.
- **Entity merge tool.** When automatic deduplication fails, agents or operators can explicitly merge entities via `entity.merge`.
- **Minimum mention threshold.** Entities mentioned only once with low confidence can be auto-pruned (configurable).

**Failure handling.** The extraction queue processes items asynchronously with retry and dead-letter semantics:

```yaml
entity:
  extraction_max_attempts: 5
  extraction_backoff: exponential    # "fixed" | "exponential"
  extraction_dead_letter_policy: retain  # "retain" | "drop"
```

Items that fail all retry attempts are either retained in the queue with status `dead_letter` (for manual inspection and replay) or silently dropped. The queue exposes a `dead_letter_count` metric for alerting.

**Embedding.** Entity summaries are embedded and stored in the vector index, enabling semantic search across entities. This means `entity.search` with a query like "companies working on quantum computing" finds relevant entities even if the exact phrase was never used.

---

## 4. MCP Server Interface

### 4.1 Tool Definitions

Cortex exposes itself as an MCP server. When PetalFlow calls `tools/list`, it returns the following tools:

```json
{
  "tools": [
    {
      "name": "conversation_append",
      "description": "Append a message to a conversation thread. Creates the thread if it doesn't exist.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string", "description": "Isolation scope (e.g., workflow ID, user ID)" },
          "thread_id": { "type": "string", "description": "Conversation thread identifier" },
          "role": { "type": "string", "enum": ["user", "assistant", "system", "tool"] },
          "content": { "type": "string", "description": "Message content" },
          "metadata": { "type": "object", "description": "Optional key-value metadata" },
          "max_content_length": { "type": "integer", "description": "Truncate content exceeding this character count (optional, useful for large tool outputs)" }
        },
        "required": ["namespace", "thread_id", "role", "content"]
      }
    },
    {
      "name": "conversation_history",
      "description": "Retrieve recent messages from a conversation thread, including any summarized context.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "thread_id": { "type": "string" },
          "last_n": { "type": "integer", "description": "Number of recent messages to return (default: 20)" },
          "include_summary": { "type": "boolean", "description": "Prepend thread summary if available (default: true)" },
          "cursor": { "type": "string", "description": "Pagination cursor from previous response (optional)" }
        },
        "required": ["namespace", "thread_id"]
      }
    },
    {
      "name": "conversation_summarize",
      "description": "Generate an LLM summary of older messages in a thread, compressing them for context management.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "thread_id": { "type": "string" },
          "keep_recent": { "type": "integer", "description": "Number of recent messages to keep unsummarized (default: 10)" }
        },
        "required": ["namespace", "thread_id"]
      }
    },
    {
      "name": "conversation_search",
      "description": "Semantic search across conversation history in a namespace.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "query": { "type": "string", "description": "Natural language search query" },
          "thread_id": { "type": "string", "description": "Limit to a specific thread (optional)" },
          "top_k": { "type": "integer", "description": "Max results (default: 5)" }
        },
        "required": ["namespace", "query"]
      }
    },
    {
      "name": "knowledge_ingest",
      "description": "Ingest a document into the knowledge store. The document is chunked, embedded, and indexed for retrieval.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "collection_id": { "type": "string", "description": "Collection to add the document to" },
          "title": { "type": "string" },
          "content": { "type": "string", "description": "Document text content" },
          "content_type": { "type": "string", "enum": ["text", "markdown", "html"], "description": "Content format (default: text)" },
          "source": { "type": "string", "description": "Origin URL or file path" },
          "metadata": { "type": "object", "description": "Filterable key-value metadata" },
          "chunk_config": {
            "type": "object",
            "description": "Override collection's default chunking (optional)",
            "properties": {
              "strategy": { "type": "string", "enum": ["fixed", "sentence", "paragraph", "semantic"] },
              "max_tokens": { "type": "integer" },
              "overlap": { "type": "integer" }
            }
          }
        },
        "required": ["namespace", "collection_id", "content"]
      }
    },
    {
      "name": "knowledge_search",
      "description": "Search the knowledge store using semantic similarity. Returns relevant document chunks with context.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "query": { "type": "string", "description": "Natural language search query" },
          "collection_id": { "type": "string", "description": "Limit to a specific collection (optional)" },
          "top_k": { "type": "integer", "description": "Max results (default: 5)" },
          "min_score": { "type": "number", "description": "Minimum similarity threshold 0.0-1.0 (default: 0.0)" },
          "filters": { "type": "object", "description": "Metadata key-value filters (optional)" },
          "include_context": { "type": "boolean", "description": "Include adjacent chunks for context (default: true)" },
          "context_window": { "type": "integer", "description": "Number of adjacent chunks to include (default: 1)" }
        },
        "required": ["namespace", "query"]
      }
    },
    {
      "name": "knowledge_collections",
      "description": "List or create collections in a namespace.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "action": { "type": "string", "enum": ["list", "create", "delete"], "description": "Operation to perform" },
          "name": { "type": "string", "description": "Collection name (required for create)" },
          "description": { "type": "string", "description": "Collection description (for create)" },
          "collection_id": { "type": "string", "description": "Collection to delete (for delete)" },
          "chunk_config": { "type": "object", "description": "Default chunk config for new documents (for create)" }
        },
        "required": ["namespace", "action"]
      }
    },
    {
      "name": "context_get",
      "description": "Retrieve a value from workflow context by key.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "key": { "type": "string" },
          "run_id": { "type": "string", "description": "Scope to a specific run (omit for persistent context)" }
        },
        "required": ["namespace", "key"]
      }
    },
    {
      "name": "context_set",
      "description": "Store a value in workflow context. Overwrites any existing value at this key.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "key": { "type": "string" },
          "value": { "description": "Any JSON-serializable value" },
          "run_id": { "type": "string", "description": "Scope to a specific run (omit for persistent context)" },
          "ttl_seconds": { "type": "integer", "description": "Auto-expire after this many seconds (optional)" },
          "expected_version": { "type": "integer", "description": "Optimistic concurrency check (optional)" }
        },
        "required": ["namespace", "key", "value"]
      }
    },
    {
      "name": "context_merge",
      "description": "Merge a value into an existing workflow context key using a specified strategy.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "key": { "type": "string" },
          "value": { "description": "Value to merge" },
          "strategy": { "type": "string", "enum": ["deep_merge", "append", "replace", "max", "min", "sum"], "description": "Merge strategy (default: deep_merge)" },
          "run_id": { "type": "string" },
          "expected_version": { "type": "integer" }
        },
        "required": ["namespace", "key", "value"]
      }
    },
    {
      "name": "context_list",
      "description": "List keys in a workflow context namespace.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "prefix": { "type": "string", "description": "Key prefix filter (optional)" },
          "run_id": { "type": "string" },
          "cursor": { "type": "string", "description": "Pagination cursor from previous response (optional)" },
          "limit": { "type": "integer", "description": "Max results per page (default: 50)" }
        },
        "required": ["namespace"]
      }
    },
    {
      "name": "entity_query",
      "description": "Look up an entity by name or alias. Returns the entity's summary, attributes, relationships, and recent mentions.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "name": { "type": "string", "description": "Entity name or alias to look up" },
          "include_mentions": { "type": "boolean", "description": "Include recent mentions with source context (default: true)" },
          "mention_limit": { "type": "integer", "description": "Max mentions to return (default: 10)" }
        },
        "required": ["namespace", "name"]
      }
    },
    {
      "name": "entity_search",
      "description": "Semantic search across entities by description, attributes, or summary content.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "query": { "type": "string", "description": "Natural language search query" },
          "type": { "type": "string", "enum": ["person", "organization", "product", "location", "concept"], "description": "Filter by entity type (optional)" },
          "top_k": { "type": "integer", "description": "Max results (default: 10)" }
        },
        "required": ["namespace", "query"]
      }
    },
    {
      "name": "entity_relationships",
      "description": "Get all relationships for an entity, optionally filtered by relationship type.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "entity_name": { "type": "string", "description": "Entity name or alias" },
          "relation_type": { "type": "string", "description": "Filter by relation type (optional)" },
          "direction": { "type": "string", "enum": ["outgoing", "incoming", "both"], "description": "Relationship direction (default: both)" }
        },
        "required": ["namespace", "entity_name"]
      }
    },
    {
      "name": "entity_update",
      "description": "Manually add or correct attributes, aliases, or type on an existing entity.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "entity_name": { "type": "string", "description": "Entity name or alias" },
          "attributes": { "type": "object", "description": "Key-value attributes to set or update" },
          "aliases": { "type": "array", "items": { "type": "string" }, "description": "Additional aliases to add" },
          "type": { "type": "string", "enum": ["person", "organization", "product", "location", "concept"] }
        },
        "required": ["namespace", "entity_name"]
      }
    },
    {
      "name": "entity_merge",
      "description": "Merge two entities that refer to the same real-world thing. Combines mentions, relationships, and attributes.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "source_entity": { "type": "string", "description": "Entity name to merge FROM (will be deleted)" },
          "target_entity": { "type": "string", "description": "Entity name to merge INTO (will be kept)" }
        },
        "required": ["namespace", "source_entity", "target_entity"]
      }
    },
    {
      "name": "entity_list",
      "description": "List entities in a namespace with optional type filter and sorting.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "namespace": { "type": "string" },
          "type": { "type": "string", "enum": ["person", "organization", "product", "location", "concept"] },
          "sort_by": { "type": "string", "enum": ["name", "mention_count", "last_seen"], "description": "Sort order (default: mention_count)" },
          "limit": { "type": "integer", "description": "Max results (default: 50)" },
          "cursor": { "type": "string", "description": "Pagination cursor from previous response (optional)" }
        },
        "required": ["namespace"]
      }
    }
  ]
}
```

### 4.2 PetalFlow Overlay

The corresponding PetalFlow overlay gives these tools typed outputs for graph wiring validation:

```yaml
# cortex.overlay.yaml
overlay_version: "1.0"

group_actions:
  conversation.append: conversation_append
  conversation.history: conversation_history
  conversation.summarize: conversation_summarize
  conversation.search: conversation_search
  knowledge.ingest: knowledge_ingest
  knowledge.search: knowledge_search
  knowledge.collections: knowledge_collections
  context.get: context_get
  context.set: context_set
  context.merge: context_merge
  context.list: context_list
  entity.query: entity_query
  entity.search: entity_search
  entity.relationships: entity_relationships
  entity.update: entity_update
  entity.merge: entity_merge
  entity.list: entity_list

output_schemas:
  conversation.append:
    message_id:
      type: string
    thread_id:
      type: string

  conversation.history:
    messages:
      type: array
      items:
        type: object
        properties:
          id: { type: string }
          role: { type: string }
          content: { type: string }
          created_at: { type: string }
          metadata: { type: object }
    summary:
      type: string
      description: "Thread summary (if available and include_summary=true)"
    total_count:
      type: integer
    next_cursor:
      type: string
      description: "Pagination cursor for next page (null if no more results)"

  conversation.search:
    results:
      type: array
      items:
        type: object
        properties:
          message_id: { type: string }
          thread_id: { type: string }
          content: { type: string }
          role: { type: string }
          score: { type: float }
          created_at: { type: string }

  knowledge.ingest:
    document_id:
      type: string
    chunks_created:
      type: integer
    collection_id:
      type: string

  knowledge.search:
    results:
      type: array
      items:
        type: object
        properties:
          chunk_id: { type: string }
          document_id: { type: string }
          content: { type: string }
          score: { type: float }
          document_title: { type: string }
          source: { type: string }
          metadata: { type: object }
          context_before: { type: string }
          context_after: { type: string }
    total_found:
      type: integer

  context.get:
    key:
      type: string
    value:
      type: any
    version:
      type: integer
    updated_at:
      type: string
    exists:
      type: boolean

  context.set:
    key:
      type: string
    version:
      type: integer
    previous_version:
      type: integer

  context.merge:
    key:
      type: string
    version:
      type: integer
    merged_value:
      type: any

  context.list:
    keys:
      type: array
      items:
        type: string
    count:
      type: integer
    next_cursor:
      type: string

  entity.query:
    entity:
      type: object
      properties:
        id: { type: string }
        name: { type: string }
        type: { type: string }
        aliases: { type: array, items: { type: string } }
        summary: { type: string }
        attributes: { type: object }
        mention_count: { type: integer }
        first_seen_at: { type: string }
        last_seen_at: { type: string }
    relationships:
      type: array
      items:
        type: object
        properties:
          target_name: { type: string }
          target_type: { type: string }
          relation_type: { type: string }
          description: { type: string }
    mentions:
      type: array
      items:
        type: object
        properties:
          source_type: { type: string }
          context: { type: string }
          created_at: { type: string }
    found:
      type: boolean

  entity.search:
    results:
      type: array
      items:
        type: object
        properties:
          name: { type: string }
          type: { type: string }
          summary: { type: string }
          score: { type: float }
          mention_count: { type: integer }
    total_found:
      type: integer

  entity.relationships:
    entity_name:
      type: string
    relationships:
      type: array
      items:
        type: object
        properties:
          related_entity: { type: string }
          related_entity_type: { type: string }
          relation_type: { type: string }
          description: { type: string }
          direction: { type: string }
          confidence: { type: float }
          mention_count: { type: integer }

  entity.update:
    entity_name:
      type: string
    updated_fields:
      type: array
      items:
        type: string

  entity.merge:
    kept_entity:
      type: string
    merged_mentions:
      type: integer
    merged_relationships:
      type: integer

  entity.list:
    entities:
      type: array
      items:
        type: object
        properties:
          name: { type: string }
          type: { type: string }
          mention_count: { type: integer }
          last_seen_at: { type: string }
    total_count:
      type: integer
    next_cursor:
      type: string

config:
  storage_backend:
    type: string
    required: false
    default: "sqlite"
    description: "Storage backend: sqlite or pgvector"
    env_var: CORTEX_STORAGE_BACKEND
  database_url:
    type: string
    required: false
    description: "Database URL (required for pgvector, auto-created for sqlite)"
    env_var: CORTEX_DATABASE_URL
    sensitive: true
  data_dir:
    type: string
    required: false
    default: "~/.cortex/data"
    description: "Data directory for SQLite databases"
    env_var: CORTEX_DATA_DIR
  iris_endpoint:
    type: string
    required: false
    default: "http://localhost:8787"
    description: "Iris API endpoint for embedding generation"
    env_var: CORTEX_IRIS_ENDPOINT
  embedding_provider:
    type: string
    required: false
    default: "openai"
    description: "Embedding provider (via Iris)"
    env_var: CORTEX_EMBEDDING_PROVIDER
  embedding_model:
    type: string
    required: false
    default: "text-embedding-3-small"
    description: "Embedding model name"
    env_var: CORTEX_EMBEDDING_MODEL
  embedding_dimensions:
    type: integer
    required: false
    default: 1536
    description: "Embedding vector dimensions"
    env_var: CORTEX_EMBEDDING_DIMENSIONS
  summarization_provider:
    type: string
    required: false
    default: "anthropic"
    description: "LLM provider for conversation summarization (via Iris)"
    env_var: CORTEX_SUMMARIZATION_PROVIDER
  summarization_model:
    type: string
    required: false
    default: "claude-sonnet-4-20250514"
    description: "LLM model for summarization"
    env_var: CORTEX_SUMMARIZATION_MODEL
  entity_extraction_enabled:
    type: boolean
    required: false
    default: true
    description: "Enable automatic entity extraction from conversations and documents"
    env_var: CORTEX_ENTITY_EXTRACTION_ENABLED
  entity_extraction_model:
    type: string
    required: false
    default: "claude-haiku-4-5-20251001"
    description: "LLM model for entity extraction (fast model recommended)"
    env_var: CORTEX_ENTITY_EXTRACTION_MODEL
  entity_summary_regeneration_threshold:
    type: integer
    required: false
    default: 5
    description: "New mentions before regenerating entity summary"
    env_var: CORTEX_ENTITY_SUMMARY_THRESHOLD
  entity_min_confidence:
    type: number
    required: false
    default: 0.5
    description: "Minimum extraction confidence to store an entity (0.0-1.0)"
    env_var: CORTEX_ENTITY_MIN_CONFIDENCE

health:
  strategy: ping
  interval_seconds: 30
  timeout_ms: 5000
  unhealthy_threshold: 3

metadata:
  author: "petal-labs"
  version: "0.1.0"
  homepage: "https://github.com/petal-labs/cortex"
  tags: ["memory", "knowledge", "vector", "context", "rag"]
```

### 4.3 Registration in PetalFlow

Cortex registers like any other MCP tool — no special-casing. The overlay ships alongside PetalFlow's tool configs in the PetalFlow repository, not in the Cortex repository. This keeps Cortex a pure service with no PetalFlow-specific files, and lets PetalFlow manage its own tool integration contracts.

```
petalflow/
├── tools/
│   ├── overlays/
│   │   ├── cortex.overlay.yaml   // Cortex overlay
│   │   ├── s3_fetch.overlay.yaml      // Other tool overlays
│   │   └── ...
│   └── manifests/
│       └── ...
```

```yaml
# petalflow.yaml
tools:
  cortex:
    type: mcp
    transport:
      mode: stdio
      command: cortex
      args: ["serve", "--mcp"]
    overlay: ./tools/overlays/cortex.overlay.yaml
    config:
      storage_backend: sqlite
      data_dir: ./data/cortex
      iris_endpoint: "http://localhost:8787"
      embedding_provider: openai
      embedding_model: text-embedding-3-small
```

Or via CLI:

```bash
petalflow tools register cortex \
    --type mcp \
    --transport-mode stdio \
    --command "cortex" \
    --args "serve,--mcp" \
    --overlay ./tools/overlays/cortex.overlay.yaml
```

### 4.4 Agent/Task Integration

In Agent/Task workflows, memory tools are referenced like any other tool:

```json
{
  "agents": {
    "researcher": {
      "role": "Senior Research Analyst",
      "goal": "Find and synthesize information, building on prior research",
      "tools": [
        "web_search",
        "cortex.knowledge.search",
        "cortex.knowledge.ingest",
        "cortex.context.get",
        "cortex.context.set",
        "cortex.entity.query",
        "cortex.entity.search"
      ]
    },
    "assistant": {
      "role": "Customer Support Agent",
      "goal": "Help users while maintaining conversation context",
      "tools": [
        "cortex.conversation.history",
        "cortex.conversation.append",
        "cortex.knowledge.search",
        "cortex.entity.query",
        "cortex.entity.relationships"
      ]
    }
  }
}
```

The Agent/Task compiler handles these identically to any other tool reference — they become LLM function-calling tools in the agent node's tool config (since all memory operations take simple typed inputs and return JSON).

---

## 5. Storage Backend

### 5.1 Storage Interface

```go
package storage

type Backend interface {
    // Conversation memory
    AppendMessage(ctx context.Context, msg *Message) error
    GetMessages(ctx context.Context, namespace, threadID string, limit int) ([]*Message, error)
    SearchMessages(ctx context.Context, namespace string, embedding []float32, opts SearchOpts) ([]*MessageResult, error)
    ListThreads(ctx context.Context, namespace string) ([]*Thread, error)
    UpdateThread(ctx context.Context, thread *Thread) error
    DeleteThread(ctx context.Context, namespace, threadID string) error

    // Knowledge store
    InsertDocument(ctx context.Context, doc *Document) error
    InsertChunks(ctx context.Context, chunks []*Chunk) error
    SearchChunks(ctx context.Context, namespace string, embedding []float32, opts SearchOpts) ([]*ChunkResult, error)
    GetDocument(ctx context.Context, namespace, docID string) (*Document, error)
    DeleteDocument(ctx context.Context, namespace, docID string) error
    GetAdjacentChunks(ctx context.Context, chunkID string, window int) ([]*Chunk, error)
    ListCollections(ctx context.Context, namespace string) ([]*Collection, error)
    CreateCollection(ctx context.Context, col *Collection) error
    DeleteCollection(ctx context.Context, namespace, colID string) error
    CollectionStats(ctx context.Context, namespace, colID string) (*CollectionStats, error)

    // Workflow context
    GetContext(ctx context.Context, namespace, key string, runID *string) (*ContextEntry, error)
    SetContext(ctx context.Context, entry *ContextEntry) error
    ListContextKeys(ctx context.Context, namespace string, prefix *string, runID *string) ([]string, error)
    DeleteContext(ctx context.Context, namespace, key string, runID *string) error
    ContextHistory(ctx context.Context, namespace, key string, runID *string) ([]*ContextEntry, error)
    CleanupExpired(ctx context.Context) (int64, error)

    // Entity memory
    UpsertEntity(ctx context.Context, entity *Entity) error
    GetEntityByName(ctx context.Context, namespace, name string) (*Entity, error)
    ResolveAlias(ctx context.Context, namespace, alias string) (*Entity, error)
    SearchEntities(ctx context.Context, namespace string, embedding []float32, opts EntitySearchOpts) ([]*EntityResult, error)
    ListEntities(ctx context.Context, namespace string, opts EntityListOpts) ([]*Entity, error)
    DeleteEntity(ctx context.Context, namespace, entityID string) error
    MergeEntities(ctx context.Context, namespace, sourceID, targetID string) error
    InsertMention(ctx context.Context, mention *EntityMention) error
    GetMentions(ctx context.Context, entityID string, limit int) ([]*EntityMention, error)
    UpsertRelationship(ctx context.Context, rel *EntityRelationship) error
    GetRelationships(ctx context.Context, namespace, entityID string, opts RelationshipOpts) ([]*EntityRelationship, error)
    RegisterAlias(ctx context.Context, namespace, alias, entityID string) error
    EnqueueExtraction(ctx context.Context, item *ExtractionQueueItem) error
    DequeueExtraction(ctx context.Context, batchSize int) ([]*ExtractionQueueItem, error)
    CompleteExtraction(ctx context.Context, itemID int64, status string) error

    // Lifecycle
    Migrate(ctx context.Context) error
    Close() error
    Health(ctx context.Context) error
}

type SearchOpts struct {
    TopK         int
    MinScore     float64
    CollectionID *string
    ThreadID     *string
    Filters      map[string]string
}

type ChunkResult struct {
    Chunk         *Chunk
    Score         float64
    DocumentTitle string
    Source        string
    DocMetadata   map[string]string
}

type MessageResult struct {
    Message  *Message
    Score    float64
    ThreadID string
}

type CollectionStats struct {
    DocumentCount int64
    ChunkCount    int64
    TotalTokens   int64
    LastIngest    time.Time
}

type EntitySearchOpts struct {
    TopK       int
    MinScore   float64
    EntityType *string
}

type EntityListOpts struct {
    EntityType *string
    SortBy     string // "name", "mention_count", "last_seen"
    Limit      int
}

type RelationshipOpts struct {
    RelationType *string
    Direction    string // "outgoing", "incoming", "both"
}

type EntityResult struct {
    Entity *Entity
    Score  float64
}

type ExtractionQueueItem struct {
    ID         int64
    Namespace  string
    SourceType string
    SourceID   string
    Content    string
    Status     string
    Attempts   int
}
```

### 5.2 SQLite + vec0 (Default)

The default backend uses SQLite with the `vec0` virtual table extension for vector similarity search. This enables zero-infrastructure deployments — Cortex ships a single binary with embedded SQLite.

**Schema:**

```sql
-- Conversation memory
CREATE TABLE threads (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    title TEXT,
    summary TEXT,
    metadata TEXT, -- JSON
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_threads_namespace ON threads(namespace);

CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata TEXT, -- JSON
    summarized BOOLEAN DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_messages_thread ON messages(namespace, thread_id, created_at);

-- Message embeddings (for semantic search across conversations)
CREATE VIRTUAL TABLE message_embeddings USING vec0(
    message_id TEXT PRIMARY KEY,
    embedding float[1536]
);

-- Knowledge store
CREATE TABLE collections (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    chunk_config TEXT, -- JSON
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(namespace, name)
);

CREATE TABLE documents (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    title TEXT,
    content TEXT NOT NULL,
    content_type TEXT DEFAULT 'text',
    source TEXT,
    metadata TEXT, -- JSON
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_documents_collection ON documents(namespace, collection_id);

CREATE TABLE chunks (
    id TEXT PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    namespace TEXT NOT NULL,
    collection_id TEXT NOT NULL,
    content TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    metadata TEXT, -- JSON
    token_count INTEGER
);
CREATE INDEX idx_chunks_document ON chunks(document_id, chunk_index);
CREATE INDEX idx_chunks_collection ON chunks(namespace, collection_id);

CREATE VIRTUAL TABLE chunk_embeddings USING vec0(
    chunk_id TEXT PRIMARY KEY,
    embedding float[1536]
);

-- Workflow context
CREATE TABLE context_entries (
    namespace TEXT NOT NULL,
    run_id TEXT, -- NULL for persistent context
    key TEXT NOT NULL,
    value TEXT NOT NULL, -- JSON
    version INTEGER NOT NULL DEFAULT 1,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_by TEXT,
    ttl_expires_at DATETIME, -- NULL for no expiration
    PRIMARY KEY (namespace, COALESCE(run_id, ''), key)
);
CREATE INDEX idx_context_ttl ON context_entries(ttl_expires_at) WHERE ttl_expires_at IS NOT NULL;

-- Context history (append-only log)
CREATE TABLE context_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace TEXT NOT NULL,
    run_id TEXT,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    version INTEGER NOT NULL,
    operation TEXT NOT NULL, -- "set", "merge", "delete"
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_by TEXT
);
CREATE INDEX idx_context_history_key ON context_history(namespace, key, version);

-- Entity memory
CREATE TABLE entities (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL, -- "person", "organization", "product", "location", "concept"
    aliases TEXT, -- JSON array
    summary TEXT,
    attributes TEXT, -- JSON object
    metadata TEXT, -- JSON object
    mention_count INTEGER DEFAULT 0,
    first_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_entities_namespace ON entities(namespace, type);
CREATE INDEX idx_entities_name ON entities(namespace, name);
CREATE UNIQUE INDEX idx_entities_unique_name ON entities(namespace, LOWER(name));

-- Entity name/alias lookup (denormalized for fast resolution)
CREATE TABLE entity_aliases (
    namespace TEXT NOT NULL,
    alias TEXT NOT NULL, -- Lowercased for case-insensitive lookup
    entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    PRIMARY KEY (namespace, alias)
);

-- Entity embeddings (for semantic search across entity summaries)
CREATE VIRTUAL TABLE entity_embeddings USING vec0(
    entity_id TEXT PRIMARY KEY,
    embedding float[1536]
);

CREATE TABLE entity_mentions (
    id TEXT PRIMARY KEY,
    entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    namespace TEXT NOT NULL,
    source_type TEXT NOT NULL, -- "conversation", "knowledge", "manual"
    source_id TEXT NOT NULL,
    context TEXT, -- Surrounding text
    snippet TEXT, -- Exact mention text
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_entity_mentions_entity ON entity_mentions(entity_id, created_at DESC);
CREATE INDEX idx_entity_mentions_source ON entity_mentions(source_type, source_id);

CREATE TABLE entity_relationships (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    source_entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    target_entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    relation_type TEXT NOT NULL,
    description TEXT,
    confidence REAL DEFAULT 1.0,
    mention_count INTEGER DEFAULT 1,
    first_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(namespace, source_entity_id, target_entity_id, relation_type)
);
CREATE INDEX idx_entity_rels_source ON entity_relationships(source_entity_id);
CREATE INDEX idx_entity_rels_target ON entity_relationships(target_entity_id);

-- Entity extraction queue (async processing)
CREATE TABLE entity_extraction_queue (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_id TEXT NOT NULL,
    content TEXT NOT NULL,
    status TEXT DEFAULT 'pending', -- "pending", "processing", "completed", "failed"
    attempts INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    processed_at DATETIME
);
CREATE INDEX idx_extraction_queue_status ON entity_extraction_queue(status, created_at);
```

**vec0 specifics.** The `vec0` extension (part of sqlite-vec) provides ANN (approximate nearest neighbor) search. It supports multiple distance metrics (cosine, L2, inner product). Cortex defaults to cosine similarity. The extension is compiled into the Cortex binary — no external dependencies.

**Limitations.** SQLite is single-writer. For workflows with high concurrency (many agents writing simultaneously), writes are serialized. This is acceptable for most single-node deployments. For production scale, use the pgvector backend.

### 5.3 PostgreSQL + pgvector (Production)

For production deployments with multiple concurrent workflows, multiple Cortex instances, or large corpora, pgvector provides:

- Concurrent read/write with proper transaction isolation
- HNSW and IVFFlat indexes for fast ANN at scale
- GIN indexes for metadata filtering (pre-filter, not post-filter)
- Connection pooling and horizontal read replicas
- Native `tsvector` for hybrid search (v1.1)

The schema mirrors the SQLite version with PostgreSQL-native types:

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE chunks (
    id TEXT PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    namespace TEXT NOT NULL,
    collection_id TEXT NOT NULL,
    content TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    embedding vector(1536),
    metadata JSONB,
    token_count INTEGER
);

CREATE INDEX idx_chunks_embedding ON chunks USING hnsw (embedding vector_cosine_ops);
CREATE INDEX idx_chunks_metadata ON chunks USING gin (metadata);

-- Entity tables use the same pattern
CREATE TABLE entities (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    aliases JSONB,
    summary TEXT,
    summary_embedding vector(1536),
    attributes JSONB,
    metadata JSONB,
    mention_count INTEGER DEFAULT 0,
    first_seen_at TIMESTAMPTZ DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_entities_summary_embedding ON entities USING hnsw (summary_embedding vector_cosine_ops);
CREATE INDEX idx_entities_attributes ON entities USING gin (attributes);
CREATE INDEX idx_entities_namespace_type ON entities(namespace, type);
```

### 5.4 Embedding Dimension Configuration

The embedding dimension (default: 1536) must be consistent across the entire Cortex instance. Changing the embedding model or dimensions requires re-embedding all existing content. Cortex stores the active embedding config in a metadata table and refuses to start if the configured dimensions don't match stored data (with a flag to force re-indexing).

---

## 6. Iris Integration

### 6.1 Embedding Generation

Cortex calls Iris's Go SDK for all embedding operations:

```go
package embedding

import (
    "github.com/petal-labs/iris"
)

type Provider struct {
    client    *iris.Client
    provider  string
    model     string
    dimensions int
}

func NewProvider(cfg Config) (*Provider, error) {
    client, err := iris.NewClient(iris.WithBaseURL(cfg.IrisEndpoint))
    if err != nil {
        return nil, err
    }
    return &Provider{
        client:     client,
        provider:   cfg.EmbeddingProvider,
        model:      cfg.EmbeddingModel,
        dimensions: cfg.EmbeddingDimensions,
    }, nil
}

func (p *Provider) Embed(ctx context.Context, text string) ([]float32, error) {
    resp, err := p.client.Embeddings(ctx, iris.EmbeddingRequest{
        Provider: p.provider,
        Model:    p.model,
        Input:    []string{text},
    })
    if err != nil {
        return nil, fmt.Errorf("embedding failed: %w", err)
    }
    return resp.Embeddings[0], nil
}

func (p *Provider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
    resp, err := p.client.Embeddings(ctx, iris.EmbeddingRequest{
        Provider: p.provider,
        Model:    p.model,
        Input:    texts,
    })
    if err != nil {
        return nil, fmt.Errorf("batch embedding failed: %w", err)
    }
    return resp.Embeddings, nil
}
```

### 6.2 Summarization

Conversation summarization uses Iris's completion API:

```go
func (e *Engine) Summarize(ctx context.Context, messages []*Message, keepRecent int) (string, error) {
    // Separate messages into "to summarize" and "to keep"
    toSummarize := messages[:len(messages)-keepRecent]
    
    // Format messages for the summarization prompt
    formatted := formatMessages(toSummarize)
    
    resp, err := e.irisClient.Complete(ctx, iris.CompletionRequest{
        Provider: e.cfg.SummarizationProvider,
        Model:    e.cfg.SummarizationModel,
        Messages: []iris.Message{
            {Role: "system", Content: summarizationSystemPrompt},
            {Role: "user", Content: fmt.Sprintf("Summarize this conversation:\n\n%s", formatted)},
        },
        MaxTokens: 1024,
    })
    if err != nil {
        return "", fmt.Errorf("summarization failed: %w", err)
    }
    return resp.Content, nil
}

const summarizationSystemPrompt = `You are a conversation summarizer. Create a concise summary that preserves:
- Key decisions and conclusions
- Important facts, numbers, and data points
- Action items and commitments
- User preferences and requirements
- Technical details relevant to ongoing work

Omit pleasantries, filler, and redundant exchanges. Write in present tense as a reference document, not a narrative.`
```

### 6.3 Embedding Caching

Frequently repeated queries (e.g., the same search run across multiple workflow executions) don't need fresh embeddings every time. Cortex maintains an LRU cache of recent query embeddings:

```go
type EmbeddingCache struct {
    cache    *lru.Cache[string, []float32]
    provider *Provider
}

func (c *EmbeddingCache) Embed(ctx context.Context, text string) ([]float32, error) {
    key := hashText(text)
    if cached, ok := c.cache.Get(key); ok {
        return cached, nil
    }
    embedding, err := c.provider.Embed(ctx, text)
    if err != nil {
        return nil, err
    }
    c.cache.Add(key, embedding)
    return embedding, nil
}
```

Cache size is configurable (default: 1000 entries). Cache is in-memory only and cleared on restart.

---

## 7. CLI & Standalone Operation

Cortex is a standalone binary that can run independently of PetalFlow — useful for debugging, data management, and integration testing.

### 7.1 CLI Commands

```bash
# Start as MCP server (used by PetalFlow)
cortex serve --mcp
cortex serve --mcp --transport sse --port 9810

# Start as HTTP API server (standalone use, debugging)
cortex serve --http --port 9810

# Knowledge management
cortex knowledge ingest --namespace my-project \
    --collection docs \
    --file ./product-docs.md \
    --source "https://docs.example.com"

cortex knowledge ingest-dir --namespace my-project \
    --collection docs \
    --dir ./docs/ \
    --glob "**/*.md"

cortex knowledge search --namespace my-project \
    --query "How does authentication work?"

cortex knowledge stats --namespace my-project

# Conversation management
cortex conversation list --namespace my-project
cortex conversation export --namespace my-project --thread-id abc123

# Context management
cortex context list --namespace my-project
cortex context get --namespace my-project --key research_findings
cortex context set --namespace my-project --key config \
    --value '{"max_retries": 3}'

# Entity management
cortex entity list --namespace my-project
cortex entity list --namespace my-project --type organization
cortex entity query --namespace my-project --name "Acme Corp"
cortex entity search --namespace my-project --query "AI safety companies"
cortex entity relationships --namespace my-project --name "Acme Corp"
cortex entity merge --namespace my-project \
    --source "Google" --target "Alphabet"
cortex entity export --namespace my-project --format json

# Data management
cortex namespace list
cortex namespace delete my-old-project
cortex gc --expired-ttl --orphaned-chunks

# Configuration
cortex config show
cortex config set embedding_model text-embedding-3-large
cortex config test  # Verify Iris connectivity and embedding generation
```

### 7.2 Configuration

```yaml
# ~/.cortex/config.yaml

storage:
  backend: sqlite          # "sqlite" or "pgvector"
  data_dir: ~/.cortex/data
  # database_url: postgres://user:pass@host:5432/cortex  # for pgvector

iris:
  endpoint: http://localhost:8787
  
embedding:
  provider: openai
  model: text-embedding-3-small
  dimensions: 1536
  batch_size: 100          # Max texts per embedding API call
  cache_size: 1000         # LRU cache entries

summarization:
  provider: anthropic
  model: claude-sonnet-4-20250514
  max_tokens: 1024

conversation:
  auto_summarize_threshold: 50   # Messages before auto-summarize
  default_history_limit: 20
  semantic_search_enabled: true   # Embed messages on append for semantic search

knowledge:
  default_chunk_strategy: sentence
  default_chunk_max_tokens: 512
  default_chunk_overlap: 50
  default_search_top_k: 5

context:
  ttl_cleanup_interval: 60s     # How often to purge expired entries
  history_retention_days: 30     # How long to keep version history

entity:
  extraction_mode: full          # "off" | "sampled" | "whitelist" | "full"
  extraction_model: claude-haiku-4-5-20251001  # Fast model for NER
  extraction_batch_size: 10     # Queue items processed per batch
  extraction_interval: 5s       # Queue poll interval
  sample_rate: 1.0              # For "sampled" mode (0.0-1.0)
  whitelist_keywords: []        # For "whitelist" mode
  summary_regeneration_threshold: 5  # New mentions before resummarizing
  min_confidence: 0.5           # Minimum extraction confidence to store
  min_mentions_to_keep: 1       # Entities below this count may be pruned
  alias_fuzzy_threshold: 0.85   # String similarity for alias matching
  extraction_max_attempts: 5
  extraction_backoff: exponential
  extraction_dead_letter_policy: retain  # "retain" | "drop"

retention:
  conversation_retention_days: 0    # 0 = no auto-delete
  entity_stale_days: 0              # 0 = no auto-prune
  gc_interval: 24h                  # How often the garbage collector runs

namespace:
  allowed_namespaces: []            # Empty = allow all (for session scoping via MCP init)

server:
  log_level: info
  metrics_enabled: true
  metrics_port: 9811
  structured_logging: true          # JSON structured logs with namespace, thread_id, run_id, entity_id
  request_id_header: X-PetalFlow-Request-ID  # Propagated from MCP calls
```

---

## 8. Use Case Patterns

### 8.1 RAG Pipeline (Knowledge-Augmented Agent)

The most common pattern. An agent searches the knowledge store before generating a response, grounding its output in relevant documents.

```json
{
  "kind": "agent_workflow",
  "agents": {
    "support_agent": {
      "role": "Technical Support Specialist",
      "goal": "Answer user questions accurately using product documentation",
      "tools": [
        "cortex.knowledge.search",
        "cortex.conversation.history",
        "cortex.conversation.append"
      ]
    }
  },
  "tasks": {
    "answer_question": {
      "description": "1. Load conversation history for context. 2. Search knowledge base for relevant docs. 3. Answer the user's question grounded in the documentation. 4. Save the exchange to conversation memory.",
      "agent": "support_agent"
    }
  }
}
```

### 8.2 Iterative Research (Cross-Run Memory)

A research agent accumulates findings across multiple runs, building on prior work rather than starting from scratch.

```json
{
  "kind": "agent_workflow",
  "agents": {
    "researcher": {
      "role": "Research Analyst",
      "tools": [
        "web_search",
        "cortex.context.get",
        "cortex.context.merge",
        "cortex.knowledge.ingest",
        "cortex.knowledge.search"
      ]
    }
  },
  "tasks": {
    "research": {
      "description": "1. Check context for prior research on this topic (context.get 'research_{{input.topic}}'). 2. Search knowledge base for previously ingested findings. 3. Conduct new research via web_search. 4. Merge new findings into context (context.merge with deep_merge strategy). 5. Ingest any substantial new sources into knowledge store for future runs.",
      "agent": "researcher"
    }
  }
}
```

### 8.3 Multi-Agent Shared State

Multiple agents within a workflow contribute to a shared context object, similar to LangGraph's shared state pattern.

```json
{
  "kind": "agent_workflow",
  "agents": {
    "market_researcher": {
      "tools": ["web_search", "cortex.context.merge"]
    },
    "competitor_analyst": {
      "tools": ["web_search", "cortex.context.merge"]
    },
    "strategist": {
      "tools": ["cortex.context.get"]
    }
  },
  "tasks": {
    "market_research": {
      "description": "Research market trends. Merge findings into context key 'analysis_data' under 'market' sub-key.",
      "agent": "market_researcher"
    },
    "competitor_research": {
      "description": "Analyze competitor landscape. Merge findings into context key 'analysis_data' under 'competitors' sub-key.",
      "agent": "competitor_analyst"
    },
    "synthesize": {
      "description": "Read the full 'analysis_data' context (which now contains both market and competitor findings) and write a strategy memo.",
      "agent": "strategist"
    }
  },
  "execution": {
    "strategy": "custom",
    "tasks": {
      "market_research": { "depends_on": [] },
      "competitor_research": { "depends_on": [] },
      "synthesize": { "depends_on": ["market_research", "competitor_research"] }
    }
  }
}
```

### 8.4 Conversational Agent with Long-Term Memory

An assistant that maintains conversation history, automatically summarizes old exchanges, and can recall details from prior conversations.

```json
{
  "kind": "agent_workflow",
  "agents": {
    "assistant": {
      "role": "Personal Assistant",
      "tools": [
        "cortex.conversation.history",
        "cortex.conversation.append",
        "cortex.conversation.summarize",
        "cortex.conversation.search"
      ]
    }
  },
  "tasks": {
    "respond": {
      "description": "1. Load conversation history (last 20 messages + summary). 2. If the user references something from a past conversation, search conversation history semantically. 3. Generate a response. 4. Append both the user message and your response to conversation memory. 5. If thread exceeds 50 messages, trigger summarization.",
      "agent": "assistant"
    }
  }
}
```

### 8.5 Entity-Aware Competitive Intelligence

An agent that automatically builds and queries a knowledge graph of companies, people, and products as it researches a market. Entity memory transforms raw research into structured, queryable intelligence.

```json
{
  "kind": "agent_workflow",
  "agents": {
    "intelligence_analyst": {
      "role": "Competitive Intelligence Analyst",
      "goal": "Build and maintain a structured understanding of the competitive landscape",
      "tools": [
        "web_search",
        "cortex.knowledge.ingest",
        "cortex.knowledge.search",
        "cortex.entity.query",
        "cortex.entity.search",
        "cortex.entity.relationships",
        "cortex.entity.update",
        "cortex.context.merge"
      ]
    }
  },
  "tasks": {
    "research_competitor": {
      "description": "1. Query entity memory for any existing knowledge about '{{input.company}}'. 2. Check entity relationships to see known connections (investors, partners, key people). 3. Search web for recent news and developments. 4. Ingest substantial findings into knowledge store (entity extraction runs automatically). 5. Manually update entity attributes with structured facts (funding amount, employee count, HQ location). 6. Merge a competitive summary into context key 'competitive_landscape' under the company's name.",
      "agent": "intelligence_analyst"
    }
  }
}
```

Over multiple runs targeting different companies, the entity graph grows organically. The analyst can later ask "What companies compete with Acme Corp?" or "Who are the key people we've identified in the quantum computing space?" and get answers from the accumulated entity graph — without anyone manually building that structure.

### 8.6 Hybrid Conversation + Entity Recall

An agent that combines conversation context with entity knowledge for contextual, personalized responses. When a user says "tell me more about Alice," the agent pulls structured entity data and recent conversational mentions simultaneously.

```json
{
  "kind": "agent_workflow",
  "agents": {
    "advisor": {
      "role": "Account Advisor",
      "goal": "Provide informed, contextual responses using both conversation history and accumulated entity knowledge",
      "tools": [
        "cortex.conversation.history",
        "cortex.conversation.append",
        "cortex.conversation.search",
        "cortex.entity.query",
        "cortex.entity.relationships",
        "cortex.knowledge.search"
      ]
    }
  },
  "tasks": {
    "respond": {
      "description": "1. Load conversation history for immediate context. 2. If the user references a person, company, or product by name, query entity memory for structured facts and relationships. 3. Search recent conversation history for additional context about when and how the entity was discussed. 4. Synthesize entity knowledge + conversational context into a response. 5. Append exchange to conversation memory (entity extraction happens automatically).",
      "agent": "advisor"
    }
  }
}
```

This pattern shows the synergy between primitives — the entity graph provides structured "what we know" facts, while conversation search provides "what we've discussed" context. The combination is more useful than either alone.

---

## 9. Consistency Guarantees

Cortex has different consistency characteristics across its primitives, and these must be understood to avoid surprises.

**Conversation, knowledge, and context operations** are strongly consistent. Writes are immediately visible to subsequent reads within the same namespace. SQLite's single-writer model provides serializable isolation. pgvector provides transactional consistency with standard PostgreSQL isolation levels.

**Entity memory is eventually consistent.** Entity extraction runs asynchronously via the extraction queue. After a `conversation.append` or `knowledge.ingest` call, entities may take seconds to appear in entity queries. The target extraction pipeline latency is under 5 seconds for a single item under normal load.

**Context entries provide strong read-after-write.** A `context.set` followed immediately by a `context.get` for the same key is guaranteed to return the new value. Optimistic concurrency via version numbers prevents lost updates from concurrent writers.

**No cross-namespace transactional guarantees.** Operations within a single namespace are atomic. Operations spanning multiple namespaces are independent — there is no distributed transaction support. This is by design; namespaces are isolation boundaries.

**Entity extraction queue ordering.** Items are processed roughly in FIFO order, but exact ordering is not guaranteed during concurrent processing. For most use cases this is irrelevant — entity resolution is idempotent.

---

## 10. Security & Multi-Tenancy

Cortex v1.0 assumes trusted internal deployment. It is designed to run behind PetalFlow's daemon, not exposed directly to the internet. The following security properties apply:

**v1.0 (current):**
- No authentication on MCP or HTTP interfaces. All callers are trusted.
- Namespace isolation is enforced at the service layer — tool calls that reference a namespace outside of `allowed_namespaces` (set during MCP initialization) are rejected.
- Sensitive config values (API keys, database credentials) are stored encrypted at rest in the registry.
- No PII detection or data classification.

**v1.1 (planned):**
- API key authentication for HTTP mode.
- Role-based namespace isolation (read-only, read-write, admin per namespace).
- Audit logging for all write operations.
- PII detection hooks on ingest (flag but don't block, with configurable policy).

**Important:** Do not deploy Cortex multi-tenant without understanding that v1.0 has no authentication. Any process that can reach the Cortex endpoint can read/write any namespace. The `allowed_namespaces` MCP init parameter provides defense-in-depth when PetalFlow is the sole client, but is not a security boundary.

---

## 11. Operational Considerations

### 11.1 Observability

Cortex exposes Prometheus-compatible metrics on the configured `metrics_port`. All metrics are labeled with `namespace` where applicable.

**Core metrics:**

| Metric | Type | Description |
|---|---|---|
| `cortex_operations_total` | counter | Total operations by primitive and action |
| `cortex_operation_duration_seconds` | histogram | Latency by primitive and action |
| `cortex_embedding_latency_seconds` | histogram | Iris embedding call latency |
| `cortex_search_latency_seconds` | histogram | Vector search latency (p50, p95, p99) |
| `cortex_extraction_queue_size` | gauge | Current entity extraction queue depth |
| `cortex_extraction_dead_letter_count` | gauge | Failed extraction items awaiting review |
| `cortex_namespace_storage_bytes` | gauge | Per-namespace storage usage |
| `cortex_context_conflicts_total` | counter | Optimistic concurrency conflicts on context.merge/set |
| `cortex_active_namespaces` | gauge | Number of namespaces with recent activity |

**Structured logging.** When `structured_logging: true` (default), Cortex emits JSON-formatted logs with contextual fields: `namespace`, `thread_id`, `run_id`, `entity_id`, `request_id`, `action`, `duration_ms`. Request IDs are propagated from PetalFlow's MCP calls via the `X-PetalFlow-Request-ID` header.

**Debug query logging.** For selected namespaces (configurable), Cortex can log search queries with their top-k results and scores. This is off by default and intended for debugging retrieval quality issues.

### 11.2 Data Retention & Garbage Collection

Conversation history, knowledge corpora, and entity graphs grow indefinitely without lifecycle management. Cortex includes a background garbage collector that runs on a configurable interval.

```yaml
retention:
  conversation_retention_days: 90    # Delete threads older than N days (0 = no auto-delete)
  entity_stale_days: 365             # Prune entities with no mentions in N days (0 = no auto-prune)
  context_run_retention_days: 30     # Delete run-scoped context entries after N days
  gc_interval: 24h                   # How often the garbage collector runs
```

The garbage collector:
- Deletes expired TTL context entries (runs every `ttl_cleanup_interval`, separate from GC).
- Deletes conversations and threads older than `conversation_retention_days`.
- Prunes entities with `last_seen_at` older than `entity_stale_days` and `mention_count` below a configurable threshold.
- Removes orphaned chunks whose parent documents have been deleted.
- Logs all deletions with counts for auditability.

### 11.3 Backup & Restore

**SQLite:** Backup is a file copy of the data directory. Cortex exposes a `cortex backup --output /path/to/backup.db` CLI command that creates a consistent snapshot using SQLite's online backup API (no downtime).

**PostgreSQL:** Standard `pg_dump` workflows apply. Cortex does not manage PostgreSQL backups directly.

**API-triggered backup (future):** A `POST /api/admin/backup` endpoint for infrastructure automation. Deferred to v1.1.

### 11.4 Schema & Data Evolution

All schema changes are managed via embedded migrations that run automatically on startup.

- **Embedding dimension is locked at startup.** Cortex stores the active embedding configuration in a metadata table. If the configured dimensions don't match stored data, Cortex refuses to start. A `--force-reindex` CLI flag triggers background re-embedding of all content with the new model.
- **Adding fields to entities/documents.** New optional fields are added via `ALTER TABLE` migrations. Existing rows get default/null values. No data loss.
- **Switching backends (SQLite → pgvector).** An export/import pipeline is provided: `cortex export --namespace X --format json` and `cortex import --backend pgvector --file export.json`. This is a manual operation, not automatic failover.
- **Major version changes** (new primitives, breaking field renames) increment the major version and require an explicit `cortex migrate` command. Data is namespaced, so v2 can run side-by-side with v1 during a gradual migration.

---

## 12. Open Questions & Future Considerations

### 12.1 Memory Policies

Production deployments need data lifecycle controls: retention policies (auto-delete after N days), size limits (per namespace, per collection), and compliance features (PII detection, data residency). These are critical for enterprise adoption but are v1.1 scope.

### 12.2 Multi-Modal Knowledge

The current knowledge store handles text only. Future versions should support image embeddings (CLIP), audio transcripts, and structured data (tables, CSVs). The storage interface already accommodates this — `content_type` can extend to new modalities, and the embedding provider can switch models per content type.

### 12.3 Incremental Re-Embedding

When switching embedding models (e.g., from `text-embedding-3-small` to `text-embedding-3-large`), all existing vectors must be regenerated. Cortex should support background re-embedding that processes chunks in batches without blocking queries. During the transition, search quality may degrade (mixed embeddings). A v1.1 feature.

### 12.4 Webhook Notifications

Cortex could notify external systems when certain events occur — new knowledge ingested, context key updated, conversation summarized. This would integrate with PetalFlow's planned webhook nodes and enable event-driven workflows triggered by memory changes.

### 12.5 MCP Resources

The MCP protocol defines a "resources" primitive that Cortex could expose — e.g., knowledge collections as browseable resources, conversation threads as readable resources. This would enable richer integration with MCP-aware clients beyond PetalFlow. Deferred pending the tool contract FRD's v1.1 resource support.

### 12.6 Rate Limiting for Iris Calls

Cortex makes potentially high volumes of embedding and completion calls to Iris (which proxies to external LLM providers). The current spec has no rate limiting. For production deployments with cost sensitivity, Cortex should support configurable rate limits on Iris calls — both global and per-namespace. A simple token bucket with configurable requests-per-second would prevent runaway costs from bulk ingest operations or extraction storms. v1.1 scope.

### 12.7 Token Accounting

Cortex has no visibility into how many tokens it consumes across embedding, summarization, and entity extraction calls. Adding per-namespace token counters (tracked via Iris response metadata) would enable cost attribution, budget enforcement, and usage dashboards. This depends on Iris exposing token usage in its response format. v1.1 scope.

---

## 13. Implementation Plan

### Phase 1: Core Service & Storage (Foundation)

**Scope:** Cortex binary, storage interface, SQLite+vec0 backend, Iris embedding integration, all four memory primitives with core operations, entity extraction pipeline.

```
cortex/
├── cmd/
│   └── cortex/
│       └── main.go              // CLI entrypoint
├── internal/
│   ├── server/
│   │   ├── mcp.go               // MCP server implementation
│   │   └── http.go              // HTTP API (standalone mode)
│   ├── conversation/
│   │   ├── engine.go            // Conversation memory logic
│   │   └── engine_test.go
│   ├── knowledge/
│   │   ├── engine.go            // Knowledge store logic
│   │   ├── chunker.go           // Document chunking strategies
│   │   └── engine_test.go
│   ├── context/
│   │   ├── engine.go            // Workflow context logic
│   │   ├── merge.go             // Merge strategy implementations
│   │   └── engine_test.go
│   ├── entity/
│   │   ├── engine.go            // Entity memory logic
│   │   ├── extractor.go         // LLM-powered NER extraction
│   │   ├── resolver.go          // Name/alias resolution and deduplication
│   │   ├── queue.go             // Async extraction queue processor
│   │   └── engine_test.go
│   ├── embedding/
│   │   ├── provider.go          // Iris embedding client
│   │   ├── cache.go             // LRU embedding cache
│   │   └── provider_test.go
│   └── storage/
│       ├── backend.go           // Storage interface
│       ├── sqlite/
│       │   ├── sqlite.go        // SQLite+vec0 implementation
│       │   ├── migrations.go    // Schema migrations
│       │   └── sqlite_test.go
│       └── pgvector/
│           ├── pgvector.go      // PostgreSQL+pgvector implementation
│           ├── migrations.go
│           └── pgvector_test.go
├── go.mod
└── go.sum
```

**Deliverables:**
- Cortex binary with MCP server mode (stdio)
- SQLite+vec0 storage backend with all schema tables (including entity tables)
- Conversation memory: append, history, clear
- Knowledge store: ingest (fixed + sentence chunking), search, delete
- Workflow context: get, set, merge (deep_merge + append), list, delete
- Entity memory: query, search, list, update, merge, delete
- Entity extraction pipeline: async queue processor, LLM-powered NER via Iris, name resolution, alias deduplication
- Extraction hooks on `conversation.append` and `knowledge.ingest`
- Iris embedding integration with batch support
- Integration test suite validating MCP protocol compliance

### Phase 2: Advanced Features

**Scope:** Conversation summarization, semantic chunking, conversation search, context history and optimistic concurrency, entity summary regeneration, embedding cache, CLI management commands.

**Deliverables:**
- Conversation summarization via Iris completion API
- Semantic chunking strategy
- Semantic search across conversation history
- Context version history and optimistic concurrency control
- TTL expiration with background cleanup
- Entity summary auto-regeneration on mention threshold
- Entity relationship graph traversal (multi-hop queries)
- Entity extraction confidence tuning and minimum-mention pruning
- Embedding LRU cache
- CLI commands for knowledge management (ingest, ingest-dir, search, stats)
- CLI commands for context, conversation, and entity management

### Phase 3: pgvector & Production Hardening

**Scope:** PostgreSQL+pgvector backend, HNSW indexing, metadata pre-filtering, connection pooling, metrics, SSE transport.

**Deliverables:**
- pgvector storage backend with HNSW indexes
- GIN indexes for metadata pre-filtering
- MCP SSE transport mode
- Prometheus metrics endpoint
- Configurable retention policies (namespace-level)
- Namespace management (list, delete, stats)
- Graceful shutdown and connection draining

### Phase 4: Ecosystem Integration

**Scope:** Hybrid search (vector + BM25), bulk ingest optimization, PetalFlow UI integration, documentation.

**Deliverables:**
- Hybrid search with reciprocal rank fusion (SQLite FTS5 / PostgreSQL tsvector)
- Bulk ingest pipeline with progress reporting
- PetalFlow UI integration (knowledge browser, context inspector, conversation viewer)
- User documentation and integration guides
- Published overlay to PetalFlow community overlay registry (when available)
