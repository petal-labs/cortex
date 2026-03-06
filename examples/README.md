# Cortex Examples

This directory contains examples demonstrating how to use Cortex in various scenarios.

## Examples

| Example | Description |
|---------|-------------|
| [cli-basics](./cli-basics/) | Command-line usage patterns for common tasks |
| [claude-desktop](./claude-desktop/) | Integration with Claude Desktop via MCP |
| [go-client](./go-client/) | Using Cortex programmatically as a Go library |

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
