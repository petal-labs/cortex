# Using Cortex with Claude Desktop

This example shows how to integrate Cortex with [Claude Desktop](https://claude.ai/download) using the Model Context Protocol (MCP).

## Overview

Claude Desktop supports MCP servers, allowing Claude to use Cortex's memory capabilities directly in your conversations. This gives Claude:

- **Persistent conversation memory** across chat sessions
- **Knowledge retrieval** from your documents
- **Workflow context** that persists between tasks
- **Entity memory** for tracking people, projects, and concepts

## Setup

### 1. Install Cortex

```bash
# macOS (Apple Silicon)
curl -LO https://github.com/petal-labs/cortex/releases/latest/download/cortex_0.1.0_darwin_arm64.tar.gz
tar -xzf cortex_0.1.0_darwin_arm64.tar.gz
sudo mv cortex_0.1.0_darwin_arm64/cortex /usr/local/bin/

# macOS (Intel)
curl -LO https://github.com/petal-labs/cortex/releases/latest/download/cortex_0.1.0_darwin_amd64.tar.gz
tar -xzf cortex_0.1.0_darwin_amd64.tar.gz
sudo mv cortex_0.1.0_darwin_amd64/cortex /usr/local/bin/
```

### 2. Configure Claude Desktop

Edit your Claude Desktop configuration file:

**macOS**: `~/Library/Application Support/Claude/claude_desktop_config.json`

**Windows**: `%APPDATA%\Claude\claude_desktop_config.json`

Add the Cortex MCP server:

```json
{
  "mcpServers": {
    "cortex": {
      "command": "cortex",
      "args": ["serve", "--namespace", "claude-desktop"]
    }
  }
}
```

### 3. Restart Claude Desktop

Quit and reopen Claude Desktop. You should see Cortex tools available in the tools menu.

## Configuration Options

### Basic Configuration

```json
{
  "mcpServers": {
    "cortex": {
      "command": "cortex",
      "args": ["serve"]
    }
  }
}
```

### With Custom Data Directory

```json
{
  "mcpServers": {
    "cortex": {
      "command": "cortex",
      "args": ["serve", "--namespace", "claude"],
      "env": {
        "CORTEX_DATA_DIR": "/path/to/your/cortex/data"
      }
    }
  }
}
```

### With Iris Embedding Service

If you're running an [Iris](https://github.com/petal-labs/iris) embedding service:

```json
{
  "mcpServers": {
    "cortex": {
      "command": "cortex",
      "args": ["serve", "--namespace", "claude"],
      "env": {
        "IRIS_ENDPOINT": "http://localhost:8000"
      }
    }
  }
}
```

## Available Tools

Once configured, Claude will have access to these tools:

### Conversation Memory
- `conversation_append` - Save messages to memory
- `conversation_history` - Retrieve past conversations
- `conversation_search` - Search conversation history
- `conversation_summarize` - Summarize long conversations

### Knowledge Store
- `knowledge_ingest` - Add documents to knowledge base
- `knowledge_search` - Search your documents
- `knowledge_collections` - Manage document collections

### Workflow Context
- `context_get` / `context_set` - Store and retrieve key-value data
- `context_list` - List stored context
- `context_merge` - Merge context with strategies

### Entity Memory
- `entity_query` - Look up entities by name
- `entity_search` - Semantic search across entities
- `entity_relationships` - Get entity connections

## Example Prompts

Once Cortex is connected, try these prompts:

**Build a knowledge base:**
> "Ingest this document into my 'research' collection and remember the key points."

**Search your knowledge:**
> "Search my knowledge base for information about machine learning optimization."

**Track project context:**
> "Remember that I'm working on the authentication feature for Project X."

**Recall past conversations:**
> "What did we discuss last week about the database schema?"

## Pre-loading Knowledge

You can pre-load documents before starting Claude Desktop:

```bash
# Ingest your project documentation
cortex knowledge ingest-dir \
  --collection "project-docs" \
  --dir ~/projects/myapp/docs \
  --pattern "*.md" \
  --namespace claude-desktop

# Add important reference materials
cortex knowledge ingest \
  --collection "references" \
  --title "API Guidelines" \
  --file ~/docs/api-guidelines.md \
  --namespace claude-desktop
```

## Troubleshooting

### Cortex tools not appearing

1. Check that Cortex is in your PATH: `which cortex`
2. Verify the config file syntax is valid JSON
3. Check Claude Desktop logs for errors
4. Restart Claude Desktop completely

### Connection errors

1. Ensure no other process is using Cortex
2. Check file permissions on the data directory
3. Try running `cortex serve` manually to see errors

### Data not persisting

1. Verify the namespace matches between CLI and config
2. Check that the data directory is writable
3. Ensure you're not running multiple instances with different data dirs
