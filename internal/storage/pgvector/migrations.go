package pgvector

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Migration represents a versioned database migration. Each migration is
// applied in a single transaction; Up is the ordered list of statements pgx
// executes (pgx's extended-protocol Exec runs one statement per call, unlike
// the SQLite driver which accepts multi-statement strings).
type Migration struct {
	Version int
	Name    string
	Up      []string
}

// buildMigrations returns the ordered versioned migrations, with the embedding
// vector columns sized to the configured dimension.
func buildMigrations(dimensions int) ([]Migration, error) {
	statements, err := buildMigrationStatements(dimensions)
	if err != nil {
		return nil, err
	}
	return []Migration{
		{
			Version: 1,
			Name:    "initial_schema",
			Up:      statements,
		},
		{
			Version: 2,
			Name:    "extraction_queue_backoff",
			Up: []string{
				// Track earliest eligible retry time for failed extraction
				// attempts. NULL (or a past time) means immediately
				// eligible for dequeue.
				`ALTER TABLE extraction_queue ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ`,
			},
		},
	}, nil
}

// runMigrations applies pending versioned migrations in order, recording each
// applied version in cortex_metadata.schema_version. This mirrors the SQLite
// versioned-migration runner (internal/storage/sqlite/migrations.go) so both
// backends evolve the schema consistently post-1.0.
func (b *Backend) runMigrations(ctx context.Context) error {
	migrations, err := buildMigrations(b.cfg.Embedding.Dimensions)
	if err != nil {
		return fmt.Errorf("invalid embedding dimensions: %w", err)
	}

	// Ensure the metadata table exists (same shape as SQLite for cross-backend
	// consistency).
	if _, err := b.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS cortex_metadata (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("failed to create metadata table: %w", err)
	}

	currentVersion, err := b.getSchemaVersion(ctx)
	if err != nil {
		return err
	}

	// Apply pending migrations in order, each in its own transaction.
	for _, m := range migrations {
		if m.Version <= currentVersion {
			continue
		}
		if err := b.applyMigration(ctx, m); err != nil {
			return err
		}
	}

	// CREATE TABLE IF NOT EXISTS is a no-op when a table already exists, so
	// changing embedding.dimensions on an existing database will not alter the
	// column type. Fail fast with an actionable error rather than silently
	// breaking at insert time. A future versioned migration can ALTER the
	// column type; vector dimension changes are lossy and must be deliberate.
	if err := b.verifyEmbeddingDimensions(ctx, b.cfg.Embedding.Dimensions); err != nil {
		return err
	}

	return nil
}

// applyMigration executes a single migration in a transaction and records its
// version in cortex_metadata.
func (b *Backend) applyMigration(ctx context.Context, m Migration) error {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin migration transaction: %w", err)
	}
	defer tx.Rollback(ctx) // Safe no-op after Commit.

	for _, stmt := range m.Up {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("failed to run migration %d (%s): %w", m.Version, m.Name, err)
		}
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO cortex_metadata (key, value) VALUES ('schema_version', $1)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		strconv.Itoa(m.Version),
	); err != nil {
		return fmt.Errorf("failed to update schema version: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit migration %d (%s): %w", m.Version, m.Name, err)
	}
	return nil
}

// getSchemaVersion returns the current schema version from cortex_metadata, or
// 0 if no version has been recorded yet (fresh database).
func (b *Backend) getSchemaVersion(ctx context.Context) (int, error) {
	var value string
	err := b.pool.QueryRow(ctx, "SELECT value FROM cortex_metadata WHERE key = 'schema_version'").Scan(&value)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get schema version: %w", err)
	}
	v, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid schema_version %q: %w", value, err)
	}
	return v, nil
}

// vectorDimensionRE matches the dimension declared in a vector column type as
// reported by format_type, e.g. "vector(1536)" -> 1536.
var vectorDimensionRE = regexp.MustCompile(`vector\((\d+)\)`)

// verifyEmbeddingDimensions checks that every existing embedding column has a
// vector dimension matching the configured value, guarding against
// dev-works/prod-breaks divergence when embedding.dimensions is changed on a
// database that was already migrated with a different dimension.
func (b *Backend) verifyEmbeddingDimensions(ctx context.Context, want int) error {
	const query = `
		SELECT format_type(a.atttypid, a.atttypmod) AS col_type
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE a.attname = 'embedding'
			AND c.relkind = 'r'
			AND n.nspname = current_schema()
	`
	rows, err := b.pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query embedding column types: %w", err)
	}
	defer rows.Close()

	var mismatches []string
	for rows.Next() {
		var colType string
		if err := rows.Scan(&colType); err != nil {
			return fmt.Errorf("failed to scan embedding column type: %w", err)
		}
		m := vectorDimensionRE.FindStringSubmatch(colType)
		if m == nil {
			continue
		}
		got, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if got != want {
			mismatches = append(mismatches, fmt.Sprintf(
				"existing embedding columns are vector(%d) but embedding.dimensions is %d; "+
					"run an ALTER TABLE ... ALTER COLUMN embedding TYPE vector(%d) migration "+
					"or start with a fresh database", got, want, want))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed iterating embedding column types: %w", err)
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("embedding dimension mismatch: %s", strings.Join(mismatches, "; "))
	}
	return nil
}

