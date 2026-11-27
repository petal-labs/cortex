package cortex_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/cortex/pkg/cortex"
	"github.com/petal-labs/cortex/pkg/types"
)

// deterministicEmbedder implements cortex.EmbeddingProvider for external SDK tests.
type deterministicEmbedder struct {
	dims int
}

func (e *deterministicEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, e.dims)
	for i := 0; i < len(text); i++ {
		vec[i%e.dims] += float32(text[i]) / 255.0
	}
	if len(text) == 0 {
		vec[0] = 1.0
	}
	return vec, nil
}

func (e *deterministicEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := e.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (e *deterministicEmbedder) Dimensions() int { return e.dims }
func (e *deterministicEmbedder) Close() error    { return nil }

// stubSummarizer implements cortex.Summarizer for external SDK tests.
type stubSummarizer struct{}

func (s *stubSummarizer) SummarizeMessages(_ context.Context, messages []*types.Message) (string, error) {
	return "Summary of conversation", nil
}

// TestPublicAPINoInternalExports inspects every non-test Go file in pkg/cortex
// and verifies that no exported function, method, type, var, or const references
// an internal/ package in its signature.
func TestPublicAPINoInternalExports(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob pkg/cortex: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected Go files in pkg/cortex")
	}

	fset := token.NewFileSet()
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}

		internalAliases := make(map[string]string)
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if !strings.Contains(importPath, "/internal/") {
				continue
			}
			var alias string
			if imp.Name != nil {
				alias = imp.Name.Name
			} else {
				parts := strings.Split(importPath, "/")
				alias = parts[len(parts)-1]
			}
			internalAliases[alias] = importPath
		}

		checkNode := func(owner string, node ast.Node) {
			if node == nil {
				return
			}
			ast.Inspect(node, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if ident, ok := sel.X.(*ast.Ident); ok {
					if impPath, leaked := internalAliases[ident.Name]; leaked {
						t.Errorf("%s: exported symbol %s references internal package %q via %s.%s",
							file, owner, impPath, ident.Name, sel.Sel.Name)
					}
				}
				return true
			})
		}

		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Name.IsExported() {
					checkNode(d.Name.Name, d.Type)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if !s.Name.IsExported() {
							continue
						}
						if st, ok := s.Type.(*ast.StructType); ok {
							for _, field := range st.Fields.List {
								// Only inspect exported struct fields
								exported := len(field.Names) == 0 // embedded
								for _, name := range field.Names {
									if name.IsExported() {
										exported = true
									}
								}
								if exported {
									checkNode(s.Name.Name, field.Type)
								}
							}
						} else {
							checkNode(s.Name.Name, s.Type)
						}
					case *ast.ValueSpec:
						for _, name := range s.Names {
							if name.IsExported() {
								checkNode(name.Name, s.Type)
							}
						}
					}
				}
			}
		}
	}
}

