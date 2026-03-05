.PHONY: all build test lint fmt vet sec clean install run tui help

# Build variables
BINARY_NAME := cortex
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Go variables
GOBIN ?= $(shell go env GOPATH)/bin

all: lint test build

## Build

build: ## Build the binary
	CGO_ENABLED=1 go build -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) ./cmd/cortex

install: ## Install the binary to GOBIN
	CGO_ENABLED=1 go install -ldflags "$(LDFLAGS)" ./cmd/cortex

## Run

run: ## Run the MCP server (stdio)
	go run ./cmd/cortex serve

run-sse: ## Run the MCP server (SSE transport)
	go run ./cmd/cortex serve --transport sse --port 9810

tui: ## Run the terminal UI
	go run ./cmd/cortex tui

## Test

test: ## Run tests
	go test -v -race ./...

test-cover: ## Run tests with coverage
	go test -v -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out -o coverage.html

test-short: ## Run tests (short mode)
	go test -v -short ./...

## Lint & Format

lint: ## Run golangci-lint
	golangci-lint run --timeout=5m

fmt: ## Format code
	gofmt -s -w .
	goimports -w -local github.com/petal-labs/cortex .

vet: ## Run go vet
	go vet ./...

sec: ## Run gosec security scanner
	gosec -exclude-dir=.git -exclude-dir=vendor ./...

check: fmt vet lint sec ## Run all checks (fmt, vet, lint, sec)

## Dependencies

deps: ## Download dependencies
	go mod download

deps-update: ## Update dependencies
	go get -u ./...
	go mod tidy

deps-verify: ## Verify dependencies
	go mod verify

## Clean

clean: ## Remove build artifacts
	rm -f $(BINARY_NAME)
	rm -f coverage.out coverage.html
	go clean -cache

## Tools

tools: ## Install development tools
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	go install github.com/goreleaser/goreleaser/v2@latest

## Release

release-dry: ## Dry run release
	goreleaser release --snapshot --clean

release: ## Create a release (requires GITHUB_TOKEN)
	goreleaser release --clean

## Help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'
