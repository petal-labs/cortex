# Changelog

All notable changes to Cortex will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[0.2.0]: https://github.com/petal-labs/cortex/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/petal-labs/cortex/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/petal-labs/cortex/releases/tag/v0.1.0
