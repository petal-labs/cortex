package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/petal-labs/cortex/internal/embedding"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/pkg/types"
)

var knowledgeCmd = &cobra.Command{
	Use:   "knowledge",
	Short: "Manage knowledge store",
	Long:  `Commands for managing the knowledge store including ingesting documents, searching, and viewing statistics.`,
}

var knowledgeIngestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "Ingest a document into the knowledge store",
	Long: `Ingest a document file into a collection. The document is chunked, embedded, and indexed for retrieval.

Examples:
  cortex knowledge ingest --namespace myapp --collection docs --file README.md
  cortex knowledge ingest --namespace myapp --collection docs --file manual.txt --title "User Manual"`,
	RunE: runKnowledgeIngest,
}

var knowledgeIngestDirCmd = &cobra.Command{
	Use:   "ingest-dir",
	Short: "Ingest all matching files from a directory",
	Long: `Ingest multiple files from a directory using a glob pattern.

Examples:
  cortex knowledge ingest-dir --namespace myapp --collection docs --dir ./docs --glob "**/*.md"
  cortex knowledge ingest-dir --namespace myapp --collection code --dir ./src --glob "*.go"`,
	RunE: runKnowledgeIngestDir,
}

var knowledgeSearchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search the knowledge store",
	Long: `Perform semantic search across the knowledge store.

Examples:
  cortex knowledge search --namespace myapp --query "how to configure authentication"
  cortex knowledge search --namespace myapp --query "error handling" --collection docs --top-k 5`,
	RunE: runKnowledgeSearch,
}

var knowledgeStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show knowledge store statistics",
	Long: `Display statistics for collections in the knowledge store.

Examples:
  cortex knowledge stats --namespace myapp
  cortex knowledge stats --namespace myapp --collection docs`,
	RunE: runKnowledgeStats,
}

var knowledgeCollectionsCmd = &cobra.Command{
	Use:   "collections",
	Short: "List collections in the knowledge store",
	Long: `List all collections in a namespace.

Examples:
  cortex knowledge collections --namespace myapp`,
	RunE: runKnowledgeCollections,
}

var knowledgeCreateCollectionCmd = &cobra.Command{
	Use:   "create-collection",
	Short: "Create a new collection",
	Long: `Create a new collection for storing documents.

Examples:
  cortex knowledge create-collection --namespace myapp --name docs --description "Documentation"`,
	RunE: runKnowledgeCreateCollection,
}

func init() {
	rootCmd.AddCommand(knowledgeCmd)

	// ingest command
	knowledgeCmd.AddCommand(knowledgeIngestCmd)
	knowledgeIngestCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	knowledgeIngestCmd.Flags().StringP("collection", "c", "", "Collection ID (required)")
	knowledgeIngestCmd.Flags().StringP("file", "f", "", "File to ingest (required)")
	knowledgeIngestCmd.Flags().String("title", "", "Document title (optional, defaults to filename)")
	knowledgeIngestCmd.Flags().String("content-type", "text", "Content type (text, markdown, html)")
	knowledgeIngestCmd.MarkFlagRequired("namespace")
	knowledgeIngestCmd.MarkFlagRequired("collection")
	knowledgeIngestCmd.MarkFlagRequired("file")

	// ingest-dir command
	knowledgeCmd.AddCommand(knowledgeIngestDirCmd)
	knowledgeIngestDirCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	knowledgeIngestDirCmd.Flags().StringP("collection", "c", "", "Collection ID (required)")
	knowledgeIngestDirCmd.Flags().StringP("dir", "d", "", "Directory to scan (required)")
	knowledgeIngestDirCmd.Flags().StringP("glob", "g", "**/*", "Glob pattern for files")
	knowledgeIngestDirCmd.Flags().String("content-type", "text", "Content type (text, markdown, html)")
	knowledgeIngestDirCmd.MarkFlagRequired("namespace")
	knowledgeIngestDirCmd.MarkFlagRequired("collection")
	knowledgeIngestDirCmd.MarkFlagRequired("dir")

	// search command
	knowledgeCmd.AddCommand(knowledgeSearchCmd)
	knowledgeSearchCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	knowledgeSearchCmd.Flags().StringP("query", "q", "", "Search query (required)")
	knowledgeSearchCmd.Flags().StringP("collection", "c", "", "Limit to collection (optional)")
	knowledgeSearchCmd.Flags().Int("top-k", 10, "Number of results")
	knowledgeSearchCmd.Flags().Int("context-window", 1, "Chunks before/after to include")
	knowledgeSearchCmd.MarkFlagRequired("namespace")
	knowledgeSearchCmd.MarkFlagRequired("query")

	// stats command
	knowledgeCmd.AddCommand(knowledgeStatsCmd)
	knowledgeStatsCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	knowledgeStatsCmd.Flags().StringP("collection", "c", "", "Collection ID (optional)")
	knowledgeStatsCmd.MarkFlagRequired("namespace")

	// collections command
	knowledgeCmd.AddCommand(knowledgeCollectionsCmd)
	knowledgeCollectionsCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	knowledgeCollectionsCmd.MarkFlagRequired("namespace")

	// create-collection command
	knowledgeCmd.AddCommand(knowledgeCreateCollectionCmd)
	knowledgeCreateCollectionCmd.Flags().StringP("namespace", "n", "", "Namespace (required)")
	knowledgeCreateCollectionCmd.Flags().String("name", "", "Collection name (required)")
	knowledgeCreateCollectionCmd.Flags().String("description", "", "Collection description")
	knowledgeCreateCollectionCmd.Flags().String("chunk-strategy", "sentence", "Chunking strategy (fixed, sentence, paragraph, semantic)")
	knowledgeCreateCollectionCmd.Flags().Int("chunk-max-tokens", 512, "Max tokens per chunk")
	knowledgeCreateCollectionCmd.Flags().Int("chunk-overlap", 50, "Token overlap between chunks")
	knowledgeCreateCollectionCmd.MarkFlagRequired("namespace")
	knowledgeCreateCollectionCmd.MarkFlagRequired("name")
}

