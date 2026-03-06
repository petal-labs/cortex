# Research Agent with PetalFlow

This example demonstrates how to build a multi-agent research workflow using [PetalFlow](https://github.com/petal-labs/petalflow)'s Agent/Task schema with Cortex as the knowledge backend.

## Overview

The workflow uses three specialized agents working in sequence:

1. **Researcher** - Searches the Cortex knowledge store for relevant information
2. **Analyst** - Analyzes findings and identifies key insights
3. **Writer** - Generates a structured research report

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     Agent/Task Schema                        │
│                  (research_workflow.json)                    │
└─────────────────────┬───────────────────────────────────────┘
                      │ compile
                      ▼
┌─────────────────────────────────────────────────────────────┐
│                    PetalFlow Graph                           │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐              │
│  │ Research │───▶│ Analyze  │───▶│  Report  │              │
│  │   Task   │    │   Task   │    │   Task   │              │
│  └────┬─────┘    └──────────┘    └──────────┘              │
│       │                                                      │
│       ▼                                                      │
│  ┌──────────────────┐                                       │
│  │ cortex_search    │                                       │
│  │     Tool         │                                       │
│  └────────┬─────────┘                                       │
│           │                                                  │
└───────────┼─────────────────────────────────────────────────┘
            ▼
┌─────────────────────────────────────────────────────────────┐
│                    Cortex Knowledge Store                    │
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

First, populate the knowledge store with documents:

```bash
# Ingest your documentation
cortex knowledge ingest-dir \
  --collection docs \
  --dir /path/to/your/docs \
  --pattern "*.md"

# Or ingest individual files
cortex knowledge ingest \
  --collection research \
  --title "Important Paper" \
  --file paper.pdf
```

### 2. Run the Example

```bash
cd examples/petalflow-research-agent
go run main.go "What are the main features of this project?"
```

## The Agent/Task Schema

The `research_workflow.json` file defines the workflow using PetalFlow's Agent/Task format:

```json
{
  "agents": {
    "researcher": {
      "role": "Research Analyst",
      "goal": "Search the knowledge base...",
      "tools": ["cortex_search"]
    }
  },
  "tasks": {
    "research": {
      "description": "Search the Cortex knowledge base...",
      "agent": "researcher",
      "expected_output": "A comprehensive collection..."
    }
  },
  "execution": {
    "strategy": "sequential",
    "task_order": ["research", "analyze", "report"]
  }
}
```

## Execution Strategies

The Agent/Task schema supports multiple execution strategies:

- **sequential** - Tasks run one after another
- **parallel** - Tasks run concurrently
- **hierarchical** - A manager agent coordinates workers
- **custom** - DAG-based execution with dependencies

## Customization

### Using Different LLM Providers

Replace the Ollama provider with any Iris-compatible provider:

```go
// Anthropic
provider := anthropic.New(anthropic.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))

// OpenAI
provider := openai.New(openai.WithAPIKey(os.Getenv("OPENAI_API_KEY")))
```

### Adding More Tools

Register additional tools for the agents:

```go
reg.RegisterTool("web_search", registry.NodeTypeDef{
    ID:       "web_search",
    Name:     "Web Search",
    IsTool:   true,
    ToolMode: "function_call",
})
```

### Modifying the Workflow

Edit `research_workflow.json` to:
- Add more agents with different specializations
- Create parallel task execution
- Add human review steps
- Include conditional branching

## Output

The workflow produces a structured research report:

```
Research Query: What are the main features of Cortex?
============================================================

[10:30:45] Starting node: research__researcher
[10:30:52] Completed node: research__researcher
[10:30:52] Starting node: analyze__analyst
[10:31:05] Completed node: analyze__analyst
[10:31:05] Starting node: report__writer
[10:31:20] Completed node: report__writer

============================================================
RESEARCH REPORT
============================================================

## Executive Summary
...

## Key Findings
...

## Recommendations
...
```
