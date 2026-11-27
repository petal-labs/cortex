// Example: Using Cortex as a Go library via the public pkg/cortex SDK.
//
// This example demonstrates how external Go applications can embed Cortex
// using only github.com/petal-labs/cortex/pkg/cortex and pkg/types
// (with zero internal/ imports).
//
// Run with: go run -tags sqlite_fts5 ./examples/go-client
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/petal-labs/cortex/pkg/cortex"
	"github.com/petal-labs/cortex/pkg/types"
)

func main() {
	ctx := context.Background()

	// Create a temporary directory for the SQLite database
	tmpDir, err := os.MkdirTemp("", "cortex-example-*")
	if err != nil {
		log.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	fmt.Printf("Using database: %s\n\n", filepath.Join(tmpDir, "cortex.db"))

	// Open Cortex with the local SQLite backend (automatically runs migrations).
	// For semantic search, pass cortex.WithIrisEmbedding(...) or cortex.WithEmbeddingProvider(...).
	client, err := cortex.Open(ctx, cortex.WithSQLite(tmpDir))
	if err != nil {
		log.Fatalf("Failed to open Cortex: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("Failed to close Cortex: %v", err)
		}
	}()

	namespace := "example"

	// 1. Knowledge Store: create collection and ingest documents
	fmt.Println("Creating collection 'docs'...")
	collection, err := client.Knowledge().CreateCollection(ctx, namespace, cortex.CreateCollectionOpts{
		Name:        "docs",
		Description: "Example documentation",
	})
	if err != nil {
		log.Fatalf("Failed to create collection: %v", err)
	}
	fmt.Printf("Created collection: %s\n\n", collection.ID)

	documents := []struct {
		title   string
		content string
	}{
		{
			title: "Getting Started",
			content: `Cortex is a memory and knowledge service for AI agents.
It provides persistent context, vector-backed knowledge retrieval,
and conversation memory through the Model Context Protocol (MCP) and Go SDK.`,
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
		result, err := client.Knowledge().Ingest(ctx, namespace, collection.ID, doc.content, &cortex.IngestOpts{
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
	collections, _, err := client.Knowledge().ListCollections(ctx, namespace, "", 100)
	if err != nil {
		log.Fatalf("Failed to list collections: %v", err)
	}
	for _, c := range collections {
		fmt.Printf("  - %s: %s\n", c.Name, c.Description)
	}
	fmt.Println()

	// Get collection stats
	stats, err := client.Knowledge().CollectionStats(ctx, namespace, collection.ID)
	if err != nil {
		log.Fatalf("Failed to get stats: %v", err)
	}
	fmt.Printf("Collection stats: %d documents, %d chunks, %d total tokens\n\n",
		stats.DocumentCount, stats.ChunkCount, stats.TotalTokens)

	// 2. Workflow Context: store and read structured state
	if _, err := client.Context().Set(ctx, namespace, "onboarding.status", map[string]any{
		"completed": true,
		"docs":      stats.DocumentCount,
	}, nil); err != nil {
		log.Fatalf("Failed to set context: %v", err)
	}
	ctxVal, err := client.Context().Get(ctx, namespace, "onboarding.status", nil)
	if err != nil {
		log.Fatalf("Failed to get context: %v", err)
	}
	fmt.Printf("Workflow context (v%d): %v\n\n", ctxVal.Version, ctxVal.Value)

	// 3. Conversation Memory: append and read back history
	if _, err := client.Conversation().Append(ctx, namespace, "demo-thread", "user", "How do I install Cortex?", nil); err != nil {
		log.Fatalf("Failed to append message: %v", err)
	}
	hist, err := client.Conversation().History(ctx, namespace, "demo-thread", nil)
	if err != nil {
		log.Fatalf("Failed to get history: %v", err)
	}
	fmt.Printf("Conversation history (%s): %d message(s)\n\n", hist.ThreadID, len(hist.Messages))

	// 4. Entity Memory: create entities and link them
	cortexEnt, err := client.Entity().Create(ctx, namespace, "Cortex", types.EntityTypeProduct, &cortex.EntityCreateOpts{
		Summary: "Memory and knowledge service for AI agents",
	})
	if err != nil {
		log.Fatalf("Failed to create entity: %v", err)
	}
	fmt.Printf("Created entity: %s (%s)\n", cortexEnt.Entity.Name, cortexEnt.Entity.Type)

	fmt.Println("\nDone!")
	fmt.Println("\nNote: To enable semantic search, configure an embedding provider with")
	fmt.Println("cortex.WithIrisEmbedding(...) or cortex.WithEmbeddingProvider(...).")
}
