# Cortex Examples

This directory contains examples demonstrating how to use Cortex in various scenarios.

## Examples

| Example | Description |
|---------|-------------|
| [cli-basics](./cli-basics/) | Command-line usage patterns for common tasks |
| [claude-desktop](./claude-desktop/) | Integration with Claude Desktop via MCP |
| [go-client](./go-client/) | Using Cortex programmatically as a Go library |
| [petalflow-research-agent](./petalflow-research-agent/) | Multi-agent research workflow using PetalFlow Agent/Task schema |
| [petalflow-graph](./petalflow-graph/) | Building workflow graphs programmatically with PetalFlow |
| [petalflow-agent-tools](./petalflow-agent-tools/) | Using Cortex as a tool provider for PetalFlow agents |

## Quick Start

Before running these examples, ensure Cortex is installed:

```bash
# Download from releases
curl -LO https://github.com/petal-labs/cortex/releases/latest/download/cortex_0.1.0_$(uname -s | tr '[:upper:]' '[:lower:]')_$(uname -m).tar.gz
tar -xzf cortex_*.tar.gz
sudo mv cortex_*/cortex /usr/local/bin/

# Verify installation
cortex --version
```

## Use Cases

### Personal Knowledge Base

Store and search your notes, documents, and research:

```bash
cortex knowledge ingest --collection notes --title "Meeting Notes" --file notes.md
cortex knowledge search "action items from last meeting"
```

### Agent Memory

Give AI agents persistent memory across sessions:

```bash
# Start MCP server for your agent
cortex serve --namespace my-agent
```

### Development Context

Track project state and context across coding sessions:

```bash
cortex context set "current_task" "implementing user auth"
cortex context set "project_status" '{"phase": "development", "sprint": 3}'
```

### AI Agent Workflows with PetalFlow

Build sophisticated multi-agent workflows using [PetalFlow](https://github.com/petal-labs/petalflow):

```go
// Use the Agent/Task schema for declarative workflows
wf, _ := agent.LoadFromFile("research_workflow.json")
graphDef, _ := agent.Compile(wf)
graph, _ := hydrate.Hydrate(graphDef, opts)

// Or build graphs programmatically
builder := petalflow.NewGraphBuilder("qa-workflow")
builder.AddNode(searchNode)
builder.Edge(prepareNode)
builder.Edge(generateNode)
graph, _ := builder.Build()
```

See the [petalflow-research-agent](./petalflow-research-agent/) and [petalflow-graph](./petalflow-graph/) examples for complete implementations.