// buildMigrationStatements returns the ordered schema DDL with the embedding
// vector columns sized to the configured dimension. It validates that
// dimensions is positive before generating any DDL.
func buildMigrationStatements(dimensions int) ([]string, error) {
	if dimensions <= 0 {
		return nil, fmt.Errorf("embedding dimensions must be > 0, got %d", dimensions)
	}

	return []string{
		// ==========================================================================
		// Conversation Memory Tables
		// ==========================================================================
		`CREATE TABLE IF NOT EXISTS threads (
		id TEXT PRIMARY KEY,
		namespace TEXT NOT NULL,
		title TEXT,
		summary TEXT,
		metadata JSONB,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,

		`CREATE INDEX IF NOT EXISTS idx_threads_namespace ON threads(namespace)`,
		`CREATE INDEX IF NOT EXISTS idx_threads_updated ON threads(namespace, updated_at DESC)`,

		`CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
		namespace TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		metadata JSONB,
		summarized BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,

		`CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(thread_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_namespace ON messages(namespace)`,

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS message_embeddings (
		message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
		embedding vector(%d)
	)`, dimensions),

		// HNSW index for fast similarity search
		`CREATE INDEX IF NOT EXISTS idx_message_embeddings_hnsw
		ON message_embeddings USING hnsw (embedding vector_cosine_ops)
		WITH (m = 16, ef_construction = 64)`,

		// ==========================================================================
		// Knowledge Store Tables
		// ==========================================================================
		`CREATE TABLE IF NOT EXISTS collections (
		id TEXT PRIMARY KEY,
		namespace TEXT NOT NULL,
		name TEXT NOT NULL,
		description TEXT,
		chunk_strategy TEXT NOT NULL DEFAULT 'sentence',
		chunk_max_tokens INTEGER NOT NULL DEFAULT 512,
		chunk_overlap INTEGER NOT NULL DEFAULT 50,
		metadata JSONB,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,

		`CREATE INDEX IF NOT EXISTS idx_collections_namespace ON collections(namespace)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_collections_name ON collections(namespace, name)`,

		`CREATE TABLE IF NOT EXISTS documents (
		id TEXT PRIMARY KEY,
		collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
		namespace TEXT NOT NULL,
		title TEXT,
		source TEXT,
		content_type TEXT,
		content_hash TEXT,
		token_count INTEGER NOT NULL DEFAULT 0,
		metadata JSONB,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,

		`CREATE INDEX IF NOT EXISTS idx_documents_collection ON documents(collection_id)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_namespace ON documents(namespace)`,

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS chunks (
		id TEXT PRIMARY KEY,
		document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
		collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
		namespace TEXT NOT NULL,
		content TEXT NOT NULL,
		sequence_num INTEGER NOT NULL,
		token_count INTEGER NOT NULL DEFAULT 0,
		metadata JSONB,
		embedding vector(%d),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`, dimensions),

		`CREATE INDEX IF NOT EXISTS idx_chunks_document ON chunks(document_id, sequence_num)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_collection ON chunks(collection_id)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_namespace ON chunks(namespace)`,

		// HNSW index for fast similarity search
		`CREATE INDEX IF NOT EXISTS idx_chunks_embedding_hnsw
		ON chunks USING hnsw (embedding vector_cosine_ops)
		WITH (m = 16, ef_construction = 64)`,

		// GIN index for metadata filtering
		`CREATE INDEX IF NOT EXISTS idx_chunks_metadata ON chunks USING GIN (metadata)`,

		// ==========================================================================
		// Workflow Context Tables
		// ==========================================================================
		`CREATE TABLE IF NOT EXISTS context_entries (
		id SERIAL PRIMARY KEY,
		namespace TEXT NOT NULL,
		key TEXT NOT NULL,
		run_id TEXT,
		value JSONB NOT NULL,
		version BIGINT NOT NULL DEFAULT 1,
		expires_at TIMESTAMPTZ,
		updated_by TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,

		`CREATE UNIQUE INDEX IF NOT EXISTS idx_context_entries_unique
		ON context_entries (namespace, key, COALESCE(run_id, ''))`,

		`CREATE INDEX IF NOT EXISTS idx_context_namespace_key ON context_entries(namespace, key)`,
		`CREATE INDEX IF NOT EXISTS idx_context_run ON context_entries(namespace, run_id) WHERE run_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_context_expires ON context_entries(expires_at) WHERE expires_at IS NOT NULL`,

		`CREATE TABLE IF NOT EXISTS context_history (
		id SERIAL PRIMARY KEY,
		namespace TEXT NOT NULL,
		key TEXT NOT NULL,
		run_id TEXT,
		version BIGINT NOT NULL,
		value JSONB NOT NULL,
		operation TEXT NOT NULL,
		updated_by TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,

		`CREATE INDEX IF NOT EXISTS idx_context_history_key ON context_history(namespace, key, created_at DESC)`,

		// ==========================================================================
		// Entity Memory Tables
		// ==========================================================================
		`CREATE TABLE IF NOT EXISTS entities (
		id TEXT PRIMARY KEY,
		namespace TEXT NOT NULL,
		name TEXT NOT NULL,
		type TEXT NOT NULL,
		aliases TEXT[],
		summary TEXT,
		attributes JSONB,
		metadata JSONB,
		mention_count BIGINT NOT NULL DEFAULT 0,
		first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,

		`CREATE INDEX IF NOT EXISTS idx_entities_namespace ON entities(namespace)`,
		`CREATE INDEX IF NOT EXISTS idx_entities_name ON entities(namespace, LOWER(name))`,
		`CREATE INDEX IF NOT EXISTS idx_entities_type ON entities(namespace, type)`,
		`CREATE INDEX IF NOT EXISTS idx_entities_mentions ON entities(namespace, mention_count DESC)`,

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS entity_embeddings (
		entity_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
		embedding vector(%d)
	)`, dimensions),

		// HNSW index for fast similarity search
		`CREATE INDEX IF NOT EXISTS idx_entity_embeddings_hnsw
		ON entity_embeddings USING hnsw (embedding vector_cosine_ops)
		WITH (m = 16, ef_construction = 64)`,

		`CREATE TABLE IF NOT EXISTS entity_aliases (
		id SERIAL PRIMARY KEY,
		namespace TEXT NOT NULL,
		alias TEXT NOT NULL,
		entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,

		`CREATE UNIQUE INDEX IF NOT EXISTS idx_entity_aliases_unique
		ON entity_aliases (namespace, LOWER(alias))`,

		`CREATE INDEX IF NOT EXISTS idx_entity_aliases_lookup ON entity_aliases(namespace, LOWER(alias))`,

		`CREATE TABLE IF NOT EXISTS entity_mentions (
		id SERIAL PRIMARY KEY,
		entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
		namespace TEXT NOT NULL,
		source_type TEXT NOT NULL,
		source_id TEXT NOT NULL,
		context TEXT,
		confidence REAL NOT NULL DEFAULT 1.0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,

		`CREATE INDEX IF NOT EXISTS idx_mentions_entity ON entity_mentions(entity_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_mentions_source ON entity_mentions(namespace, source_type, source_id)`,

		`CREATE TABLE IF NOT EXISTS entity_relationships (
		id TEXT PRIMARY KEY,
		namespace TEXT NOT NULL,
		source_entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
		target_entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
		relation_type TEXT NOT NULL,
		description TEXT,
		confidence REAL NOT NULL DEFAULT 1.0,
		mention_count BIGINT NOT NULL DEFAULT 1,
		metadata JSONB,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(namespace, source_entity_id, target_entity_id, relation_type)
	)`,

		`CREATE INDEX IF NOT EXISTS idx_relationships_source ON entity_relationships(source_entity_id)`,
		`CREATE INDEX IF NOT EXISTS idx_relationships_target ON entity_relationships(target_entity_id)`,

		// ==========================================================================
		// Entity Extraction Queue
		// ==========================================================================
		`CREATE TABLE IF NOT EXISTS extraction_queue (
		id SERIAL PRIMARY KEY,
		namespace TEXT NOT NULL,
		source_type TEXT NOT NULL,
		source_id TEXT NOT NULL,
		content TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		retry_count INTEGER NOT NULL DEFAULT 0,
		error_message TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		processed_at TIMESTAMPTZ
	)`,

		`CREATE INDEX IF NOT EXISTS idx_extraction_queue_status ON extraction_queue(status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_extraction_queue_source ON extraction_queue(namespace, source_type, source_id)`,

		// ==========================================================================
		// Full-Text Search Support (tsvector columns and GIN indexes)
		// ==========================================================================

		// Messages FTS
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS content_tsvector tsvector
		GENERATED ALWAYS AS (to_tsvector('english', content)) STORED`,
		`CREATE INDEX IF NOT EXISTS idx_messages_fts ON messages USING GIN (content_tsvector)`,

		// Chunks FTS
		`ALTER TABLE chunks ADD COLUMN IF NOT EXISTS content_tsvector tsvector
		GENERATED ALWAYS AS (to_tsvector('english', content)) STORED`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_fts ON chunks USING GIN (content_tsvector)`,

		// Entities FTS (name + summary)
		`ALTER TABLE entities ADD COLUMN IF NOT EXISTS search_tsvector tsvector
		GENERATED ALWAYS AS (
			setweight(to_tsvector('english', COALESCE(name, '')), 'A') ||
			setweight(to_tsvector('english', COALESCE(summary, '')), 'B')
		) STORED`,
		`CREATE INDEX IF NOT EXISTS idx_entities_fts ON entities USING GIN (search_tsvector)`,
	}, nil
}
