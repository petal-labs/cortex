# Changelog

All notable changes to Cortex will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.1.0] - 2026-08-17

Reconciles the CLI with the interface README and `examples/cli-basics` have
always documented. Several commands were unusable before this release; the
flags that changed are listed below because scripts written against 1.0.x
`--help` output will need updating.

As of this release the CLI is a versioned surface — see
[Versioning](README.md#versioning). Flag removals now require a major bump.

### Changed

- **CLI — breaking.** No aliases are retained for the old spellings.

  | Was | Now | Notes |
  |-----|-----|-------|
  | `--thread` | `--thread-id` | all `conversation` subcommands |
  | `--last` | `--limit` | `conversation history` |
  | `--glob` | `--pattern` | `knowledge ingest-dir` |
  | `-c` (for `--config`) | `-C` | see below |
  | `--ttl 3600` | `--ttl 1h` | now a duration, not integer seconds |

  **`-c` needs care.** It no longer means `--config`, and the two failure
  modes differ. `cortex -c cfg.yaml serve` now errors with
  `unknown shorthand flag: 'c'`. But on commands that define their own `-c`,
  such as `knowledge ingest`, it is *silently* reinterpreted — there
  `-c cfg.yaml` now binds to `--collection`. Audit scripts for `-c` rather
  than relying on a failure to surface it.

- `--namespace` now defaults to `"default"` on data-plane commands instead of
  being required. It stays required on `export` and `namespace delete`, where
  operating on the wrong namespace is destructive.

- Search commands take their query positionally
  (`cortex knowledge search "query"`), as documented. The same applies to
  `context` keys and `entity` IDs.

### Added

- `knowledge ingest` accepts the documented `--chunk-strategy`,
  `--chunk-max-tokens`, and `--chunk-overlap` flags.
- `knowledge ingest` and `ingest-dir` create the target collection on first
  use, so the documented examples work without a separate create step.
- `knowledge ingest` accepts the file as a positional argument, and
  `create-collection` accepts the name positionally.
- A test that locks every command invocation appearing in README and
  `examples/cli-basics` (`internal/cmd/documented_commands_test.go`), so a
  renamed flag or newly required argument fails CI rather than shipping.

### Fixed

- **SQLite** — `New` now runs migrations, mirroring the pgvector backend.
  Every caller that did not explicitly call `Migrate` — `serve`, all the
  data-plane CLI commands, the examples — failed on a fresh database with
  `no such table`. The runner is versioned and idempotent.
- **CLI** — `--config`'s `-c` shorthand collided with local `-c` flags on
  several subcommands (`knowledge ingest`/`search`/`stats` use
  `--collection`, `conversation append` uses `--content`). pflag panics on a
  shorthand collision at parse time, so those commands crashed on any
  invocation. Resolved by moving `--config` to `-C`.

### Documentation

- Corrected remaining divergences between the docs and the CLI: search mode
  is `--mode text` (not `fts`), `namespace delete` takes
  `--namespace <ns> --confirm`, and `context cleanup` takes
  `--expired`/`--run-id`.

## [1.0.1] - 2026-08-16

### Fixed

- **CLI**
  - `cortex --version` now works. The flag did not exist — the root command
    never set a version — so the command documented under "Verify
    Installation" failed with `unknown flag: --version`.
  - Release binaries now carry their build information. The Makefile and
    release workflow passed `-X main.version=...`, `-X main.commit=...`, and
    `-X main.date=...` to a `package main` that declared none of those
    variables; the Go linker ignores an `-X` flag naming a symbol that does
    not exist, without warning, so every binary through v1.0.0 shipped with
    no version at all.

  Release builds now report `cortex version 1.0.1 (<commit>, <date>)`.
  A binary built without ldflags reports `cortex version dev` rather than
  leaving the flag unavailable.

## [1.0.0] - 2026-08-16

First stable release. Cortex's storage, MCP wire contract, and public Go API
are now covered by semantic versioning: breaking changes require a major bump.

This release is the stability hardening described in the
[Cortex 1.0 Stability Roadmap](https://github.com/petal-labs/cortex/issues/35) —
all 32 items across its five phases.

### Upgrading from 0.3.x

- **MCP clients** must send string-valued `metadata`, filters, and attributes,
  and correctly-typed tool arguments; both are now rejected rather than
  coerced. Responses gained a `schema_version` field (currently `1`) and no
  longer contain embedding vectors, `null` collections, or zero timestamps.
- **Go API consumers** should note the `pkg/types` changes below.
- **pgvector deployments** created before this release keep their existing
  `vector(1536)` columns. Cortex now refuses to start if `embedding.dimensions`
  disagrees with them — set it to `1536` (the default) unless you re-embed.
- **No manual migration step.** Both backends apply pending versioned
  migrations at startup.

### Added

- **Storage**
  - Versioned migration runner for the pgvector backend, mirroring SQLite's.
    Both backends record the applied version in `cortex_metadata.schema_version`
    and apply only pending migrations at startup.
  - Dual-backend conformance suite (`internal/storage/conformance/`) holding
    SQLite and pgvector to the same behavioral contract. The pgvector run uses
    testcontainers and is skipped in short mode, on Windows, and without a
    healthy Docker provider. New `make test-integration` target and a CI job.
  - `storage.pool_max_conns` and `storage.pool_min_conns` config options for
    pgvector. `0` (the default) defers to the `pool_max_conns`/`pool_min_conns`
    query params on `database_url`, then to pgx's defaults; explicit values
    take precedence over the URL.

- **Configuration**
  - Startup validation across every config section. Bad values fail fast with
    an error listing each violation by config key, instead of failing late and
    opaquely at runtime (a non-positive `retention.gc_interval`, for example,
    used to panic the background ticker).
  - `summarization.timeout` (default `120s`) and `entity.extraction_timeout`
    (default `120s`) explicitly bound LLM calls. `0` disables.
  - `server.shutdown_timeout` (default `30s`) bounds the graceful drain of
    background workers.

- **MCP Server**
  - Output DTOs with a declared, versioned response contract
    (`internal/server/dto/`). Every top-level response now carries
    `schema_version`, letting clients feature-detect contract changes. The wire
    shapes are frozen by golden-file tests, decoupling them from internal
    engine types.

- **Observability**
  - `/ready` readiness probe on the metrics port, returning `200 ready` when
    the storage backend is reachable within 2s and `503 not ready: <reason>`
    otherwise. `/health` remains liveness-only so a database outage pulls the
    instance from rotation rather than getting it restarted.

- **Entity Memory**
  - `event` and `other` entity types.
  - `next_retry_at` on `ExtractionQueueItem`, recording the earliest eligible
    dequeue time after a failed attempt.

### Changed

- **BREAKING (Go API)** — `pkg/types`:
  - `ExtractedEntity` dropped `Relationships` and gained `Confidence`;
    `ExtractedRelationship` gained `SourceName` and always emits `Confidence`.
    The duplicate extractor-local and package-level definitions are now a
    single canonical pair.
  - `CollectionStats.LastIngest` is now `*time.Time` and is omitted from JSON
    for collections that have never been ingested into, rather than rendering
    as the misleading `0001-01-01T00:00:00Z`.

- **BREAKING (MCP wire contract)**:
  - Metadata, filter, and attribute values must be strings. Non-string values
    are rejected with an actionable message naming the key and its type;
    Cortex previously stringified them silently, which corrupted typed values
    and made filter matching ambiguous.
  - Tool arguments that are present but of the wrong type are rejected rather
    than silently falling back to the default, so a client bug no longer looks
    like a working call.
  - Entity type values outside the seven supported names are rejected instead
    of being silently remapped to an unrelated type.
  - Embedding vectors are stripped from search results — a large payload MCP
    clients could not use.
  - Empty slices and maps serialize as `[]` and `{}` instead of `null`.
  - Zero-value timestamps are omitted from responses.

- **pgvector**
  - Schema DDL is generated from `embedding.dimensions` instead of a hardcoded
    `vector(1536)`, so non-1536 embedding models work without patching DDL.
    A configured dimension that disagrees with existing columns is a startup
    error naming the required `ALTER TABLE`, not a silent write of ragged data.
  - Vector types are registered in the `pgxpool` `AfterConnect` hook, which was
    previously a no-op.
  - Unique-constraint violations are detected via `pgconn.PgError` codes rather
    than substring matching on the error text.

- **Embeddings**
  - Transient provider failures (5xx, network, 429) are retried with
    exponential backoff and jitter; permanent failures (bad key, malformed
    request, undecodable response) are not retried.
  - Provider errors are classified through Iris's typed error taxonomy
    (`core.ProviderError` + sentinels) rather than message text, and logged
    with status, provider error code, and request ID for escalation.
  - Oversized `EmbedBatch` inputs are split to fit the provider's limit instead
    of failing the whole call.
  - Response vector lengths are validated against `embedding.dimensions`,
    catching a mismatched model before bad data reaches storage.
  - `EmbedBatch` maps vectors by their response `Index` rather than by position
    in the response array.

- **Entity Extraction**
  - The extraction JSON schema uses an object root with `Strict` enabled;
    an array root is not valid for structured output.
  - Fallback JSON parsing (for providers without structured output) is
    hardened against fenced blocks and surrounding prose.
  - The queue enforces `extraction_backoff` between attempts and routes
    non-retryable errors to the dead-letter policy instead of spinning on them.

- **MCP Server**
  - Errors are classified before reaching the client: known client-facing
    sentinels return their own message, everything else returns
    `internal server error` and is logged server-side, so SQL fragments and
    connection URLs no longer leak to clients.
  - Large integers keep their precision through the JSON argument path;
    a value that float64 decoding would corrupt is rejected.

### Fixed

- **Knowledge**
  - Ingestion no longer swallows embedding failures. A document whose
    embeddings fail now returns an error instead of being persisted without
    vectors — permanently invisible to semantic search.

- **Reliability**
  - Panic recovery for MCP tool and resource handlers, the entity extraction
    queue, and the garbage collector. A panic in one batch is logged with its
    stack and the worker continues, instead of taking down the process.
  - Bounded, coordinated shutdown: on signal, the queue processor and garbage
    collector are drained within `server.shutdown_timeout` before exit.

- **Embeddings**
  - An all-empty batch with `embedding.dimensions` unset is rejected instead of
    synthesizing zero-width vectors that produced ragged output downstream.

### Removed

- Dead embedding contract types that no code path referenced.

## [0.3.0] - 2026-07-31

### Changed

- **Dependencies**
  - Upgraded Iris SDK from v0.14.0 to v0.15.0

### Added

- **Embeddings**
  - New `embedding.timeout` config option (default 120s) that bounds embedding
    provider calls. Cortex calls the embedding provider directly rather than
    through `core.Client`, so iris's own execution timeout does not apply; this
    imposes an equivalent deadline. The timeout is only applied when the caller
    supplied no deadline of its own, and `timeout <= 0` disables it (unbounded).

### Fixed

- **Embeddings**
  - A hung embedding provider call now fails fast with a legible
    `context.DeadlineExceeded` (detectable via `errors.Is`) instead of blocking
    until the caller cancels and surfacing as an opaque `context.Canceled`.

## [0.2.1] - 2026-07-29

### Changed

- **Dependencies**
  - Upgraded Iris SDK from v0.13.0 to v0.14.0, which includes a bugfix for the
    Ollama integration

## [0.2.0] - 2026-03-08

### Changed

- **Dependencies**
  - Upgraded Iris SDK from v0.12.0 to v0.13.0

### Improved

- **Entity Extraction**
  - Now uses structured output (`ResponseJSONSchema`) for reliable JSON parsing
  - Added strict JSON Schema validation with required fields and enum constraints
  - Simplified extraction prompt (schema handles format enforcement)
  - Kept fallback parsing for providers without structured output support

## [0.1.1] - 2026-03-06

### Added

- **Examples**
  - `cli-basics` - Command-line usage patterns for common tasks
  - `claude-desktop` - Integration with Claude Desktop via MCP
  - `go-client` - Using Cortex programmatically as a Go library
  - `petalflow-research-agent` - Multi-agent research workflow using PetalFlow Agent/Task schema
  - `petalflow-graph` - Building workflow graphs programmatically with PetalFlow
  - `petalflow-agent-tools` - Using Cortex as a tool provider for PetalFlow agents

- **Community**
  - GitHub issue templates (bug report, feature request)
  - Pull request template
  - CONTRIBUTING guide
  - SECURITY policy
  - CODE_OF_CONDUCT

### Fixed

- Windows CI builds now properly configure SQLite headers
- Line ending handling across platforms with git autocrlf

## [0.1.0] - 2026-03-05

Initial release of Cortex - a memory and knowledge service for AI agents.

### Added

- **Core Memory Engines**
  - Conversation memory with automatic summarization
  - Knowledge store with semantic chunking and hybrid search
  - Workflow context engine with merge strategies
  - Entity memory with knowledge graph support

- **Storage Backends**
  - SQLite with sqlite-vec for local vector similarity search
  - PostgreSQL with pgvector for production deployments

- **Embedding Support**
  - Iris embedding integration with LRU caching
  - Configurable embedding dimensions and batch processing

- **MCP Server**
  - Full MCP protocol implementation
  - Memory tools for conversation, knowledge, context, and entity operations
  - CLI serve command for easy deployment

- **TUI Dashboard**
  - Interactive terminal interface for memory exploration
  - Conversation, knowledge, context, and entity views
  - Real-time statistics and navigation

- **Production Features**
  - Prometheus metrics endpoint
  - Structured logging with zap
  - Garbage collection for memory management
  - Backup and export functionality

- **CI/CD**
  - GitHub Actions for testing on Linux, macOS, and Windows
  - Automated releases with cross-platform binaries
  - Code coverage with Codecov integration
  - Security scanning with gosec
  - Linting with golangci-lint

### Platforms

- Linux (amd64, arm64)
- macOS (amd64, arm64)
- Windows (amd64)

[1.1.0]: https://github.com/petal-labs/cortex/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/petal-labs/cortex/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/petal-labs/cortex/compare/v0.3.0...v1.0.0
[0.3.0]: https://github.com/petal-labs/cortex/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/petal-labs/cortex/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/petal-labs/cortex/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/petal-labs/cortex/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/petal-labs/cortex/releases/tag/v0.1.0
