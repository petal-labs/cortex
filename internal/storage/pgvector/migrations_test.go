package pgvector

import (
	"strconv"
	"strings"
	"testing"
)

func TestBuildMigrationStatements_InjectsDimensions(t *testing.T) {
	cases := []int{768, 1536, 3072}

	for _, dims := range cases {
		t.Run("", func(t *testing.T) {
			statements, err := buildMigrationStatements(dims)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if len(statements) == 0 {
				t.Fatal("expected non-empty migration statements")
			}

			want := "vector(" + strconv.Itoa(dims) + ")"
			count := 0
			for _, stmt := range statements {
				if strings.Contains(stmt, want) {
					count++
				}
				if strings.Contains(stmt, "vector(1536)") && dims != 1536 {
					t.Errorf("found stale hardcoded vector(1536) for dims=%d in statement:\n%s", dims, stmt)
				}
			}
			if count != 3 {
				t.Errorf("expected 3 embedding columns with %s, got %d", want, count)
			}
		})
	}
}

func TestBuildMigrationStatements_IncludesAllEmbeddingTables(t *testing.T) {
	statements, err := buildMigrationStatements(1536)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tables := []string{
		"message_embeddings",
		"chunks",
		"entity_embeddings",
	}
	joined := strings.Join(statements, "\n")
	for _, table := range tables {
		if !strings.Contains(joined, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("expected DDL to create table %q", table)
		}
	}
}

func TestBuildMigrationStatements_InvalidDimension(t *testing.T) {
	cases := []int{0, -1, -1536}

	for _, dims := range cases {
		t.Run(strconv.Itoa(dims), func(t *testing.T) {
			statements, err := buildMigrationStatements(dims)
			if err == nil {
				t.Fatalf("expected error for dimensions=%d, got nil", dims)
			}
			if statements != nil {
				t.Errorf("expected nil statements on error, got %d statements", len(statements))
			}
			if !strings.Contains(err.Error(), "must be > 0") {
				t.Errorf("expected error to mention must be > 0, got: %v", err)
			}
		})
	}
}

func TestBuildMigrationStatements_PreservesHNSWIndexes(t *testing.T) {
	statements, err := buildMigrationStatements(768)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	indexes := []string{
		"idx_message_embeddings_hnsw",
		"idx_chunks_embedding_hnsw",
		"idx_entity_embeddings_hnsw",
	}
	joined := strings.Join(statements, "\n")
	for _, idx := range indexes {
		if !strings.Contains(joined, idx) {
			t.Errorf("expected HNSW index %q in DDL", idx)
		}
	}
	if !strings.Contains(joined, "vector_cosine_ops") {
		t.Error("expected vector_cosine_ops in HNSW index definitions")
	}
}

func TestBuildMigrations_InitialSchema(t *testing.T) {
	migrations, err := buildMigrations(1536)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(migrations) != 2 {
		t.Fatalf("expected 2 migrations, got %d", len(migrations))
	}
	m := migrations[0]
	if m.Version != 1 {
		t.Errorf("expected version 1, got %d", m.Version)
	}
	if m.Name != "initial_schema" {
		t.Errorf("expected name initial_schema, got %q", m.Name)
	}
	if len(m.Up) == 0 {
		t.Error("expected non-empty Up statements")
	}

	want, err := buildMigrationStatements(1536)
	if err != nil {
		t.Fatalf("expected no error from buildMigrationStatements, got %v", err)
	}
	if len(m.Up) != len(want) {
		t.Errorf("expected %d statements, got %d", len(want), len(m.Up))
	}
	for i, stmt := range m.Up {
		if stmt != want[i] {
			t.Errorf("statement %d differs from buildMigrationStatements output", i)
		}
	}

	backoff := migrations[1]
	if backoff.Version != 2 {
		t.Errorf("expected version 2, got %d", backoff.Version)
	}
	if backoff.Name != "extraction_queue_backoff" {
		t.Errorf("expected name extraction_queue_backoff, got %q", backoff.Name)
	}
	if len(backoff.Up) == 0 {
		t.Error("expected non-empty Up statements for backoff migration")
	}
}

func TestBuildMigrations_InvalidDimension(t *testing.T) {
	if _, err := buildMigrations(0); err == nil {
		t.Error("expected error for dimensions=0, got nil")
	}
	if _, err := buildMigrations(-1); err == nil {
		t.Error("expected error for dimensions=-1, got nil")
	}
}

func TestBuildMigrations_VersionOrdering(t *testing.T) {
	migrations, err := buildMigrations(768)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	for i := 1; i < len(migrations); i++ {
		if migrations[i].Version <= migrations[i-1].Version {
			t.Errorf("migrations not ordered: %d after %d", migrations[i].Version, migrations[i-1].Version)
		}
	}
}
