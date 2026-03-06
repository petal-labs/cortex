# Cortex CLI Basics

This guide covers common CLI usage patterns for Cortex.

## Knowledge Store

### Ingest Documents

```bash
# Ingest a single file
cortex knowledge ingest \
  --collection docs \
  --title "README" \
  --file README.md

# Ingest with custom chunking
cortex knowledge ingest \
  --collection docs \
  --title "API Reference" \
  --file api.md \
  --chunk-strategy semantic \
  --chunk-max-tokens 512

# Ingest a directory of files
cortex knowledge ingest-dir \
  --collection project-docs \
  --dir ./docs \
  --pattern "*.md"
```

### Search Knowledge

```bash
# Basic search
cortex knowledge search "how to configure"

# Search specific collection
cortex knowledge search "authentication" --collection docs

# Hybrid search (vector + full-text)
cortex knowledge search "user management" --mode hybrid

# Full-text search only
cortex knowledge search "error handling" --mode fts
```

### Manage Collections

```bash
# List collections
cortex knowledge collections

# Create a collection
cortex knowledge create-collection \
  --name research \
  --description "Research papers and notes"

# Get stats
cortex knowledge stats
```

## Conversation Memory

### Store Conversations

```bash
# Add a message
cortex conversation append \
  --thread-id project-chat \
  --role user \
  --content "Let's discuss the architecture"

cortex conversation append \
  --thread-id project-chat \
  --role assistant \
  --content "Sure! What aspects would you like to cover?"
```

### Retrieve History

```bash
# Get recent history
cortex conversation history --thread-id project-chat

# Limit results
cortex conversation history --thread-id project-chat --limit 10

# Search across conversations
cortex conversation search "architecture decisions"
```

### Summarize

```bash
# Summarize a long conversation
cortex conversation summarize --thread-id project-chat
```

## Workflow Context

### Store Context

```bash
# Set a simple value
cortex context set current_task "implementing auth"

# Set JSON data
cortex context set project_config '{"env": "dev", "debug": true}'

# Set with TTL (auto-expires)
cortex context set temp_token "abc123" --ttl 1h
```

### Retrieve Context

```bash
# Get a value
cortex context get current_task

# List all keys
cortex context list

# List with prefix filter
cortex context list --prefix project_
```

### Context History

```bash
# View version history
cortex context history current_task
```

## Entity Memory

### Query Entities

```bash
# List all entities
cortex entity list

# Filter by type
cortex entity list --type person

# Search entities
cortex entity search "software engineer"
```

### Manage Entities

```bash
# Create an entity
cortex entity create \
  --name "Acme Corp" \
  --type organization

# Add an alias
cortex entity add-alias <entity-id> --alias "Acme"

# Add a relationship
cortex entity add-relationship <person-id> <org-id> --type works_at
```

## Namespaces

All commands support namespaces for data isolation:

```bash
# Use a specific namespace
cortex knowledge search "query" --namespace project-a
cortex conversation history --thread-id chat --namespace project-a

# List namespace stats
cortex namespace stats --namespace project-a
```

## Server Commands

### Start MCP Server

```bash
# stdio transport (default, for MCP clients)
cortex serve

# SSE transport (for web clients)
cortex serve --transport sse --port 9810

# Restrict to namespace
cortex serve --namespace my-project
```

### Launch TUI

```bash
# Interactive terminal UI
cortex tui

# With specific namespace
cortex tui --namespace my-project
```

## Maintenance

### Backup

```bash
# Create a backup
cortex backup --output cortex-backup.db
```

### Export

```bash
# Export namespace data as JSON
cortex export --namespace default --output export.json
```

### Garbage Collection

```bash
# Preview what would be cleaned
cortex gc --dry-run

# Run garbage collection
cortex gc

# Clean all namespaces
cortex gc --all
```

## Tips

### Shell Completion

```bash
# Bash
cortex completion bash > /etc/bash_completion.d/cortex

# Zsh
cortex completion zsh > "${fpath[1]}/_cortex"

# Fish
cortex completion fish > ~/.config/fish/completions/cortex.fish
```

### Debug Mode

```bash
# Enable verbose logging
cortex --log-level debug serve
```

### Configuration File

Create `~/.cortex/config.yaml` for persistent settings:

```yaml
storage:
  backend: sqlite
  data_dir: ~/.cortex/data

embedding:
  dimensions: 1536

knowledge:
  default_chunk_strategy: sentence
  default_chunk_max_tokens: 512
```
