// Example: PetalFlow Graph Workflow with Cortex
//
// This example demonstrates how to build a workflow graph programmatically
// using PetalFlow's graph builder API with Cortex as the knowledge backend.
//
// The workflow implements a knowledge-enriched Q&A pattern:
// 1. Search Cortex for relevant context
// 2. Prepare the context for the LLM
// 3. Generate an answer using the retrieved context
// 4. Optionally save the Q&A to Cortex for future reference
//
// Prerequisites:
// - Cortex installed and configured
// - Ollama running locally with llama3.2: ollama pull llama3.2
// - Documents ingested into Cortex knowledge store
//
// Run: go run main.go "Your question here"
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/petal-labs/cortex/internal/config"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/internal/storage/sqlite"
	"github.com/petal-labs/cortex/pkg/types"
	"github.com/petal-labs/iris/providers/ollama"
	"github.com/petal-labs/petalflow"
	"github.com/petal-labs/petalflow/irisadapter"
)

// CortexKnowledgeStore wraps the Cortex knowledge engine.
type CortexKnowledgeStore struct {
	engine    *knowledge.Engine
	namespace string
}

// NewCortexKnowledgeStore creates a new Cortex-backed knowledge store.
func NewCortexKnowledgeStore(engine *knowledge.Engine, namespace string) *CortexKnowledgeStore {
	return &CortexKnowledgeStore{
		engine:    engine,
		namespace: namespace,
	}
}

// Search retrieves relevant documents from Cortex.
func (s *CortexKnowledgeStore) Search(ctx context.Context, query string, topK int) ([]*types.ChunkResult, error) {
	result, err := s.engine.Search(ctx, s.namespace, query, &knowledge.SearchOpts{
		TopK:       topK,
		SearchMode: knowledge.SearchModeHybrid,
	})
	if err != nil {
		return nil, err
	}
	return result.Results, nil
}

func main() {
	ctx := context.Background()

	// Get query from command line
	query := "What is Cortex and what are its main features?"
	if len(os.Args) > 1 {
		query = strings.Join(os.Args[1:], " ")
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
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		fmt.Printf("Failed to run migrations: %v\n", err)
		os.Exit(1)
	}

	// Create knowledge engine
	engine, err := knowledge.NewEngine(store, nil, &cfg.Knowledge)
	if err != nil {
		fmt.Printf("Failed to create knowledge engine: %v\n", err)
		os.Exit(1)
	}

	cortexStore := NewCortexKnowledgeStore(engine, "default")

	// Create LLM client
	provider := ollama.New(ollama.WithBaseURL("http://localhost:11434"))
	client := irisadapter.NewProviderAdapter(provider)

	// Build the workflow graph programmatically
	graph := buildKnowledgeQAGraph(client, cortexStore)

	// Create input envelope
	env := petalflow.NewEnvelope().WithVar("query", query)

	fmt.Printf("Question: %s\n", query)
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()

	// Create runtime with event handler
	runtime := petalflow.NewRuntime()
	opts := petalflow.DefaultRunOptions()
	opts.Timeout = 3 * time.Minute
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
	fmt.Println("ANSWER")
	fmt.Println(strings.Repeat("=", 60))

	if answer := result.GetVarString("answer"); answer != "" {
		fmt.Println(answer)
	} else {
		fmt.Println("No answer generated.")
	}

	// Show sources if available
	if sources := result.GetVarString("sources"); sources != "" {
		fmt.Println()
		fmt.Println("Sources:")
		fmt.Println(sources)
	}
}

// buildKnowledgeQAGraph constructs the Q&A workflow graph.
//
// Graph structure:
//
//	┌──────────────┐     ┌──────────────┐     ┌──────────────┐
//	│   Search     │────▶│   Prepare    │────▶│   Generate   │
//	│   Cortex     │     │   Context    │     │   Answer     │
//	└──────────────┘     └──────────────┘     └──────────────┘
func buildKnowledgeQAGraph(client petalflow.LLMClient, store *CortexKnowledgeStore) petalflow.Graph {
	// Step 1: Search node - retrieves relevant context from Cortex
	searchNode := petalflow.NewFuncNode("search", func(ctx context.Context, env *petalflow.Envelope) (*petalflow.Envelope, error) {
		query := env.GetVarString("query")

		results, err := store.Search(ctx, query, 5)
		if err != nil {
			// Continue even if search fails - we'll just have less context
			fmt.Printf("Warning: Search failed: %v\n", err)
			env.SetVar("context_chunks", []map[string]any{})
			env.SetVar("has_context", false)
			return env, nil
		}

		// Convert results to serializable format
		chunks := make([]map[string]any, 0, len(results))
		sources := make([]string, 0, len(results))

		for _, r := range results {
			chunks = append(chunks, map[string]any{
				"content":  r.Chunk.Content,
				"score":    r.Score,
				"document": r.DocumentTitle,
				"source":   r.Source,
			})
			if r.DocumentTitle != "" {
				sources = append(sources, fmt.Sprintf("- %s", r.DocumentTitle))
			}
		}

		env.SetVar("context_chunks", chunks)
		env.SetVar("has_context", len(chunks) > 0)
		env.SetVar("sources", strings.Join(sources, "\n"))

		fmt.Printf("  Found %d relevant chunks\n", len(chunks))
		return env, nil
	})

	// Step 2: Prepare context node - formats the retrieved chunks for the LLM
	prepareNode := petalflow.NewTransformNode("prepare", petalflow.TransformNodeConfig{
		Transform: petalflow.TransformTemplate,
		Template: `{{if .has_context}}Based on the following context from the knowledge base:

{{range $i, $chunk := .context_chunks}}
[Source {{$i | printf "%d"}}] {{index $chunk "content"}}

{{end}}
Answer this question: {{.query}}

Provide a comprehensive answer based on the context above. If the context doesn't fully answer the question, acknowledge what's missing.{{else}}Answer this question based on your knowledge: {{.query}}

Note: No relevant context was found in the knowledge base for this query.{{end}}`,
		OutputVar: "prompt",
	})

	// Step 3: Generate answer node - uses LLM to generate the response
	generateNode := petalflow.NewLLMNode("generate", client, petalflow.LLMNodeConfig{
		Model: "llama3.2",
		System: `You are a helpful assistant that answers questions based on provided context.
When context is available, base your answers on it and cite relevant sources.
When context is limited, acknowledge what you don't know.
Be concise but thorough.`,
		PromptTemplate: "{{.prompt}}",
		OutputKey:      "answer",
		Timeout:        2 * time.Minute,
		RecordMessages: true,
	})

	// Build the graph using the fluent builder API
	builder := petalflow.NewGraphBuilder("knowledge-qa")
	builder.AddNode(searchNode)
	builder.Edge(prepareNode)
	builder.Edge(generateNode)

	graph, err := builder.Build()
	if err != nil {
		panic(fmt.Sprintf("Failed to build graph: %v", err))
	}

	return graph
}
