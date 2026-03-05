package pgvector

import (
	"context"
	"fmt"
)

// runMigrations creates all required tables and indexes.
func (b *Backend) runMigrations(ctx context.Context) error {
	// Ensure pgvector extension is installed
	if _, err := b.pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		return fmt.Errorf("failed to create vector extension: %w", err)
	}

	// Run all table creation statements
	for _, stmt := range migrationStatements {
		if _, err := b.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	return nil
}

var migrationStatements = []string{
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

	`CREATE TABLE IF NOT EXISTS message_embeddings (
		message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
		embedding vector(1536)
	)`,

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

	`CREATE TABLE IF NOT EXISTS chunks (
		id TEXT PRIMARY KEY,
		document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
		collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
		namespace TEXT NOT NULL,
		content TEXT NOT NULL,
		sequence_num INTEGER NOT NULL,
		token_count INTEGER NOT NULL DEFAULT 0,
		metadata JSONB,
		embedding vector(1536),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,

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
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(namespace, key, COALESCE(run_id, ''))
	)`,

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

	`CREATE TABLE IF NOT EXISTS entity_embeddings (
		entity_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
		embedding vector(1536)
	)`,

	// HNSW index for fast similarity search
	`CREATE INDEX IF NOT EXISTS idx_entity_embeddings_hnsw
		ON entity_embeddings USING hnsw (embedding vector_cosine_ops)
		WITH (m = 16, ef_construction = 64)`,

	`CREATE TABLE IF NOT EXISTS entity_aliases (
		id SERIAL PRIMARY KEY,
		namespace TEXT NOT NULL,
		alias TEXT NOT NULL,
		entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE(namespace, LOWER(alias))
	)`,

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
}
