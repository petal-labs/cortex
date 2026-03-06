# PetalFlow Agent with Cortex Tools

This example demonstrates how to use Cortex as a **tool provider** for PetalFlow agents. The agent uses Cortex tools for knowledge retrieval, context storage, and persistent memory.

## Overview

The example implements a customer support agent with three Cortex-backed tools:

| Tool | Description |
|------|-------------|
| `cortex_knowledge_search` | Search the knowledge base for relevant documentation |
| `cortex_context_get` | Retrieve stored context (e.g., customer history) |
| `cortex_context_set` | Store context for future interactions |

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                   PetalFlow Agent Workflow                   │
│                                                              │
│  ┌────────────────────┐      ┌────────────────────┐        │
│  │   Support Agent    │─────▶│    Summarizer      │        │
│  │   (handle_query)   │      │   (save_context)   │        │
│  └─────────┬──────────┘      └─────────┬──────────┘        │
│            │                           │                    │
│            ▼                           ▼                    │
│  ┌─────────────────────────────────────────────────────┐   │
│  │                   Cortex Tools                       │   │
│  │  ┌──────────────┐ ┌─────────────┐ ┌─────────────┐   │   │
│  │  │  knowledge   │ │  context    │ │  context    │   │   │
│  │  │   _search    │ │   _get      │ │   _set      │   │   │
│  │  └──────┬───────┘ └──────┬──────┘ └──────┬──────┘   │   │
│  └─────────┼────────────────┼───────────────┼──────────┘   │
│            │                │               │               │
└────────────┼────────────────┼───────────────┼───────────────┘
             ▼                ▼               ▼
┌─────────────────────────────────────────────────────────────┐
│                       Cortex Engines                         │
│  ┌─────────────────────┐    ┌─────────────────────┐        │
│  │   Knowledge Engine  │    │   Context Engine    │        │
│  │   (search docs)     │    │   (key-value store) │        │
│  └─────────────────────┘    └─────────────────────┘        │
└─────────────────────────────────────────────────────────────┘
```

## Prerequisites

1. **Cortex** installed and configured
2. **Ollama** running locally with llama3.2:
   ```bash
   ollama pull llama3.2
   ```
3. **Documents ingested** into Cortex:
   ```bash
   cortex knowledge ingest-dir \
     --collection docs \
     --dir /path/to/docs \
     --pattern "*.md"
   ```

## Running the Example

```bash
cd examples/petalflow-agent-tools

# Basic usage
go run main.go "How do I configure the embedding service?"

# With customer ID for context tracking
go run main.go "What storage backends are supported?" customer-123
```

## How It Works

### 1. Agent Receives Query

The support agent receives a customer query and uses tools to:
- Search the knowledge base for relevant documentation
- Check for previous context about this customer
- Formulate a helpful response

### 2. Tools Are Called

The LLM decides which tools to call based on the query:

```json
{
  "tool": "cortex_knowledge_search",
  "args": { "query": "embedding service configuration", "limit": 5 }
}
```

### 3. Context Is Persisted

After responding, the summarizer agent saves a summary:

```json
{
  "tool": "cortex_context_set",
  "args": {
    "key": "customer_customer-123",
    "value": "{\"topic\": \"embedding config\", \"resolved\": true}"
  }
}
```

### 4. Future Queries Use Context

On subsequent queries, the agent retrieves previous context:

```json
{
  "tool": "cortex_context_get",
  "args": { "key": "customer_customer-123" }
}
```

## Implementing Custom Tools

Create a tool by implementing the `PetalTool` interface:

```go
type MyTool struct{}

func (t *MyTool) Name() string { return "my_tool" }

func (t *MyTool) Description() string {
    return "What this tool does"
}

func (t *MyTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "param1": map[string]any{
                "type": "string",
                "description": "Parameter description",
            },
        },
        "required": []string{"param1"},
    }
}

func (t *MyTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
    // Tool implementation
    return map[string]any{"result": "value"}, nil
}
```

Register the tool:

```go
registry.Global().RegisterTool("my_tool", registry.NodeTypeDef{
    ID:       "my_tool",
    Name:     "My Tool",
    IsTool:   true,
    ToolMode: "function_call",
})

toolRegistry := petalflow.NewToolRegistry()
toolRegistry.Register(&MyTool{})
```

## Agent/Task Schema

The `support_agent.json` defines the workflow:

```json
{
  "agents": {
    "support": {
      "role": "Customer Support Specialist",
      "tools": ["cortex_knowledge_search", "cortex_context_get", "cortex_context_set"]
    }
  },
  "tasks": {
    "handle_query": {
      "description": "Handle the customer query using available tools...",
      "agent": "support"
    }
  }
}
```

## Output Example

```
============================================================
CORTEX SUPPORT AGENT
============================================================
Customer ID: demo-customer
Query: How do I configure the embedding service?
------------------------------------------------------------

[14:30:15] Starting: handle_query__support
[14:30:28] Completed: handle_query__support
[14:30:28] Starting: save_context__summarizer
[14:30:35] Completed: save_context__summarizer

============================================================
RESPONSE
============================================================
To configure the embedding service in Cortex, you have several options:

1. **Using Iris (Recommended)**
   Set the IRIS_ENDPOINT environment variable:
   ```bash
   export IRIS_ENDPOINT=http://localhost:8000
   ```

2. **In config.yaml**
   ```yaml
   embedding:
     endpoint: http://localhost:8000
     dimensions: 1536
   ```
...

------------------------------------------------------------
Interaction Summary: Saved to Cortex
```

## See Also

- [petalflow-research-agent](../petalflow-research-agent/) - Agent/Task schema example
- [petalflow-graph](../petalflow-graph/) - Programmatic graph example
- [Cortex CLI Basics](../cli-basics/) - CLI usage patterns
