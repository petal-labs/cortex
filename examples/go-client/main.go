// Example: Using Cortex as a Go library
//
// This example demonstrates how to use Cortex's knowledge store
// programmatically in your own Go applications.
//
// Run with: go run main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
	"github.com/petal-labs/cortex/pkg/types"
)

func main() {
	ctx := context.Background()

	// Create a temporary directory for the database
	tmpDir, err := os.MkdirTemp("", "cortex-example-*")
	if err != nil {
		log.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	dbPath := filepath.Join(tmpDir, "cortex.db")
	fmt.Printf("Using database: %s\n\n", dbPath)

	// Create configuration
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = tmpDir

	// Initialize SQLite storage backend
	store, err := sqlite.New(cfg)
	if err != nil {
		log.Fatalf("Failed to create storage: %v", err)
	}
	defer func() { _ = store.Close() }()

	// Run migrations
	if err := store.Migrate(ctx); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Create knowledge engine (without embedding provider for this example)
	// For semantic search, you would add an embedding provider here
	engine, err := knowledge.NewEngine(store, nil, &cfg.Knowledge)
	if err != nil {
		log.Fatalf("Failed to create knowledge engine: %v", err)
	}

	namespace := "example"

	// Create a collection
	fmt.Println("Creating collection 'docs'...")
	collection, err := engine.CreateCollection(ctx, namespace, knowledge.CreateCollectionOpts{
		Name:        "docs",
		Description: "Example documentation",
	})
	if err != nil {
		log.Fatalf("Failed to create collection: %v", err)
	}
	fmt.Printf("Created collection: %s\n\n", collection.ID)

	// Ingest some documents
	documents := []struct {
		title   string
		content string
	}{
		{
			title: "Getting Started",
			content: `Cortex is a memory and knowledge service for AI agents.
It provides persistent context, vector-backed knowledge retrieval,
and conversation memory through the Model Context Protocol (MCP).`,
		},
		{
			title: "Installation",
			content: `To install Cortex, download the latest release from GitHub
or build from source using Go 1.24 or later. CGO must be enabled
for SQLite support.`,
		},
		{
			title: "Configuration",
			content: `Cortex can be configured using a YAML file at ~/.cortex/config.yaml.
You can specify the storage backend (sqlite or pgvector), embedding
service endpoint, and various memory primitive settings.`,
		},
	}

	fmt.Println("Ingesting documents...")
	for _, doc := range documents {
		result, err := engine.Ingest(ctx, namespace, collection.ID, doc.content, &knowledge.IngestOpts{
			Title: doc.title,
			ChunkConfig: &types.ChunkConfig{
				Strategy:  "sentence",
				MaxTokens: 256,
				Overlap:   20,
			},
		})
		if err != nil {
			log.Fatalf("Failed to ingest document: %v", err)
		}
		fmt.Printf("  - %s: %d chunks\n", doc.title, result.ChunksCreated)
	}
	fmt.Println()

	// List collections
	fmt.Println("Listing collections...")
	collections, _, err := engine.ListCollections(ctx, namespace, "", 100)
	if err != nil {
		log.Fatalf("Failed to list collections: %v", err)
	}
	for _, c := range collections {
		fmt.Printf("  - %s: %s\n", c.Name, c.Description)
	}
	fmt.Println()

	// Get collection stats
	stats, err := engine.CollectionStats(ctx, namespace, collection.ID)
	if err != nil {
		log.Fatalf("Failed to get stats: %v", err)
	}
	fmt.Printf("Collection stats: %d documents, %d chunks, %d total tokens\n",
		stats.DocumentCount, stats.ChunkCount, stats.TotalTokens)

	fmt.Println("\nDone!")
	fmt.Println("\nNote: To enable semantic search, configure an embedding provider")
	fmt.Println("such as Iris (https://github.com/petal-labs/iris)")
}
