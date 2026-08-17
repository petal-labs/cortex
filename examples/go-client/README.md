# Using Cortex as a Go Library

This example demonstrates how to use Cortex programmatically in your own Go applications.

## Overview

While Cortex is primarily designed as an MCP server, you can also use its engines directly in Go applications for:

- Building custom memory-enabled applications
- Integrating with existing Go services
- Creating specialized AI agent backends

## Installation

```bash
go get github.com/petal-labs/cortex
```

## Example: Simple Knowledge Store

See [main.go](./main.go) for a complete example that demonstrates:

1. Initializing the SQLite storage backend
2. Creating a knowledge engine
3. Ingesting documents with chunking
4. Searching with hybrid search

## Running the Example

```bash
cd examples/go-client
go run main.go
```

## Key Concepts

### Configuration

Both backends and the embedding client are constructed from a `*config.Config`:

```go
import "github.com/petal-labs/cortex/internal/config"

// Defaults, then override what you need
cfg := config.DefaultConfig()
cfg.Storage.DataDir = "./data"

// Or load from disk. Load validates the result and returns an error
// listing every invalid value by config key.
cfg, err := config.Load("~/.cortex/config.yaml")
```

Building a `Config` by hand skips validation — call `cfg.Validate()` yourself
if the values came from somewhere untrusted.

### Storage Backend

Cortex supports two storage backends. Both apply versioned migrations through
`Migrate`, which records the applied version and runs only what is pending:

```go
import "github.com/petal-labs/cortex/internal/storage/sqlite"

// SQLite (local, zero-config) — uses cfg.Storage.DataDir
store, err := sqlite.New(cfg)
if err != nil {
    return err
}
defer store.Close()

if err := store.Migrate(ctx); err != nil {
    return err
}
```

For pgvector, set `cfg.Storage.Backend = "pgvector"` and
`cfg.Storage.DatabaseURL`. The schema's `vector(N)` columns are generated from
`cfg.Embedding.Dimensions`; pointing a differently-dimensioned config at an
existing database is a startup error rather than a silent bad write.

### Embedding Provider

For semantic search, you need an embedding provider. The Iris client reads its
provider, model, dimensions, and timeout from the config — but the API key
comes from the environment (`OPENAI_API_KEY` here; see
[Provider API Keys](../../README.md#provider-api-keys)). `NewIrisClient`
returns an error naming the missing variable, so this fails loudly rather than
at first search. Only `openai`, `voyageai`, and `ollama` can embed:

```go
import "github.com/petal-labs/cortex/internal/embedding"

cfg.Embedding.Provider = "openai"
cfg.Embedding.Model = "text-embedding-3-small"
cfg.Embedding.Dimensions = 1536
cfg.Embedding.Timeout = 120 * time.Second // 0 disables

provider, err := embedding.NewIrisClient(cfg)
if err != nil {
    return err
}
defer provider.Close()

// Pass it to an engine instead of nil to enable semantic search
engine, err := knowledge.NewEngine(store, provider, &cfg.Knowledge)
```

Transient provider failures are retried with backoff; permanent ones are not.
Returned vectors are checked against `cfg.Embedding.Dimensions`, so a
mismatched model fails at the call rather than corrupting the index.

### Engines

Each memory primitive has its own engine:

```go
import (
    "github.com/petal-labs/cortex/internal/knowledge"
    "github.com/petal-labs/cortex/internal/conversation"
    "github.com/petal-labs/cortex/internal/context"
    "github.com/petal-labs/cortex/internal/entity"
)
```

## Architecture

```
Your Application
       │
       ▼
┌──────────────────┐
│  Cortex Engines  │
│  - Knowledge     │
│  - Conversation  │
│  - Context       │
│  - Entity        │
└────────┬─────────┘
         │
         ▼
┌──────────────────┐
│ Storage Backend  │
│ (SQLite/pgvector)│
└──────────────────┘
```

## Notes

- The `internal/` packages are not guaranteed to have a stable API
- For production use, consider using the MCP interface instead
- CGO is required for SQLite support
