# PetalFlow Graph Workflow with Cortex

This example demonstrates how to build workflow graphs programmatically using [PetalFlow](https://github.com/petal-labs/petalflow)'s graph builder API with Cortex as the knowledge backend.

## Overview

The workflow implements a knowledge-enriched Q&A pattern:

1. **Search** - Query Cortex for relevant context
2. **Prepare** - Format context for the LLM
3. **Generate** - Produce an answer using retrieved context

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    PetalFlow Graph                           │
│                                                              │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐  │
│  │   FuncNode   │───▶│TransformNode │───▶│   LLMNode    │  │
│  │   "search"   │    │  "prepare"   │    │  "generate"  │  │
│  └──────┬───────┘    └──────────────┘    └──────────────┘  │
│         │                                                    │
│         ▼                                                    │
│  ┌──────────────────────────────────────────────────────┐   │
│  │              CortexKnowledgeStore                     │   │
│  │         (wraps Cortex knowledge.Engine)               │   │
│  └───────────────────────┬──────────────────────────────┘   │
│                          │                                   │
└──────────────────────────┼───────────────────────────────────┘
                           ▼
┌─────────────────────────────────────────────────────────────┐
│                    Cortex Storage                            │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐                  │
│  │Collection│  │Collection│  │Collection│                  │
│  │   docs   │  │ research │  │  notes   │                  │
│  └──────────┘  └──────────┘  └──────────┘                  │
└─────────────────────────────────────────────────────────────┘
```

## Prerequisites

1. **Cortex** installed and configured
2. **Ollama** running locally with llama3.2:
   ```bash
   ollama pull llama3.2
   ```
3. **PetalFlow** and **Iris** dependencies

## Setup

### 1. Ingest Documents into Cortex

```bash
# Ingest your documentation
cortex knowledge ingest-dir \
  --collection docs \
  --dir /path/to/your/docs \
  --pattern "*.md"
```

### 2. Run the Example

```bash
cd examples/petalflow-graph
go run main.go "What are the main features?"
```

## Graph Building Approaches

This example demonstrates the **fluent builder API**:

```go
builder := petalflow.NewGraphBuilder("knowledge-qa")
builder.AddNode(searchNode)      // Entry node
builder.Edge(prepareNode)        // Connect to prepare
builder.Edge(generateNode)       // Connect to generate
graph, err := builder.Build()
```

### Alternative: Direct Graph Construction

```go
graph := petalflow.NewGraph("knowledge-qa")
graph.AddNode(searchNode)
graph.AddNode(prepareNode)
graph.AddNode(generateNode)
graph.AddEdge("search", "prepare")
graph.AddEdge("prepare", "generate")
graph.SetEntry("search")
```

### Alternative: Linear Pipeline Helper

```go
graph, err := petalflow.BuildGraph("knowledge-qa",
    searchNode,
    prepareNode,
    generateNode,
)
```

## Node Types Used

### FuncNode
Custom Go functions for business logic:
```go
searchNode := petalflow.NewFuncNode("search", func(ctx context.Context, env *petalflow.Envelope) (*petalflow.Envelope, error) {
    // Custom search logic
    return env, nil
})
```

### TransformNode
Template-based data transformation:
```go
prepareNode := petalflow.NewTransformNode("prepare", petalflow.TransformNodeConfig{
    Transform: petalflow.TransformTemplate,
    Template:  "{{.context}}",
    OutputVar: "prompt",
})
```

### LLMNode
LLM completion with structured configuration:
```go
generateNode := petalflow.NewLLMNode("generate", client, petalflow.LLMNodeConfig{
    Model:          "llama3.2",
    System:         "You are a helpful assistant.",
    PromptTemplate: "{{.prompt}}",
    OutputKey:      "answer",
})
```

## Extending the Graph

### Add Parallel Branches (Fan-Out)

```go
builder := petalflow.NewGraphBuilder("parallel-qa")
builder.AddNode(searchNode)
builder.FanOut(
    summaryNode,    // Branch 1: Generate summary
    keyPointsNode,  // Branch 2: Extract key points
    questionsNode,  // Branch 3: Generate follow-up questions
)
builder.Merge(mergeNode)
builder.Edge(finalNode)
```

### Add Conditional Routing

```go
router := petalflow.NewRuleRouter("route", petalflow.RuleRouterConfig{
    Rules: []petalflow.RouteRule{
        {
            Conditions: []petalflow.RouteCondition{
                {VarPath: "has_context", Op: petalflow.OpEquals, Value: true},
            },
            Target: "contextual_answer",
        },
    },
    DefaultTarget: "general_answer",
})
```

### Add Human Review

```go
reviewNode := petalflow.NewHumanNode("review", petalflow.HumanNodeConfig{
    RequestType: petalflow.HumanRequestApproval,
    Prompt:      "Please review this answer before publishing.",
    Handler:     humanHandler,
    Timeout:     5 * time.Minute,
})
```

## Output

```
Question: What is Cortex and what are its main features?
============================================================

[10:30:45] Starting: search
  Found 5 relevant chunks
[10:30:46] Completed: search
[10:30:46] Starting: prepare
[10:30:46] Completed: prepare
[10:30:46] Starting: generate
[10:31:02] Completed: generate

============================================================
ANSWER
============================================================
Cortex is a memory and knowledge service for AI agents that provides...

Sources:
- README.md
- Architecture Documentation
- API Reference
```

## See Also

- [petalflow-research-agent](../petalflow-research-agent/) - Agent/Task schema example
- [PetalFlow Documentation](https://github.com/petal-labs/petalflow)
