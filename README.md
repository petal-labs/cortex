<div align="center">
   <img src="media/cortex_banner.jpg" alt="cortex" width="100%" />
  <h1>Cortex</h1>

  <hr/>
</div>
<div>
  <p>
    <a href="https://github.com/petal-labs/cortex/actions/workflows/ci.yml"><img src="https://github.com/petal-labs/cortex/actions/workflows/ci.yml/badge.svg" alt-"Build Status"></a>&nbsp;
    <a href="https://goreportcard.com/report/github.com/petal-labs/cortex"><img src="https://goreportcard.com/badge/github.com/petal-labs/cortex?style=flat" alt="Go Report Card"></a>&nbsp;
    <a href="https://godoc.org/github.com/petal-labs/cortex"><img src="https://godoc.org/github.com/petal-labs/cortex?status.svg" alt="GoDoc"></a>&nbsp;
    <a href="https://github.com/petal-labs/cortex/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License: MIT"></a>&nbsp;
    <a href="https://github.com/petal-labs/cortex/releases"><img src="https://img.shields.io/github/v/release/petal-labs/cortex?include_prereleases&style=flat" alt="GitHub release"></a>&nbsp;
 </p>
</div>

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

### Download Binary

Prebuilt binaries are published for Linux (amd64, arm64), macOS (amd64,
arm64), and Windows (amd64) on the
[releases page](https://github.com/petal-labs/cortex/releases/latest).

On Linux and macOS, this resolves the current release and your platform
automatically:

```bash
VERSION=$(curl -sI https://github.com/petal-labs/cortex/releases/latest | awk -F'/v' '/^[Ll]ocation:/ {print $2}' | tr -d '\r')
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
[ "$ARCH" = "x86_64" ] && ARCH=amd64
[ "$ARCH" = "aarch64" ] && ARCH=arm64

curl -LO "https://github.com/petal-labs/cortex/releases/latest/download/cortex_${VERSION}_${OS}_${ARCH}.tar.gz"
tar -xzf "cortex_${VERSION}_${OS}_${ARCH}.tar.gz"
sudo mv "cortex_${VERSION}_${OS}_${ARCH}/cortex" /usr/local/bin/
```

`/releases/latest` redirects to the current tag, so `VERSION` tracks whatever
is newest without this page needing an edit each release.

**Windows:**

Download the `cortex_<version>_windows_amd64.zip` asset from the
[latest release](https://github.com/petal-labs/cortex/releases/latest),
extract it, and add the extracted folder to your PATH.

### Build from Source

Requires Go 1.24+ and CGO enabled.

```bash
git clone https://github.com/petal-labs/cortex.git
cd cortex
go build -o cortex ./cmd/cortex
```

### Verify Installation

```bash
cortex --version
# cortex version 1.0.1 (a1b2c3d, 2026-08-16T22:00:00Z)
```

Release binaries report the version, commit, and build date. A binary built
from source without ldflags reports `cortex version dev`.

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
# Ingest a document (namespace defaults to "default")
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

embedding:
  provider: openai  # openai, voyageai, gemini, ollama
  model: text-embedding-3-small
  dimensions: 1536
  cache_size: 10000
  timeout: 120s  # 0 disables the timeout

summarization:
  provider: anthropic  # anthropic, openai, gemini, ollama
  model: claude-sonnet-4-6
  timeout: 120s  # 0 disables the timeout

conversation:
  auto_summarize_threshold: 50
  semantic_search_enabled: true

knowledge:
  default_chunk_strategy: sentence  # fixed, sentence, paragraph, semantic
  default_chunk_max_tokens: 512
  default_chunk_overlap: 50

entity:
  extraction_mode: full  # off, sampled, whitelist, full
  extraction_max_attempts: 5
  extraction_backoff: exponential  # fixed, exponential
  extraction_dead_letter_policy: retain  # retain, drop
  extraction_timeout: 120s  # 0 disables the timeout

server:
  metrics_enabled: true
  metrics_port: 9811
  structured_logging: true
  shutdown_timeout: 30s
```

### Config Validation

Cortex validates the configuration at startup and refuses to start on bad
values, reporting every violation at once with its config key:

```
invalid config:
  - storage.database_url is required when storage.backend is "pgvector"
  - embedding.dimensions must be > 0 (got 0)
  - entity.extraction_backoff "linear" is not supported (supported: "fixed", "exponential")
```

This replaces late, opaque runtime failures — a non-positive
`retention.gc_interval`, for example, used to panic the background ticker
after startup.

### Timeouts

Provider calls are bounded so a hung upstream fails fast instead of blocking
indefinitely:

| Option | Default | Bounds |
|--------|---------|--------|
| `embedding.timeout` | `120s` | Embedding provider calls |
| `summarization.timeout` | `120s` | Conversation summarization calls |
| `entity.extraction_timeout` | `120s` | Entity extraction calls |
| `server.shutdown_timeout` | `30s` | Graceful drain of background workers |

Setting any of the provider timeouts to `0` disables it (unbounded). A timeout
is only applied when the caller supplied no deadline of its own.

### PostgreSQL Setup

For production deployments:

```yaml
storage:
  backend: pgvector
  database_url: postgres://user:pass@localhost:5432/cortex

  # Optional explicit pool sizing. 0 (the default) defers to the
  # pool_max_conns/pool_min_conns query params on database_url, then to
  # pgx's built-in defaults. Explicit values take precedence over the URL.
  pool_max_conns: 0
  pool_min_conns: 0
```

Ensure pgvector extension is installed:

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

**Embedding dimensions.** The pgvector schema is generated from
`embedding.dimensions`, so a non-1536 model works without patching DDL. The
dimension is baked into the `vector(N)` columns when the schema is first
created. If you later change `embedding.dimensions` against an existing
database, Cortex fails at startup rather than writing ragged vectors:

```
existing embedding columns are vector(1536) but embedding.dimensions is 768;
run an ALTER TABLE ... ALTER COLUMN embedding TYPE vector(768) migration
or start with a fresh database
```

Changing dimensions requires re-embedding your data — the stored vectors are
not convertible.

**Migrations.** Both backends run versioned migrations at startup and record
the applied version in `cortex_metadata.schema_version`. Upgrades apply only
the pending migrations; no manual DDL step is needed.

## MCP Tools

Cortex exposes 19 MCP tools across the four memory primitives:

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

Entity types are `person`, `organization`, `product`, `location`, `concept`,
`event`, and `other`. Any other value is rejected — an unrecognized type used
to be silently remapped to an unrelated one.

### Response Contract

Every tool response is a declared, versioned JSON shape. Each top-level
response carries `schema_version`, so clients can feature-detect contract
changes instead of guessing:

```json
{
  "schema_version": 1,
  "results": [
    {
      "chunk": {
        "id": "chunk-1",
        "document_id": "doc-1",
        "namespace": "ns",
        "collection_id": "col-1",
        "content": "chunk content",
        "index": 0,
        "token_count": 2
      },
      "score": 0.91,
      "rank": 1,
      "document_title": "Doc",
      "source": "test://src"
    }
  ],
  "query": "chunk",
  "total_found": 1
}
```

The contract guarantees:

- **`schema_version`** is bumped only on a breaking change to a response shape.
- **Empty collections serialize as `[]` and `{}`**, never `null`, so clients can
  iterate without a nil check.
- **Embedding vectors are stripped from search results.** They were a large,
  unusable payload for MCP clients; use the Go API if you need raw vectors.
- **Zero-value timestamps are omitted** rather than rendered as the misleading
  `0001-01-01T00:00:00Z`.

### Argument Handling

Tool arguments are validated rather than silently defaulted:

- An argument that is **present but of the wrong type** is rejected with a
  message naming the parameter and the type it got — e.g.
  `parameter "limit" must be a number, got string`. Previously a wrong-typed
  argument fell back to the default, so a client bug looked like a working call.
- **Metadata, filters, and attributes are string-valued.** Non-string values are
  rejected with an actionable message —
  `parameter "metadata" values must be strings; key "count" has a number value (send "3" as a string)`.
  Cortex used to stringify them silently, which made filter matching ambiguous.
- **Large integers keep their precision** through the JSON argument path;
  a value that would be corrupted by float64 decoding is rejected instead.

### Errors

Tool errors are classified before they reach the client. Known client-facing
conditions (not found, invalid input, version conflict, batch too large) return
their own message. Anything else returns `internal server error` and is logged
server-side with full detail, so SQL fragments, connection URLs, and other
internals never leak to MCP clients.

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
cortex context cleanup [--expired] [--run-id <id>]
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
cortex namespace delete --namespace <ns> --confirm
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
# All commands accept --namespace (defaults to "default")
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

### Health and Readiness

Cortex exposes two distinct probes on the metrics port. They answer different
questions, and conflating them gets healthy pods killed during a database blip:

| Endpoint | Question | Checks | Status |
|----------|----------|--------|--------|
| `/health` | Is the process alive? | Nothing — only that the HTTP server accepts connections | Always `200 ok` |
| `/ready` | Can it serve traffic? | Storage backend reachability, bounded at 2s | `200 ready` / `503 not ready: <reason>` |

```bash
curl http://localhost:9811/health   # liveness
curl http://localhost:9811/ready    # readiness
```

Wire `/health` to your orchestrator's liveness probe and `/ready` to its
readiness probe. A dead database should pull the instance out of rotation
(`/ready` fails), not restart it (`/health` still passes).

```yaml
# Kubernetes
livenessProbe:
  httpGet: { path: /health, port: 9811 }
readinessProbe:
  httpGet: { path: /ready, port: 9811 }
```

## Reliability

The server is built to fail loudly and recover cleanly rather than degrade
silently:

- **Failures propagate.** Embedding failures during knowledge ingestion return
  an error instead of persisting a document with no vectors — which used to
  leave content permanently invisible to semantic search.
- **Panic recovery.** MCP tool and resource handlers recover through the
  server's recovery middleware; the entity extraction queue and the garbage
  collector recover in their own loops, log the panic with its stack, and keep
  running. A nil-map access in one batch no longer takes down the process.
- **Retries with backoff.** Transient embedding-provider failures (5xx,
  network, 429) are retried up to 3 times with exponential backoff and jitter;
  permanent failures (bad API key, malformed request, undecodable response)
  are not retried at all. Classification comes from Iris's typed error
  taxonomy, not string matching, and failures are logged with the provider's
  status, error code, and request ID for escalation.
- **Auto-batching.** Oversized `EmbedBatch` inputs are split to fit the
  provider's limit instead of failing the whole call.
- **Response validation.** Returned vectors are checked against
  `embedding.dimensions`, catching a mismatched model before bad data lands in
  storage.
- **Queue backoff.** The entity extraction queue honors
  `extraction_backoff` between attempts and drops non-retryable work to the
  dead-letter policy instead of spinning on it.
- **Bounded shutdown.** On signal, the queue processor and garbage collector
  are drained within `server.shutdown_timeout` before the process exits.

## Development

### Run Tests

```bash
make test              # full suite
make test-short        # skips container-backed integration tests
make test-cover        # with coverage
make test-integration  # dual-backend conformance suite (requires Docker)
```

Both storage backends are held to the same behavioral contract by a shared
conformance suite in `internal/storage/conformance/`. The SQLite run is
in-process; the pgvector run spins up Postgres + pgvector via testcontainers
and is skipped automatically in short mode, on Windows, or when no healthy
Docker provider is available.

### Build

```bash
go build -o cortex ./cmd/cortex
```

## Versioning

Cortex follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html). As
of 1.0.0, three surfaces are covered by that guarantee:

| Surface | Contract |
|---------|----------|
| MCP tool arguments and responses | `schema_version` identifies the response contract; a breaking shape change bumps it and the major version |
| `pkg/types` | Exported types are stable within a major version |
| Storage schema | Versioned migrations apply forward automatically; no manual DDL |

The `internal/` packages are explicitly not covered — they may change in any
release. Use the MCP interface or `pkg/types` for anything you need to hold
stable.

See [CHANGELOG.md](./CHANGELOG.md) for the upgrade notes from 0.3.x.

## License

MIT