// initKnowledgeEngine creates the storage backend and knowledge engine.
func initKnowledgeEngine(cmd *cobra.Command) (*knowledge.Engine, error) {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	store, err := createStorage(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage: %w", err)
	}

	// Create embedding provider if Iris is configured
	var emb embedding.Provider
	if cfg.Iris.Endpoint != "" {
		emb, err = embedding.NewIrisClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create embedding client: %w", err)
		}

		if cfg.Embedding.CacheSize > 0 {
			emb, err = embedding.NewCachedProvider(emb, cfg.Embedding.CacheSize)
			if err != nil {
				return nil, fmt.Errorf("failed to create embedding cache: %w", err)
			}
		}
	}

	engine, err := knowledge.NewEngine(store, emb, &cfg.Knowledge)
	if err != nil {
		return nil, fmt.Errorf("failed to create knowledge engine: %w", err)
	}

	return engine, nil
}

func runKnowledgeIngest(cmd *cobra.Command, args []string) error {
	engine, err := initKnowledgeEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	collectionID, _ := cmd.Flags().GetString("collection")
	filePath, _ := cmd.Flags().GetString("file")
	title, _ := cmd.Flags().GetString("title")
	contentType, _ := cmd.Flags().GetString("content-type")

	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Use filename as title if not specified
	if title == "" {
		title = filepath.Base(filePath)
	}

	ctx := context.Background()
	result, err := engine.Ingest(ctx, namespace, collectionID, string(content), &knowledge.IngestOpts{
		Title:       title,
		Source:      filePath,
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("failed to ingest document: %w", err)
	}

	fmt.Printf("Ingested document: %s\n", result.DocumentID)
	fmt.Printf("  Chunks created: %d\n", result.ChunksCreated)
	fmt.Printf("  Collection: %s\n", result.CollectionID)

	return nil
}

func runKnowledgeIngestDir(cmd *cobra.Command, args []string) error {
	engine, err := initKnowledgeEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	collectionID, _ := cmd.Flags().GetString("collection")
	dir, _ := cmd.Flags().GetString("dir")
	globPattern, _ := cmd.Flags().GetString("glob")
	contentType, _ := cmd.Flags().GetString("content-type")

	// Find matching files
	pattern := filepath.Join(dir, globPattern)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("invalid glob pattern: %w", err)
	}

	if len(matches) == 0 {
		fmt.Println("No files matched the pattern")
		return nil
	}

	ctx := context.Background()
	var totalDocs, totalChunks int
	var errors []string

	for _, filePath := range matches {
		// Skip directories
		info, err := os.Stat(filePath)
		if err != nil || info.IsDir() {
			continue
		}

		content, err := os.ReadFile(filePath)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", filePath, err))
			continue
		}

		result, err := engine.Ingest(ctx, namespace, collectionID, string(content), &knowledge.IngestOpts{
			Title:       filepath.Base(filePath),
			Source:      filePath,
			ContentType: contentType,
		})
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", filePath, err))
			continue
		}

		totalDocs++
		totalChunks += result.ChunksCreated
		fmt.Printf("  Ingested: %s (%d chunks)\n", filePath, result.ChunksCreated)
	}

	fmt.Printf("\nSummary:\n")
	fmt.Printf("  Documents ingested: %d\n", totalDocs)
	fmt.Printf("  Total chunks: %d\n", totalChunks)
	if len(errors) > 0 {
		fmt.Printf("  Errors: %d\n", len(errors))
		for _, e := range errors {
			fmt.Printf("    - %s\n", e)
		}
	}

	return nil
}

