// Example: PetalFlow Agent with Cortex Tools
//
// This example demonstrates how to use Cortex as a tool provider for PetalFlow
// agents. It implements a customer support agent that uses three Cortex tools:
//
// 1. cortex_knowledge_search - Search the knowledge base for documentation
// 2. cortex_context_get - Retrieve stored context (customer history)
// 3. cortex_context_set - Store context for future interactions
//
// Prerequisites:
// - Cortex installed and configured with documents ingested
// - Ollama running locally with llama3.2: ollama pull llama3.2
//
// Run: go run main.go "How do I configure the embedding service?"
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
	"github.com/petal-labs/iris/providers/ollama"
	"github.com/petal-labs/petalflow"
	"github.com/petal-labs/petalflow/agent"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/irisadapter"
	"github.com/petal-labs/petalflow/registry"
)

// CortexToolProvider provides Cortex-backed tools for PetalFlow agents.
type CortexToolProvider struct {
	knowledgeEngine *knowledge.Engine
	contextEngine   *ctxengine.Engine
	namespace       string
}

// NewCortexToolProvider creates a new tool provider backed by Cortex engines.
func NewCortexToolProvider(ke *knowledge.Engine, ce *ctxengine.Engine, namespace string) *CortexToolProvider {
	return &CortexToolProvider{
		knowledgeEngine: ke,
		contextEngine:   ce,
		namespace:       namespace,
	}
}

// KnowledgeSearchTool searches the Cortex knowledge store.
type KnowledgeSearchTool struct {
	provider *CortexToolProvider
}

func (t *KnowledgeSearchTool) Name() string { return "cortex_knowledge_search" }

func (t *KnowledgeSearchTool) Description() string {
	return "Search the Cortex knowledge base for relevant documentation. Returns matching text chunks with relevance scores."
}

func (t *KnowledgeSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query to find relevant documentation",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results (default: 5)",
			},
		},
		"required": []string{"query"},
	}
}

func (t *KnowledgeSearchTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	topK := 5
	if l, ok := args["limit"].(float64); ok {
		topK = int(l)
	}

	result, err := t.provider.knowledgeEngine.Search(ctx, t.provider.namespace, query, &knowledge.SearchOpts{
		TopK:       topK,
		SearchMode: knowledge.SearchModeHybrid,
	})
	if err != nil {
		return map[string]any{
			"error":   err.Error(),
			"results": []any{},
		}, nil
	}

	docs := make([]map[string]any, 0, len(result.Results))
	for _, r := range result.Results {
		docs = append(docs, map[string]any{
			"content":  r.Chunk.Content,
			"score":    r.Score,
			"document": r.DocumentTitle,
		})
	}

	return map[string]any{
		"results": docs,
		"count":   len(docs),
		"query":   query,
	}, nil
}

// ContextGetTool retrieves context from Cortex.
type ContextGetTool struct {
	provider *CortexToolProvider
}

func (t *ContextGetTool) Name() string { return "cortex_context_get" }

func (t *ContextGetTool) Description() string {
	return "Retrieve stored context from Cortex by key. Returns the value if found, or null if not."
}

func (t *ContextGetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key": map[string]any{
				"type":        "string",
				"description": "The context key to retrieve",
			},
		},
		"required": []string{"key"},
	}
}

func (t *ContextGetTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	key, _ := args["key"].(string)
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}

	result, err := t.provider.contextEngine.Get(ctx, t.provider.namespace, key, nil)
	if err != nil {
		return map[string]any{
			"key":   key,
			"found": false,
			"value": nil,
		}, nil
	}

	return map[string]any{
		"key":   key,
		"found": result.Exists,
		"value": result.Value,
	}, nil
}

// ContextSetTool stores context in Cortex.
type ContextSetTool struct {
	provider *CortexToolProvider
}

func (t *ContextSetTool) Name() string { return "cortex_context_set" }

func (t *ContextSetTool) Description() string {
	return "Store a value in Cortex context. The value persists across sessions and can be retrieved later."
}

func (t *ContextSetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key": map[string]any{
				"type":        "string",
				"description": "The context key to store",
			},
			"value": map[string]any{
				"type":        "string",
				"description": "The value to store (can be JSON string for complex data)",
			},
		},
		"required": []string{"key", "value"},
	}
}

func (t *ContextSetTool) Invoke(ctx context.Context, args map[string]any) (map[string]any, error) {
	key, _ := args["key"].(string)
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}

	value, _ := args["value"].(string)

	_, err := t.provider.contextEngine.Set(ctx, t.provider.namespace, key, value, nil)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, nil
	}

	return map[string]any{
		"success": true,
		"key":     key,
		"message": fmt.Sprintf("Context saved successfully for key: %s", key),
	}, nil
}

