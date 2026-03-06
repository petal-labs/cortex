// Example: Research Agent with PetalFlow and Cortex
//
// This example demonstrates how to build a multi-agent research workflow
// using PetalFlow's Agent/Task schema. The workflow:
// 1. Searches the Cortex knowledge store for relevant information
// 2. Analyzes the findings to identify key insights
// 3. Generates a structured research report
//
// Prerequisites:
// - Cortex installed and configured
// - Ollama running locally with llama3.2: ollama pull llama3.2
// - Documents ingested into Cortex knowledge store
//
// Run: go run main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
	"github.com/petal-labs/iris/providers/ollama"
	"github.com/petal-labs/petalflow"
	"github.com/petal-labs/petalflow/agent"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/irisadapter"
	"github.com/petal-labs/petalflow/registry"
)

// CortexSearchTool implements a search tool that queries the Cortex knowledge store.
type CortexSearchTool struct {
	engine    *knowledge.Engine
	namespace string
}

// NewCortexSearchTool creates a search tool backed by Cortex.
func NewCortexSearchTool(engine *knowledge.Engine, namespace string) *CortexSearchTool {
	return &CortexSearchTool{
		engine:    engine,
		namespace: namespace,
	}
}

// Name returns the tool's name.
func (t *CortexSearchTool) Name() string { return "cortex_search" }

// Description returns a description for the LLM.
func (t *CortexSearchTool) Description() string {
	return "Search the Cortex knowledge store for relevant documents and information. Returns matching text chunks with relevance scores."
}

// Parameters returns the JSON schema for the tool's parameters.
func (t *CortexSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query to find relevant documents",
			},
			"collection": map[string]any{
				"type":        "string",
				"description": "Optional collection ID to search within",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return (default: 5)",
			},
		},
		"required": []string{"query"},
	}
}

// Invoke executes the search.
func (t *CortexSearchTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	topK := 5
	if l, ok := args["limit"].(float64); ok {
		topK = int(l)
	}

	var collectionID *string
	if c, ok := args["collection"].(string); ok && c != "" {
		collectionID = &c
	}

	// Execute search
	searchResult, err := t.engine.Search(ctx, t.namespace, query, &knowledge.SearchOpts{
		CollectionID: collectionID,
		TopK:         topK,
		SearchMode:   knowledge.SearchModeHybrid,
	})
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Format results
	docs := make([]map[string]any, 0, len(searchResult.Results))
	for _, r := range searchResult.Results {
		docs = append(docs, map[string]any{
			"content":  r.Chunk.Content,
			"score":    r.Score,
			"document": r.DocumentTitle,
			"source":   r.Source,
		})
	}

	return map[string]any{
		"results": docs,
		"count":   len(docs),
		"query":   query,
	}, nil
}

func main() {
	ctx := context.Background()

	// Initialize Cortex storage
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Failed to get home directory: %v\n", err)
		os.Exit(1)
	}

	cortexDir := filepath.Join(homeDir, ".cortex")
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = cortexDir

	store, err := sqlite.New(cfg)
	if err != nil {
		fmt.Printf("Failed to open Cortex storage: %v\n", err)
		fmt.Println("Make sure Cortex is configured and has data ingested.")
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		fmt.Printf("Failed to run migrations: %v\n", err)
		os.Exit(1)
	}

	// Create knowledge engine (without embedding provider for simplicity)
	engine, err := knowledge.NewEngine(store, nil, &cfg.Knowledge)
	if err != nil {
		fmt.Printf("Failed to create knowledge engine: %v\n", err)
		os.Exit(1)
	}

	namespace := "default"

	// Create the Cortex search tool
	searchTool := NewCortexSearchTool(engine, namespace)

	// Register the tool with PetalFlow's global registry
	reg := registry.Global()
	reg.RegisterTool("cortex_search", registry.NodeTypeDef{
		ID:          "cortex_search",
		Name:        "Cortex Search",
		Description: searchTool.Description(),
		IsTool:      true,
		ToolMode:    "function_call",
	})

	// Create LLM client
	provider := ollama.New(ollama.WithBaseURL("http://localhost:11434"))
	client := irisadapter.NewProviderAdapter(provider)

	// Load the agent workflow
	workflowPath := filepath.Join(".", "research_workflow.json")
	wf, err := agent.LoadFromFile(workflowPath)
	if err != nil {
		fmt.Printf("Failed to load workflow: %v\n", err)
		os.Exit(1)
	}

	// Validate the workflow
	if errs := agent.Validate(wf); len(errs) > 0 {
		fmt.Println("Workflow validation errors:")
		for _, e := range errs {
			fmt.Printf("  - %v\n", e)
		}
		os.Exit(1)
	}

	// Compile to graph definition
	graphDef, err := agent.Compile(wf)
	if err != nil {
		fmt.Printf("Failed to compile workflow: %v\n", err)
		os.Exit(1)
	}

	// Print compiled graph info
	fmt.Printf("Compiled workflow: %s\n", graphDef.ID)
	fmt.Printf("  Entry node: %s\n", graphDef.Entry)
	fmt.Printf("  Nodes: %d\n", len(graphDef.Nodes))
	fmt.Printf("  Edges: %d\n\n", len(graphDef.Edges))

	// Create tool registry for hydration
	toolRegistry := petalflow.NewToolRegistry()
	toolRegistry.Register(searchTool)

	// Hydrate the graph definition into a runnable graph
	hydrateOpts := hydrate.Options{
		LLMClient:    client,
		ToolRegistry: toolRegistry,
	}

	graph, err := hydrate.Hydrate(graphDef, hydrateOpts)
	if err != nil {
		fmt.Printf("Failed to hydrate graph: %v\n", err)
		os.Exit(1)
	}

	// Create the research query
	query := "What are the main features and capabilities of Cortex?"
	if len(os.Args) > 1 {
		query = os.Args[1]
	}

	fmt.Printf("Research Query: %s\n", query)
	fmt.Println("=" + string(make([]byte, 60)))
	fmt.Println()

	// Create input envelope
	env := petalflow.NewEnvelope().WithVar("query", query)

	// Create runtime with event handler
	runtime := petalflow.NewRuntime()
	opts := petalflow.DefaultRunOptions()
	opts.Timeout = 5 * time.Minute
	opts.EventHandler = func(event petalflow.Event) {
		switch event.Kind {
		case petalflow.EventNodeStarted:
			fmt.Printf("[%s] Starting node: %s\n", time.Now().Format("15:04:05"), event.NodeID)
		case petalflow.EventNodeFinished:
			fmt.Printf("[%s] Completed node: %s\n", time.Now().Format("15:04:05"), event.NodeID)
		case petalflow.EventNodeFailed:
			fmt.Printf("[%s] Node failed: %s - %v\n", time.Now().Format("15:04:05"), event.NodeID, event.Error)
		}
	}

	// Run the workflow
	result, err := runtime.Run(ctx, graph, env, opts)
	if err != nil {
		fmt.Printf("\nWorkflow failed: %v\n", err)
		os.Exit(1)
	}

	// Display results
	fmt.Println("\n" + string(make([]byte, 60)) + "=")
	fmt.Println("RESEARCH REPORT")
	fmt.Println(string(make([]byte, 60)) + "=")

	if report, ok := result.GetVar("final_report"); ok {
		fmt.Println(report)
	} else {
		// Print all output variables
		fmt.Println("\nOutput variables:")
		data, _ := json.MarshalIndent(result.Vars(), "", "  ")
		fmt.Println(string(data))
	}
}
