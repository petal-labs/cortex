# Contributing to Cortex

Thank you for your interest in contributing to Cortex! This guide will help you get started.

## Getting Started

### Prerequisites

- Go 1.24 or later
- CGO enabled (required for SQLite)
- Git

### Development Setup

```bash
# Clone the repository
git clone https://github.com/petal-labs/cortex.git
cd cortex

# Install dependencies
go mod download

# Build
go build -o cortex ./cmd/cortex

# Run tests
go test ./...
```

### Project Structure

```
cortex/
├── cmd/cortex/          # CLI entrypoint
├── internal/
│   ├── cmd/             # CLI commands
│   ├── config/          # Configuration loading
│   ├── context/         # Workflow context engine
│   ├── conversation/    # Conversation memory engine
│   ├── dashboard/       # Web dashboard
│   ├── embedding/       # Embedding providers
│   ├── entity/          # Entity memory engine
│   ├── gc/              # Garbage collection
│   ├── knowledge/       # Knowledge store engine
│   ├── observability/   # Metrics and logging
│   ├── server/          # MCP server
│   ├── storage/         # Storage backends (SQLite, pgvector)
│   ├── summarization/   # Conversation summarization
│   └── tui/             # Terminal UI
└── pkg/types/           # Shared types
```

## How to Contribute

### Reporting Bugs

1. Search [existing issues](https://github.com/petal-labs/cortex/issues) to avoid duplicates
2. Use the [bug report template](https://github.com/petal-labs/cortex/issues/new?template=bug_report.yml)
3. Include version, OS, CPU architecture, and relevant logs
4. Provide steps to reproduce the issue

### Suggesting Features

1. Search existing issues and discussions first
2. Use the [feature request template](https://github.com/petal-labs/cortex/issues/new?template=feature_request.yml)
3. Explain the problem you're trying to solve
4. Describe your proposed solution

### Submitting Code

1. Fork the repository
2. Create a feature branch from `main`
3. Make your changes
4. Add or update tests as needed
5. Ensure all tests pass
6. Submit a pull request

## Development Guidelines

### Code Style

- Follow standard Go conventions and idioms
- Run `gofmt` and `goimports` before committing
- Use meaningful variable and function names
- Keep functions focused and reasonably sized

```bash
# Format code
gofmt -w .
goimports -w .

# Run linter
golangci-lint run
```

### Testing

- Write unit tests for new functionality
- Maintain or improve code coverage
- Tests should be deterministic and fast

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -race -coverprofile=coverage.out ./...

# View coverage report
go tool cover -html=coverage.out
```

### Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
type(scope): description

[optional body]

[optional footer]
```

**Types:**
- `feat` - New feature
- `fix` - Bug fix
- `docs` - Documentation only
- `style` - Formatting, no code change
- `refactor` - Code restructuring
- `perf` - Performance improvement
- `test` - Adding or updating tests
- `ci` - CI/CD changes
- `chore` - Maintenance tasks

**Examples:**
```
feat(knowledge): add semantic chunking strategy
fix(storage): handle nil pointer in pgvector query
docs: update installation instructions
```

### Pull Request Process

1. Fill out the PR template completely
2. Link related issues using "Fixes #123" or "Relates to #123"
3. Ensure CI checks pass (tests, lint, security scan)
4. Request review from maintainers
5. Address review feedback
6. Squash commits if requested

### Branch Naming

- `feature/description` - New features
- `fix/description` - Bug fixes
- `docs/description` - Documentation updates
- `refactor/description` - Code refactoring

## Architecture Notes

### Storage Backends

Cortex supports two storage backends:

- **SQLite + sqlite-vec** - Default, zero-configuration, suitable for single-node
- **PostgreSQL + pgvector** - Production-scale, requires external database

When adding storage features, implement for both backends.

### Memory Primitives

The four memory primitives share common patterns:

- Each has an engine in `internal/{primitive}/`
- Storage operations go through `internal/storage/`
- MCP tools are registered in `internal/server/mcp.go`
- CLI commands are in `internal/cmd/`

### Testing with Different Backends

```bash
# SQLite (default)
go test ./...

# PostgreSQL (requires running instance)
DATABASE_URL=postgres://localhost:5432/cortex_test go test ./...
```

## Getting Help

- Check the [README](README.md) for usage documentation
- Search [existing issues](https://github.com/petal-labs/cortex/issues)
- Open a new issue for bugs or feature requests

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
