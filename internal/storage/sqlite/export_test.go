package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/petal-labs/cortex/internal/storage"
	"github.com/petal-labs/cortex/pkg/types"
)

func TestExportAllPrimitivesWithNullFieldsAndEmbeddings(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()
	const ns = "export-ns"
	const otherNS = "other-ns"

	// 1. Conversation: AppendMessage creates thread with NULL title and NULL summary.
	msg := &types.Message{
		ID:        "msg-1",
		Namespace: ns,
		ThreadID:  "thread-1",
		Role:      "user",
		Content:   "Hello Cortex",
	}
	if err := backend.AppendMessage(ctx, msg); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	msgEmb := []float32{0.1, 0.2, 0.3, 0.4}
	if err := backend.StoreMessageEmbedding(ctx, msg.ID, msgEmb); err != nil {
		t.Fatalf("StoreMessageEmbedding: %v", err)
	}
	// Seed a row in another namespace to verify isolation.
	if err := backend.AppendMessage(ctx, &types.Message{
		ID:        "other-msg",
		Namespace: otherNS,
		ThreadID:  "other-thread",
		Role:      "user",
		Content:   "Other namespace",
	}); err != nil {
		t.Fatalf("AppendMessage other: %v", err)
	}

	// 2. Knowledge: insert collection/document/chunk with SQL NULL optional columns.
	nowUnix := time.Now().UTC().Unix()
	if _, err := backend.db.ExecContext(ctx, `
		INSERT INTO collections (id, namespace, name, description, chunk_config, created_at)
		VALUES ('col-1', ?, 'docs', NULL, NULL, ?)
	`, ns, nowUnix); err != nil {
		t.Fatalf("insert null collection: %v", err)
	}
	if _, err := backend.db.ExecContext(ctx, `
		INSERT INTO documents (id, namespace, collection_id, title, content, content_type, source, metadata, created_at, updated_at)
		VALUES ('doc-1', ?, 'col-1', NULL, 'doc body', NULL, NULL, NULL, ?, ?)
	`, ns, nowUnix, nowUnix); err != nil {
		t.Fatalf("insert null document: %v", err)
	}
	if _, err := backend.db.ExecContext(ctx, `
		INSERT INTO chunks (id, document_id, namespace, collection_id, content, chunk_index, token_count, metadata)
		VALUES ('chunk-1', 'doc-1', ?, 'col-1', 'chunk body', 0, NULL, NULL)
	`, ns); err != nil {
		t.Fatalf("insert null chunk: %v", err)
	}
	chunkEmb := []float32{0.5, 0.6, 0.7, 0.8}
	if _, err := backend.db.ExecContext(ctx, `
		INSERT INTO chunk_embeddings (chunk_id, embedding) VALUES ('chunk-1', ?)
	`, encodeVectorBinary(chunkEmb)); err != nil {
		t.Fatalf("insert chunk embedding: %v", err)
	}

	// 3. Context: persistent entry with NULL updated_by and NULL ttl_expires_at,
	// plus history entry with NULL updated_by.
	if _, err := backend.db.ExecContext(ctx, `
		INSERT INTO context_entries (namespace, run_id, key, value, version, updated_at, updated_by, ttl_expires_at)
		VALUES (?, '', 'k1', '"val1"', 1, ?, NULL, NULL)
	`, ns, nowUnix); err != nil {
		t.Fatalf("insert null context entry: %v", err)
	}
	if _, err := backend.db.ExecContext(ctx, `
		INSERT INTO context_history (namespace, run_id, key, value, version, operation, updated_at, updated_by)
		VALUES (?, NULL, 'k1', '"val1"', 1, 'set', ?, NULL)
	`, ns, nowUnix); err != nil {
		t.Fatalf("insert null context history: %v", err)
	}

	// 4. Entities: NULL summary/attributes/metadata, NULL mention context/snippet, NULL relationship description.
	if _, err := backend.db.ExecContext(ctx, `
		INSERT INTO entities (id, namespace, name, type, aliases, summary, attributes, metadata, mention_count, first_seen_at, last_seen_at)
		VALUES ('ent-1', ?, 'Alice', 'person', '["Al"]', NULL, NULL, NULL, 1, ?, ?)
	`, ns, nowUnix, nowUnix); err != nil {
		t.Fatalf("insert null entity 1: %v", err)
	}
	if _, err := backend.db.ExecContext(ctx, `
		INSERT INTO entities (id, namespace, name, type, aliases, summary, attributes, metadata, mention_count, first_seen_at, last_seen_at)
		VALUES ('ent-2', ?, 'Acme', 'organization', NULL, NULL, NULL, NULL, 0, ?, ?)
	`, ns, nowUnix, nowUnix); err != nil {
		t.Fatalf("insert null entity 2: %v", err)
	}
	entEmb := []float32{0.9, 0.8, 0.7, 0.6}
	if err := backend.StoreEntityEmbedding(ctx, "ent-1", entEmb); err != nil {
		t.Fatalf("StoreEntityEmbedding: %v", err)
	}
	if _, err := backend.db.ExecContext(ctx, `
		INSERT INTO entity_aliases (namespace, alias, entity_id) VALUES (?, 'alice', 'ent-1')
	`, ns); err != nil {
		t.Fatalf("insert entity alias: %v", err)
	}
	if _, err := backend.db.ExecContext(ctx, `
		INSERT INTO entity_mentions (id, entity_id, namespace, source_type, source_id, context, snippet, created_at)
		VALUES ('ment-1', 'ent-1', ?, 'conversation', 'msg-1', NULL, NULL, ?)
	`, ns, nowUnix); err != nil {
		t.Fatalf("insert null mention: %v", err)
	}
	if _, err := backend.db.ExecContext(ctx, `
		INSERT INTO entity_relationships (id, namespace, source_entity_id, target_entity_id, relation_type, description, confidence, mention_count, first_seen_at, last_seen_at)
		VALUES ('rel-1', ?, 'ent-1', 'ent-2', 'works_at', NULL, 0.9, 1, ?, ?)
	`, ns, nowUnix, nowUnix); err != nil {
		t.Fatalf("insert null relationship: %v", err)
	}

	t.Run("with IncludeEmbeddings true", func(t *testing.T) {
		opts := storage.DefaultExportOptions()
		opts.IncludeEmbeddings = true

		data, err := backend.Export(ctx, ns, opts)
		if err != nil {
			t.Fatalf("Export(IncludeEmbeddings=true) failed: %v", err)
		}

		if len(data.Threads) != 1 || data.Threads[0].ID != "thread-1" {
			t.Fatalf("expected 1 thread, got %d", len(data.Threads))
		}
		if len(data.Messages) != 1 || len(data.Messages[0].Embedding) != 4 {
			t.Fatalf("expected 1 message with 4-dim embedding, got %d messages", len(data.Messages))
		}
		if len(data.Collections) != 1 || len(data.Documents) != 1 || len(data.Chunks) != 1 {
			t.Fatalf("unexpected knowledge counts: cols=%d docs=%d chunks=%d", len(data.Collections), len(data.Documents), len(data.Chunks))
		}
		if len(data.Chunks[0].Embedding) != 4 {
			t.Errorf("expected chunk embedding length 4, got %d", len(data.Chunks[0].Embedding))
		}
		if len(data.ContextEntries) != 1 || len(data.ContextHistory) != 1 {
			t.Fatalf("unexpected context counts: entries=%d history=%d", len(data.ContextEntries), len(data.ContextHistory))
		}
		if len(data.Entities) != 2 || len(data.EntityAliases) != 1 || len(data.Mentions) != 1 || len(data.Relationships) != 1 {
			t.Fatalf("unexpected entity counts: ents=%d aliases=%d mentions=%d rels=%d",
				len(data.Entities), len(data.EntityAliases), len(data.Mentions), len(data.Relationships))
		}

		// Verify entity embedding was preserved on ent-1 and round-trips through JSON.
		var foundEnt1Emb []float32
		for _, e := range data.Entities {
			if e.ID == "ent-1" {
				foundEnt1Emb = e.Embedding
			}
		}
		if len(foundEnt1Emb) != 4 {
			t.Fatalf("expected ent-1 embedding length 4, got %d", len(foundEnt1Emb))
		}

		raw, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("json.Marshal(ExportData): %v", err)
		}
		var roundTrip storage.ExportData
		if err := json.Unmarshal(raw, &roundTrip); err != nil {
			t.Fatalf("json.Unmarshal(ExportData): %v", err)
		}
		var rtEmb []float32
		for _, e := range roundTrip.Entities {
			if e.ID == "ent-1" {
				rtEmb = e.Embedding
			}
		}
		if len(rtEmb) != 4 {
			t.Errorf("expected ent-1 embedding to survive JSON round-trip, got len %d", len(rtEmb))
		}
	})

	t.Run("with IncludeEmbeddings false", func(t *testing.T) {
		opts := storage.DefaultExportOptions()
		opts.IncludeEmbeddings = false

		data, err := backend.Export(ctx, ns, opts)
		if err != nil {
			t.Fatalf("Export(IncludeEmbeddings=false) failed: %v", err)
		}
		if len(data.Messages) != 1 || len(data.Messages[0].Embedding) != 0 {
			t.Errorf("expected message embedding omitted when IncludeEmbeddings=false")
		}
		if len(data.Chunks) != 1 || len(data.Chunks[0].Embedding) != 0 {
			t.Errorf("expected chunk embedding omitted when IncludeEmbeddings=false")
		}
		for _, e := range data.Entities {
			if len(e.Embedding) != 0 {
				t.Errorf("expected entity %s embedding omitted when IncludeEmbeddings=false", e.ID)
			}
		}
	})
}

func TestExportPropagatesJSONDecodeError(t *testing.T) {
	backend := newTestBackendWithDB(t)
	defer backend.Close()

	ctx := context.Background()
	const ns = "corrupt-ns"
	nowUnix := time.Now().UTC().Unix()

	// Insert corrupt JSON into threads.metadata.
	if _, err := backend.db.ExecContext(ctx, `
		INSERT INTO threads (id, namespace, title, summary, metadata, created_at, updated_at)
		VALUES ('bad-thread', ?, NULL, NULL, '{not-valid-json', ?, ?)
	`, ns, nowUnix, nowUnix); err != nil {
		t.Fatalf("insert corrupt thread: %v", err)
	}

	_, err := backend.Export(ctx, ns, storage.ExportOptions{IncludeConversations: true})
	if err == nil {
		t.Fatal("expected Export to return an error on corrupt JSON metadata, got nil")
	}
}