func runKnowledgeSearch(cmd *cobra.Command, args []string) error {
	engine, err := initKnowledgeEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	query, _ := cmd.Flags().GetString("query")
	collection, _ := cmd.Flags().GetString("collection")
	topK, _ := cmd.Flags().GetInt("top-k")
	contextWindow, _ := cmd.Flags().GetInt("context-window")

	opts := &knowledge.SearchOpts{
		TopK:          topK,
		ContextWindow: contextWindow,
	}
	if collection != "" {
		opts.CollectionID = &collection
	}

	ctx := context.Background()
	result, err := engine.Search(ctx, namespace, query, opts)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	fmt.Printf("Search results for: %q\n", query)
	fmt.Printf("Found: %d results\n\n", result.TotalFound)

	for i, r := range result.Results {
		fmt.Printf("--- Result %d (score: %.3f) ---\n", i+1, r.Score)
		fmt.Printf("Document: %s\n", r.Chunk.DocumentID)
		fmt.Printf("Collection: %s\n", r.Chunk.CollectionID)

		if r.ContextBefore != "" {
			fmt.Printf("\n[Context before]\n%s\n", r.ContextBefore)
		}

		fmt.Printf("\n[Match]\n%s\n", r.Chunk.Content)

		if r.ContextAfter != "" {
			fmt.Printf("\n[Context after]\n%s\n", r.ContextAfter)
		}
		fmt.Println()
	}

	return nil
}

func runKnowledgeStats(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := createStorage(cfg)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	collectionID, _ := cmd.Flags().GetString("collection")

	ctx := context.Background()

	if collectionID != "" {
		// Stats for a specific collection
		col, err := store.GetCollection(ctx, namespace, collectionID)
		if err != nil {
			return fmt.Errorf("failed to get collection: %w", err)
		}
		stats, err := store.CollectionStats(ctx, namespace, collectionID)
		if err != nil {
			return fmt.Errorf("failed to get stats: %w", err)
		}
		printCollectionStats(col, stats)
	} else {
		// Stats for all collections
		collections, _, err := store.ListCollections(ctx, namespace, "", 100)
		if err != nil {
			return fmt.Errorf("failed to list collections: %w", err)
		}

		if len(collections) == 0 {
			fmt.Println("No collections found")
			return nil
		}

		for _, col := range collections {
			stats, err := store.CollectionStats(ctx, namespace, col.ID)
			if err != nil {
				fmt.Printf("Error getting stats for %s: %v\n", col.ID, err)
				continue
			}
			printCollectionStats(col, stats)
			fmt.Println()
		}
	}

	return nil
}

func printCollectionStats(col *types.Collection, stats *types.CollectionStats) {
	fmt.Printf("Collection: %s\n", col.ID)
	fmt.Printf("  Name: %s\n", col.Name)
	fmt.Printf("  Documents: %d\n", stats.DocumentCount)
	fmt.Printf("  Chunks: %d\n", stats.ChunkCount)
	fmt.Printf("  Total tokens: %d\n", stats.TotalTokens)
	if stats.ChunkCount > 0 {
		avgSize := float64(stats.TotalTokens) / float64(stats.ChunkCount)
		fmt.Printf("  Avg chunk size: %.1f tokens\n", avgSize)
	}
}

func runKnowledgeCollections(cmd *cobra.Command, args []string) error {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := createStorage(cfg)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	ctx := context.Background()

	collections, _, err := store.ListCollections(ctx, namespace, "", 100)
	if err != nil {
		return fmt.Errorf("failed to list collections: %w", err)
	}

	if len(collections) == 0 {
		fmt.Println("No collections found")
		return nil
	}

	fmt.Printf("Collections in namespace %q:\n\n", namespace)
	for _, col := range collections {
		fmt.Printf("  %s\n", col.ID)
		fmt.Printf("    Name: %s\n", col.Name)
		if col.Description != "" {
			fmt.Printf("    Description: %s\n", col.Description)
		}
		fmt.Printf("    Chunk strategy: %s\n", col.ChunkConfig.Strategy)
		fmt.Printf("    Created: %s\n", col.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Println()
	}

	return nil
}

func runKnowledgeCreateCollection(cmd *cobra.Command, args []string) error {
	engine, err := initKnowledgeEngine(cmd)
	if err != nil {
		return err
	}

	namespace, _ := cmd.Flags().GetString("namespace")
	name, _ := cmd.Flags().GetString("name")
	description, _ := cmd.Flags().GetString("description")
	chunkStrategy, _ := cmd.Flags().GetString("chunk-strategy")
	maxTokens, _ := cmd.Flags().GetInt("chunk-max-tokens")
	overlap, _ := cmd.Flags().GetInt("chunk-overlap")

	ctx := context.Background()

	collection, err := engine.CreateCollection(ctx, namespace, knowledge.CreateCollectionOpts{
		Name:        name,
		Description: description,
		ChunkConfig: &types.ChunkConfig{
			Strategy:  chunkStrategy,
			MaxTokens: maxTokens,
			Overlap:   overlap,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create collection: %w", err)
	}

	fmt.Printf("Created collection: %s\n", collection.ID)
	fmt.Printf("  Name: %s\n", collection.Name)
	if collection.Description != "" {
		fmt.Printf("  Description: %s\n", collection.Description)
	}

	// Output as JSON for scripting
	if cmd.Flags().Changed("output") {
		output, _ := cmd.Flags().GetString("output")
		if output == "json" {
			data, _ := json.MarshalIndent(collection, "", "  ")
			fmt.Println(string(data))
		}
	}

	return nil
}
