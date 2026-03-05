# Cortex

[![Build Status](https://github.com/petal-labs/cortex/actions/workflows/ci.yml/badge.svg)](https://github.com/petal-labs/cortex/actions/workflows/ci.yml)&nbsp;
[![Go Report Card](https://goreportcard.com/badge/github.com/petal-labs/cortex?style=flat)](https://goreportcard.com/report/github.com/petal-labs/cortex)&nbsp;
[![GoDoc](https://godoc.org/github.com/petal-labs/cortex?status.svg)](https://godoc.org/github.com/petal-labs/cortex)&nbsp;
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://github.com/petal-labs/cortex/blob/main/LICENSE)

Cortex is a memory and knowledge service for AI agents. It provides persistent context, vector-backed knowledge retrieval, and conversation memory through the Model Context Protocol (MCP).

## Features

**Four Memory Primitives:**

- **Conversation Memory** — Agent dialogue history with semantic search and auto-summarization
- **Knowledge Store** — Vector-indexed documents with hybrid search (semantic + full-text)
- **Workflow Context** — Key-value state that persists across tasks and runs
- **Entity Memory** — Auto-extracted knowledge graph of people, organizations, and concepts

**Production Ready:**

- SQLite + vec0 for single-node deployments (zero infrastructure)
- PostgreSQL + pgvector for production scale
- Prometheus metrics and structured logging
- Backup, export, and garbage collection

**Developer Experience:**

- MCP-native — works with any MCP-compatible client
- Web dashboard for browsing data
- Terminal UI for quick inspection
- Comprehensive CLI for all operations

## Installation

```bash
go install github.com/petal-labs/cortex/cmd/cortex@latest
```

Or build from source:

```bash
git clone https://github.com/petal-labs/cortex.git
cd cortex
go build -o cortex ./cmd/cortex
```

## Quick Start

### Start the MCP Server

```bash
# Start with stdio transport (default)
cortex serve

# Start with SSE transport for web clients
cortex serve --transport sse --port 9810

# Restrict to a specific namespace
cortex serve --namespace my-project
```

### Use the CLI

```bash
# Ingest a document
cortex knowledge ingest --collection docs --title "README" --file README.md

# Search knowledge
cortex knowledge search "how to install"

# View conversation history
cortex conversation history --thread-id my-thread

# List entities
cortex entity list --namespace default
```

### Launch the TUI

```bash
cortex tui
```

Navigate with `1-5` to switch sections, `j/k` to move, `Enter` to select, `q` to quit.

## Configuration

Create `~/.cortex/config.yaml`:

```yaml
storage:
  backend: sqlite  # or "pgvector"
  data_dir: ~/.cortex/data

iris:
  endpoint: http://localhost:8000  # Iris embedding service

embedding:
  model: text-embedding-3-small
  dimensions: 1536
  cache_size: 10000

conversation:
  auto_summarize_threshold: 50
  semantic_search_enabled: true

knowledge:
  default_chunk_strategy: sentence  # fixed, sentence, paragraph, semantic
  default_chunk_max_tokens: 512
  default_chunk_overlap: 50

entity:
  extraction_mode: full  # off, sampled, whitelist, full

server:
  metrics_enabled: true
  metrics_port: 9811
  structured_logging: true
```

### PostgreSQL Setup

For production deployments:

```yaml
storage:
  backend: pgvector
  database_url: postgres://user:pass@localhost:5432/cortex
```

Ensure pgvector extension is installed:

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

## MCP Tools

Cortex exposes 16 MCP tools across the four memory primitives:

### Conversation

| Tool | Description |
|------|-------------|
| `conversation_append` | Add a message to a conversation thread |
| `conversation_history` | Retrieve conversation history |
| `conversation_search` | Semantic search across messages |
| `conversation_summarize` | Summarize and compress history |

### Knowledge

| Tool | Description |
|------|-------------|
| `knowledge_ingest` | Ingest a document with chunking |
| `knowledge_bulk_ingest` | Batch ingest multiple documents |
| `knowledge_search` | Hybrid search (vector + full-text) |
| `knowledge_collections` | Create, list, delete collections |

### Context

| Tool | Description |
|------|-------------|
| `context_get` | Retrieve a value by key |
| `context_set` | Store a value with optional TTL |
| `context_merge` | Merge values with strategy |
| `context_list` | List keys with prefix filter |
| `context_history` | View version history for a key |

### Entity

| Tool | Description |
|------|-------------|
| `entity_query` | Look up entity by name or alias |
| `entity_search` | Semantic search across entities |
| `entity_relationships` | Get entity relationships |
| `entity_update` | Modify entity attributes |
| `entity_merge` | Combine duplicate entities |
| `entity_list` | List entities with filters |

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    MCP Clients                          │
│           (AI Agents, IDEs, Tools)                      │
└─────────────────────┬───────────────────────────────────┘
                      │ MCP (stdio/SSE)
                      ▼
┌─────────────────────────────────────────────────────────┐
│                     Cortex                              │
│  ┌─────────────┐ ┌─────────────┐ ┌─────────────────┐   │
│  │Conversation │ │ Knowledge   │ │ Workflow Context│   │
│  │   Memory    │ │   Store     │ │                 │   │
│  └──────┬──────┘ └──────┬──────┘ └────────┬────────┘   │
│         │               │                  │            │
│  ┌──────┴───────────────┴──────────────────┴──────┐    │
│  │              Entity Memory                      │    │
│  │         (Auto-extracted Knowledge Graph)        │    │
│  └─────────────────────┬───────────────────────────┘    │
│                        │                                │
│  ┌─────────────────────┴───────────────────────────┐   │
│  │            Storage Backend                       │   │
│  │      (SQLite+vec0 or PostgreSQL+pgvector)       │   │
│  └─────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────┐
│                     Iris                                │
│           (Embedding Generation)                        │
└─────────────────────────────────────────────────────────┘
```

## CLI Reference

### Server

```bash
cortex serve [flags]
  --transport string   Transport mode: stdio or sse (default "stdio")
  --port int           Port for SSE transport (default 9810)
  --namespace string   Restrict to a single namespace
```

### Knowledge Store

```bash
cortex knowledge ingest --collection <name> --title <title> --file <path>
cortex knowledge ingest-dir --collection <name> --dir <path> --pattern "*.md"
cortex knowledge search <query> [--collection <name>] [--mode hybrid]
cortex knowledge collections [--namespace <ns>]
cortex knowledge create-collection --name <name> [--description <desc>]
cortex knowledge stats [--namespace <ns>]
```

### Conversation Memory

```bash
cortex conversation history --thread-id <id> [--limit 50]
cortex conversation append --thread-id <id> --role user --content "message"
cortex conversation search <query> [--namespace <ns>]
cortex conversation list [--namespace <ns>]
cortex conversation clear --thread-id <id>
cortex conversation summarize --thread-id <id>
```

### Workflow Context

```bash
cortex context get <key> [--run-id <id>]
cortex context set <key> <value> [--ttl 24h]
cortex context list [--prefix <prefix>]
cortex context delete <key>
cortex context history <key>
cortex context cleanup [--expired-ttl] [--old-runs]
```

### Entity Memory

```bash
cortex entity list [--type person] [--sort mention_count]
cortex entity get <id>
cortex entity search <query>
cortex entity create --name "John Doe" --type person
cortex entity add-alias <id> --alias "J. Doe"
cortex entity add-relationship <source-id> <target-id> --type works_at
cortex entity merge <keep-id> <remove-id>
cortex entity queue-stats
```

### Maintenance

```bash
cortex gc [--all] [--dry-run]
cortex backup --output backup.db
cortex export --namespace <ns> --output export.json
cortex namespace stats [--namespace <ns>]
cortex namespace delete <ns> [--force]
```

### Terminal UI

```bash
cortex tui [--namespace <ns>]
```

## Search Modes

Cortex supports three search modes:

| Mode | Description |
|------|-------------|
| `vector` | Semantic similarity using embeddings |
| `fts` | Full-text search using BM25 (SQLite) or ts_rank (PostgreSQL) |
| `hybrid` | Combines vector and FTS using Reciprocal Rank Fusion |

```bash
cortex knowledge search "machine learning" --mode hybrid
```

## Namespaces

All data is isolated by namespace. Use namespaces to separate:

- Different projects or workflows
- Different tenants in multi-tenant deployments
- Development vs production data

```bash
# All commands accept --namespace
cortex knowledge search "query" --namespace acme/research
cortex serve --namespace acme/research  # Restricts MCP access
```

## Observability

### Prometheus Metrics

When `metrics_enabled: true`, Cortex exposes metrics at `:9811/metrics`:

- `cortex_operations_total` — Operations by primitive, action, namespace, status
- `cortex_operation_duration_seconds` — Operation latency histogram
- `cortex_search_latency_seconds` — Search-specific latency
- `cortex_embedding_requests_total` — Embedding API calls
- `cortex_extraction_queue_size` — Entity extraction queue depth

### Health Check

```bash
curl http://localhost:9811/health
```

## Development

### Run Tests

```bash
go test ./...
```

### Build

```bash
go build -o cortex ./cmd/cortex
```

## License

MIT