func main() {
	ctx := context.Background()

	// Parse command line arguments
	query := "How do I configure Cortex?"
	customerID := "demo-customer"

	if len(os.Args) > 1 {
		query = os.Args[1]
	}
	if len(os.Args) > 2 {
		customerID = os.Args[2]
	}

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
		fmt.Println("Make sure Cortex is configured. Run: cortex knowledge ingest ...")
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()

	if err := store.Migrate(ctx); err != nil {
		fmt.Printf("Failed to run migrations: %v\n", err)
		os.Exit(1)
	}

	// Create Cortex engines
	knowledgeEngine, err := knowledge.NewEngine(store, nil, &cfg.Knowledge)
	if err != nil {
		fmt.Printf("Failed to create knowledge engine: %v\n", err)
		os.Exit(1)
	}

	contextEngine, err := ctxengine.NewEngine(store, &cfg.Context)
	if err != nil {
		fmt.Printf("Failed to create context engine: %v\n", err)
		os.Exit(1)
	}

	namespace := "default"

	// Create tool provider
	toolProvider := NewCortexToolProvider(knowledgeEngine, contextEngine, namespace)

	// Create tools
	knowledgeSearchTool := &KnowledgeSearchTool{provider: toolProvider}
	contextGetTool := &ContextGetTool{provider: toolProvider}
	contextSetTool := &ContextSetTool{provider: toolProvider}

	// Register tools with PetalFlow registry
	reg := registry.Global()
	for _, toolName := range []string{"cortex_knowledge_search", "cortex_context_get", "cortex_context_set"} {
		reg.RegisterTool(toolName, registry.NodeTypeDef{
			ID:       toolName,
			Name:     toolName,
			IsTool:   true,
			ToolMode: "function_call",
		})
	}

	// Create LLM client
	provider := ollama.New(ollama.WithBaseURL("http://localhost:11434"))
	client := irisadapter.NewProviderAdapter(provider)

	// Load the agent workflow
	workflowPath := filepath.Join(".", "support_agent.json")
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

	// Create tool registry for hydration
	toolRegistry := petalflow.NewToolRegistry()
	toolRegistry.Register(knowledgeSearchTool)
	toolRegistry.Register(contextGetTool)
	toolRegistry.Register(contextSetTool)

	// Hydrate the graph
	hydrateOpts := hydrate.Options{
		LLMClient:    client,
		ToolRegistry: toolRegistry,
	}

	graph, err := hydrate.Hydrate(graphDef, hydrateOpts)
	if err != nil {
		fmt.Printf("Failed to hydrate graph: %v\n", err)
		os.Exit(1)
	}

	// Display header
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("CORTEX SUPPORT AGENT")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Customer ID: %s\n", customerID)
	fmt.Printf("Query: %s\n", query)
	fmt.Println(strings.Repeat("-", 60))
	fmt.Println()

	// Create input envelope
	env := petalflow.NewEnvelope().
		WithVar("query", query).
		WithVar("customer_id", customerID)

	// Create runtime with event handler
	runtime := petalflow.NewRuntime()
	opts := petalflow.DefaultRunOptions()
	opts.Timeout = 5 * time.Minute
	opts.EventHandler = func(event petalflow.Event) {
		switch event.Kind {
		case petalflow.EventNodeStarted:
			fmt.Printf("[%s] Starting: %s\n", time.Now().Format("15:04:05"), event.NodeID)
		case petalflow.EventNodeFinished:
			fmt.Printf("[%s] Completed: %s\n", time.Now().Format("15:04:05"), event.NodeID)
		case petalflow.EventNodeFailed:
			fmt.Printf("[%s] Failed: %s - %v\n", time.Now().Format("15:04:05"), event.NodeID, event.Error)
		}
	}

	// Run the workflow
	result, err := runtime.Run(ctx, graph, env, opts)
	if err != nil {
		fmt.Printf("\nWorkflow failed: %v\n", err)
		os.Exit(1)
	}

	// Display results
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("RESPONSE")
	fmt.Println(strings.Repeat("=", 60))

	if response := result.GetVarString("response"); response != "" {
		fmt.Println(response)
	} else {
		// Print all output variables for debugging
		fmt.Println("Output variables:")
		data, _ := json.MarshalIndent(result.Vars(), "", "  ")
		fmt.Println(string(data))
	}

	// Show summary status
	fmt.Println()
	fmt.Println(strings.Repeat("-", 60))
	if saved := result.GetVarString("summary_saved"); saved != "" {
		fmt.Println("Interaction Summary: Saved to Cortex")
	}
}
