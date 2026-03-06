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

### Storage Backend

Cortex supports two storage backends:

```go
import "github.com/petal-labs/cortex/internal/storage/sqlite"

// SQLite (local, zero-config)
store, err := sqlite.New(sqlite.Config{
    Path: "./cortex.db",
})
```

### Embedding Provider

For semantic search, you need an embedding provider:

```go
import "github.com/petal-labs/cortex/internal/embedding"

// Using Iris embedding service
provider := embedding.NewIrisProvider(embedding.IrisConfig{
    Endpoint:   "http://localhost:8000",
    Dimensions: 1536,
})
```

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