func TestClientLifecycleAndAllPrimitives(t *testing.T) {
	ctx := context.Background()
	ns := "sdk-test"

	client, err := cortex.Open(ctx,
		cortex.WithSQLite(t.TempDir()),
		cortex.WithEmbeddingProvider(&deterministicEmbedder{dims: 16}),
		cortex.WithEmbeddingCacheSize(128),
		cortex.WithSummarizer(&stubSummarizer{}),
		cortex.WithSemanticSearch(true),
		cortex.WithAutoSummarizeThreshold(0),
	)
	if err != nil {
		t.Fatalf("cortex.Open: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("client.Close: %v", err)
		}
		// Second Close must be idempotent.
		if err := client.Close(); err != nil {
			t.Errorf("second client.Close: %v", err)
		}
	}()

	if err := client.Health(ctx); err != nil {
		t.Fatalf("client.Health: %v", err)
	}

	// 1. Conversation Memory
	t.Run("Conversation", func(t *testing.T) {
		conv := client.Conversation()
		const threadID = "sdk-thread-1"

		for i := 0; i < 4; i++ {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			msg, err := conv.Append(ctx, ns, threadID, role, "Discussing vector databases and Go SDK design", nil)
			if err != nil {
				t.Fatalf("Conversation.Append %d: %v", i, err)
			}
			if msg.ID == "" {
				t.Fatal("expected message ID")
			}
		}

		hist, err := conv.History(ctx, ns, threadID, &cortex.HistoryOpts{LastN: 10})
		if err != nil {
			t.Fatalf("Conversation.History: %v", err)
		}
		if len(hist.Messages) != 4 {
			t.Fatalf("expected 4 messages, got %d", len(hist.Messages))
		}

		searchRes, err := conv.Search(ctx, ns, "vector databases", &cortex.ConversationSearchOpts{
			TopK:       5,
			SearchMode: cortex.SearchModeVector,
		})
		if err != nil {
			t.Fatalf("Conversation.Search: %v", err)
		}
		if len(searchRes.Results) == 0 {
			t.Error("expected conversation search results")
		}

		sumRes, err := conv.Summarize(ctx, ns, threadID, &cortex.SummarizeOpts{KeepRecent: 2})
		if err != nil {
			t.Fatalf("Conversation.Summarize: %v", err)
		}
		if sumRes.Summary != "Summary of conversation" {
			t.Errorf("unexpected summary: %q", sumRes.Summary)
		}

		threads, _, err := conv.ListThreads(ctx, ns, "", 10)
		if err != nil {
			t.Fatalf("Conversation.ListThreads: %v", err)
		}
		if len(threads) != 1 {
			t.Errorf("expected 1 thread, got %d", len(threads))
		}

		if err := conv.Clear(ctx, ns, threadID); err != nil {
			t.Fatalf("Conversation.Clear: %v", err)
		}
		if _, err := conv.GetThread(ctx, ns, threadID); !errors.Is(err, cortex.ErrNotFound) {
			t.Errorf("expected ErrNotFound after Clear, got %v", err)
		}
	})

	// 2. Knowledge Store
	t.Run("Knowledge", func(t *testing.T) {
		know := client.Knowledge()

		col, err := know.CreateCollection(ctx, ns, cortex.CreateCollectionOpts{
			Name:        "sdk-docs",
			Description: "SDK documentation",
		})
		if err != nil {
			t.Fatalf("CreateCollection: %v", err)
		}

		ingRes, err := know.Ingest(ctx, ns, col.ID, "Cortex provides persistent memory for AI agents via Go SDK and MCP.", &cortex.IngestOpts{
			Title:  "Overview",
			Source: "docs/overview.md",
		})
		if err != nil {
			t.Fatalf("Ingest: %v", err)
		}
		if ingRes.ChunksCreated == 0 {
			t.Fatal("expected chunks created")
		}

		bulkRes, err := know.BulkIngest(ctx, ns, col.ID, []cortex.BulkIngestDocument{
			{Title: "Guide 1", Content: "Knowledge store supports vector, text, hybrid, and graph search."},
			{Title: "Guide 2", Content: "Workflow context provides optimistic locking and TTL."},
		}, nil)
		if err != nil {
			t.Fatalf("BulkIngest: %v", err)
		}
		if bulkRes.Succeeded != 2 {
			t.Errorf("expected 2 bulk docs succeeded, got %d", bulkRes.Succeeded)
		}

		doc, err := know.GetDocument(ctx, ns, ingRes.DocumentID)
		if err != nil {
			t.Fatalf("GetDocument: %v", err)
		}
		if doc.Title != "Overview" {
			t.Errorf("expected title Overview, got %q", doc.Title)
		}

		searchRes, err := know.Search(ctx, ns, "persistent memory", &cortex.KnowledgeSearchOpts{
			CollectionID: &col.ID,
			TopK:         5,
			SearchMode:   cortex.SearchModeHybrid,
		})
		if err != nil {
			t.Fatalf("Knowledge.Search: %v", err)
		}
		if len(searchRes.Results) == 0 {
			t.Error("expected knowledge search results")
		}

		stats, err := know.CollectionStats(ctx, ns, col.ID)
		if err != nil {
			t.Fatalf("CollectionStats: %v", err)
		}
		if stats.DocumentCount != 3 {
			t.Errorf("expected 3 documents, got %d", stats.DocumentCount)
		}

		cols, _, err := know.ListCollections(ctx, ns, "", 10)
		if err != nil || len(cols) != 1 {
			t.Fatalf("ListCollections: got %d, err=%v", len(cols), err)
		}

		if err := know.DeleteDocument(ctx, ns, ingRes.DocumentID); err != nil {
			t.Fatalf("DeleteDocument: %v", err)
		}
		if err := know.DeleteCollection(ctx, ns, col.ID); err != nil {
			t.Fatalf("DeleteCollection: %v", err)
		}
	})

	// 3. Workflow Context
	t.Run("Context", func(t *testing.T) {
		ctxClient := client.Context()

		setRes, err := ctxClient.Set(ctx, ns, "workflow.state", map[string]any{"step": 1}, &cortex.ContextSetOpts{
			TTL:       time.Hour,
			UpdatedBy: "sdk-test",
		})
		if err != nil {
			t.Fatalf("Context.Set: %v", err)
		}
		if setRes.Version != 1 {
			t.Errorf("expected version 1, got %d", setRes.Version)
		}

		getRes, err := ctxClient.Get(ctx, ns, "workflow.state", nil)
		if err != nil || !getRes.Exists {
			t.Fatalf("Context.Get: exists=%v err=%v", getRes.Exists, err)
		}

		mergeRes, err := ctxClient.Merge(ctx, ns, "workflow.state", map[string]any{"status": "running"}, &cortex.ContextMergeOpts{
			Strategy: types.MergeStrategyDeepMerge,
		})
		if err != nil {
			t.Fatalf("Context.Merge: %v", err)
		}
		if mergeRes.Version != 2 {
			t.Errorf("expected version 2 after merge, got %d", mergeRes.Version)
		}

		listRes, err := ctxClient.List(ctx, ns, nil)
		if err != nil || listRes.Count != 1 {
			t.Fatalf("Context.List: count=%d err=%v", listRes.Count, err)
		}

		histRes, err := ctxClient.History(ctx, ns, "workflow.state", nil)
		if err != nil || len(histRes.History) != 2 {
			t.Fatalf("Context.History: len=%d err=%v", len(histRes.History), err)
		}

		runID := "run-42"
		if _, err := ctxClient.Set(ctx, ns, "temp.step", "ok", &cortex.ContextSetOpts{RunID: &runID}); err != nil {
			t.Fatalf("Context.Set run-scoped: %v", err)
		}
		if err := ctxClient.CleanupRun(ctx, ns, runID); err != nil {
			t.Fatalf("Context.CleanupRun: %v", err)
		}

		if err := ctxClient.Delete(ctx, ns, "workflow.state", nil); err != nil {
			t.Fatalf("Context.Delete: %v", err)
		}
	})

	// 4. Entity Memory
	t.Run("Entity", func(t *testing.T) {
		entClient := client.Entity()

		alice, err := entClient.Create(ctx, ns, "Alice", types.EntityTypePerson, &cortex.EntityCreateOpts{
			Aliases: []string{"Al"},
			Summary: "Lead architect working on Cortex",
		})
		if err != nil || !alice.Created {
			t.Fatalf("Entity.Create Alice: created=%v err=%v", alice != nil && alice.Created, err)
		}
		acme, err := entClient.Create(ctx, ns, "Acme Corp", types.EntityTypeOrganization, &cortex.EntityCreateOpts{
			Summary: "Software engineering company",
		})
		if err != nil {
			t.Fatalf("Entity.Create Acme: %v", err)
		}
		dup, err := entClient.Create(ctx, ns, "Alice A.", types.EntityTypePerson, nil)
		if err != nil {
			t.Fatalf("Entity.Create duplicate: %v", err)
		}

		if err := entClient.AddAlias(ctx, ns, alice.Entity.ID, "Alice Dev"); err != nil {
			t.Fatalf("Entity.AddAlias: %v", err)
		}
		if _, err := entClient.AddRelationship(ctx, ns, alice.Entity.ID, acme.Entity.ID, "works_at", &cortex.RelationshipOpts{
			Description: "Leads architecture",
			Confidence:  0.95,
		}); err != nil {
			t.Fatalf("Entity.AddRelationship: %v", err)
		}

		rels, err := entClient.GetRelationships(ctx, ns, alice.Entity.ID, &cortex.GetRelationshipsOpts{
			Direction: types.RelationshipDirectionOutgoing,
		})
		if err != nil || len(rels) != 1 {
			t.Fatalf("Entity.GetRelationships: len=%d err=%v", len(rels), err)
		}

		qRes, err := entClient.Query(ctx, ns, "Alice Dev", 10)
		if err != nil || qRes.Entity.ID != alice.Entity.ID {
			t.Fatalf("Entity.Query: err=%v", err)
		}

		searchRes, err := entClient.Search(ctx, ns, "architect", &cortex.EntitySearchOpts{
			TopK:       5,
			SearchMode: cortex.SearchModeVector,
		})
		if err != nil || len(searchRes.Results) == 0 {
			t.Fatalf("Entity.Search: len=%d err=%v", len(searchRes.Results), err)
		}

		newSummary := "Principal architect working on Cortex"
		updated, err := entClient.Update(ctx, ns, alice.Entity.ID, cortex.EntityUpdateOpts{Summary: &newSummary})
		if err != nil || updated.Summary != newSummary {
			t.Fatalf("Entity.Update: err=%v", err)
		}

		listRes, err := entClient.List(ctx, ns, nil)
		if err != nil || listRes.Count != 3 {
			t.Fatalf("Entity.List: count=%d err=%v", listRes.Count, err)
		}

		mergeRes, err := entClient.Merge(ctx, ns, dup.Entity.ID, alice.Entity.ID)
		if err != nil || mergeRes.KeptEntity.ID != alice.Entity.ID {
			t.Fatalf("Entity.Merge: err=%v", err)
		}

		if err := entClient.Delete(ctx, ns, acme.Entity.ID); err != nil {
			t.Fatalf("Entity.Delete: %v", err)
		}
		if _, err := entClient.Get(ctx, ns, acme.Entity.ID); !errors.Is(err, cortex.ErrNotFound) {
			t.Errorf("expected ErrNotFound after Delete, got %v", err)
		}
	})
}
