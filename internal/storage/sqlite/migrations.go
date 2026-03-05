package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// Migration represents a database migration.
type Migration struct {
	Version int
	Name    string
	Up      string
}

// migrations contains all database migrations in order.
var migrations = []Migration{
	{
		Version: 1,
		Name:    "initial_schema",
		Up: `
-- Metadata table to track schema version and embedding config
CREATE TABLE IF NOT EXISTS cortex_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Conversation memory tables
CREATE TABLE IF NOT EXISTS threads (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    title TEXT,
    summary TEXT,
    metadata TEXT, -- JSON
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_threads_namespace ON threads(namespace);

CREATE TABLE IF NOT EXISTS messages (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    thread_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    metadata TEXT, -- JSON
    summarized INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    FOREIGN KEY (thread_id) REFERENCES threads(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_messages_thread ON messages(namespace, thread_id, created_at);

-- Message embeddings stored in a separate table
-- We'll use a simple BLOB for now; vec0 integration will enhance this
CREATE TABLE IF NOT EXISTS message_embeddings (
    message_id TEXT PRIMARY KEY,
    embedding BLOB NOT NULL,
    FOREIGN KEY (message_id) REFERENCES messages(id) ON DELETE CASCADE
);

-- Knowledge store tables
CREATE TABLE IF NOT EXISTS collections (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    chunk_config TEXT, -- JSON
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    UNIQUE(namespace, name)
);
CREATE INDEX IF NOT EXISTS idx_collections_namespace ON collections(namespace);

CREATE TABLE IF NOT EXISTS documents (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    collection_id TEXT NOT NULL,
    title TEXT,
    content TEXT NOT NULL,
    content_type TEXT DEFAULT 'text',
    source TEXT,
    metadata TEXT, -- JSON
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    FOREIGN KEY (collection_id) REFERENCES collections(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_documents_collection ON documents(namespace, collection_id);

CREATE TABLE IF NOT EXISTS chunks (
    id TEXT PRIMARY KEY,
    document_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    collection_id TEXT NOT NULL,
    content TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    token_count INTEGER,
    metadata TEXT, -- JSON
    FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_chunks_document ON chunks(document_id, chunk_index);
CREATE INDEX IF NOT EXISTS idx_chunks_collection ON chunks(namespace, collection_id);

-- Chunk embeddings stored in a separate table
CREATE TABLE IF NOT EXISTS chunk_embeddings (
    chunk_id TEXT PRIMARY KEY,
    embedding BLOB NOT NULL,
    FOREIGN KEY (chunk_id) REFERENCES chunks(id) ON DELETE CASCADE
);

-- Workflow context tables
-- run_id uses empty string '' for persistent (cross-run) context, non-empty for run-scoped
CREATE TABLE IF NOT EXISTS context_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT '', -- Empty string for persistent context
    key TEXT NOT NULL,
    value TEXT NOT NULL, -- JSON
    version INTEGER NOT NULL DEFAULT 1,
    updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    updated_by TEXT,
    ttl_expires_at INTEGER, -- Unix timestamp, NULL for no expiration
    UNIQUE(namespace, run_id, key)
);
CREATE INDEX IF NOT EXISTS idx_context_namespace_key ON context_entries(namespace, key);
CREATE INDEX IF NOT EXISTS idx_context_ttl ON context_entries(ttl_expires_at) WHERE ttl_expires_at IS NOT NULL;

-- Context history (append-only log)
CREATE TABLE IF NOT EXISTS context_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace TEXT NOT NULL,
    run_id TEXT,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    version INTEGER NOT NULL,
    operation TEXT NOT NULL, -- "set", "merge", "delete"
    updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    updated_by TEXT
);
CREATE INDEX IF NOT EXISTS idx_context_history_key ON context_history(namespace, key, version);

-- Entity memory tables
CREATE TABLE IF NOT EXISTS entities (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL, -- "person", "organization", "product", "location", "concept"
    aliases TEXT, -- JSON array
    summary TEXT,
    attributes TEXT, -- JSON object
    metadata TEXT, -- JSON object
    mention_count INTEGER DEFAULT 0,
    first_seen_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    last_seen_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_entities_namespace ON entities(namespace, type);
CREATE INDEX IF NOT EXISTS idx_entities_name ON entities(namespace, name);
CREATE UNIQUE INDEX IF NOT EXISTS idx_entities_unique_name ON entities(namespace, LOWER(name));

-- Entity name/alias lookup (denormalized for fast resolution)
CREATE TABLE IF NOT EXISTS entity_aliases (
    namespace TEXT NOT NULL,
    alias TEXT NOT NULL, -- Lowercased for case-insensitive lookup
    entity_id TEXT NOT NULL,
    PRIMARY KEY (namespace, alias),
    FOREIGN KEY (entity_id) REFERENCES entities(id) ON DELETE CASCADE
);

-- Entity embeddings stored in a separate table
CREATE TABLE IF NOT EXISTS entity_embeddings (
    entity_id TEXT PRIMARY KEY,
    embedding BLOB NOT NULL,
    FOREIGN KEY (entity_id) REFERENCES entities(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS entity_mentions (
    id TEXT PRIMARY KEY,
    entity_id TEXT NOT NULL,
    namespace TEXT NOT NULL,
    source_type TEXT NOT NULL, -- "conversation", "knowledge", "manual"
    source_id TEXT NOT NULL,
    context TEXT, -- Surrounding text
    snippet TEXT, -- Exact mention text
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    FOREIGN KEY (entity_id) REFERENCES entities(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_entity_mentions_entity ON entity_mentions(entity_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_entity_mentions_source ON entity_mentions(source_type, source_id);

CREATE TABLE IF NOT EXISTS entity_relationships (
    id TEXT PRIMARY KEY,
    namespace TEXT NOT NULL,
    source_entity_id TEXT NOT NULL,
    target_entity_id TEXT NOT NULL,
    relation_type TEXT NOT NULL,
    description TEXT,
    confidence REAL DEFAULT 1.0,
    mention_count INTEGER DEFAULT 1,
    first_seen_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    last_seen_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    UNIQUE(namespace, source_entity_id, target_entity_id, relation_type),
    FOREIGN KEY (source_entity_id) REFERENCES entities(id) ON DELETE CASCADE,
    FOREIGN KEY (target_entity_id) REFERENCES entities(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_entity_rels_source ON entity_relationships(source_entity_id);
CREATE INDEX IF NOT EXISTS idx_entity_rels_target ON entity_relationships(target_entity_id);

-- Entity extraction queue (async processing)
CREATE TABLE IF NOT EXISTS entity_extraction_queue (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    namespace TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_id TEXT NOT NULL,
    content TEXT NOT NULL,
    status TEXT DEFAULT 'pending', -- "pending", "processing", "completed", "failed", "dead_letter"
    attempts INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    processed_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_extraction_queue_status ON entity_extraction_queue(status, created_at);

-- Record schema version
INSERT OR REPLACE INTO cortex_metadata (key, value) VALUES ('schema_version', '1');
`,
	},
	{
		Version: 2,
		Name:    "fts5_hybrid_search",
		Up: `
-- FTS5 virtual tables for hybrid search (vector + full-text)

-- Messages full-text search
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    id UNINDEXED,
    namespace UNINDEXED,
    thread_id UNINDEXED,
    content,
    content='messages',
    content_rowid='rowid'
);

-- Trigger to keep messages_fts in sync with messages
CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, id, namespace, thread_id, content)
    VALUES (NEW.rowid, NEW.id, NEW.namespace, NEW.thread_id, NEW.content);
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, id, namespace, thread_id, content)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.namespace, OLD.thread_id, OLD.content);
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, id, namespace, thread_id, content)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.namespace, OLD.thread_id, OLD.content);
    INSERT INTO messages_fts(rowid, id, namespace, thread_id, content)
    VALUES (NEW.rowid, NEW.id, NEW.namespace, NEW.thread_id, NEW.content);
END;

-- Chunks full-text search
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    id UNINDEXED,
    namespace UNINDEXED,
    collection_id UNINDEXED,
    document_id UNINDEXED,
    content,
    content='chunks',
    content_rowid='rowid'
);

-- Trigger to keep chunks_fts in sync with chunks
CREATE TRIGGER IF NOT EXISTS chunks_fts_insert AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, id, namespace, collection_id, document_id, content)
    VALUES (NEW.rowid, NEW.id, NEW.namespace, NEW.collection_id, NEW.document_id, NEW.content);
END;

CREATE TRIGGER IF NOT EXISTS chunks_fts_delete AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, id, namespace, collection_id, document_id, content)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.namespace, OLD.collection_id, OLD.document_id, OLD.content);
END;

CREATE TRIGGER IF NOT EXISTS chunks_fts_update AFTER UPDATE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, id, namespace, collection_id, document_id, content)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.namespace, OLD.collection_id, OLD.document_id, OLD.content);
    INSERT INTO chunks_fts(rowid, id, namespace, collection_id, document_id, content)
    VALUES (NEW.rowid, NEW.id, NEW.namespace, NEW.collection_id, NEW.document_id, NEW.content);
END;

-- Entities full-text search (name + summary)
CREATE VIRTUAL TABLE IF NOT EXISTS entities_fts USING fts5(
    id UNINDEXED,
    namespace UNINDEXED,
    name,
    summary,
    content='entities',
    content_rowid='rowid'
);

-- Trigger to keep entities_fts in sync with entities
CREATE TRIGGER IF NOT EXISTS entities_fts_insert AFTER INSERT ON entities BEGIN
    INSERT INTO entities_fts(rowid, id, namespace, name, summary)
    VALUES (NEW.rowid, NEW.id, NEW.namespace, NEW.name, COALESCE(NEW.summary, ''));
END;

CREATE TRIGGER IF NOT EXISTS entities_fts_delete AFTER DELETE ON entities BEGIN
    INSERT INTO entities_fts(entities_fts, rowid, id, namespace, name, summary)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.namespace, OLD.name, COALESCE(OLD.summary, ''));
END;

CREATE TRIGGER IF NOT EXISTS entities_fts_update AFTER UPDATE ON entities BEGIN
    INSERT INTO entities_fts(entities_fts, rowid, id, namespace, name, summary)
    VALUES ('delete', OLD.rowid, OLD.id, OLD.namespace, OLD.name, COALESCE(OLD.summary, ''));
    INSERT INTO entities_fts(rowid, id, namespace, name, summary)
    VALUES (NEW.rowid, NEW.id, NEW.namespace, NEW.name, COALESCE(NEW.summary, ''));
END;

-- Populate FTS tables with existing data
INSERT INTO messages_fts(rowid, id, namespace, thread_id, content)
SELECT rowid, id, namespace, thread_id, content FROM messages;

INSERT INTO chunks_fts(rowid, id, namespace, collection_id, document_id, content)
SELECT rowid, id, namespace, collection_id, document_id, content FROM chunks;

INSERT INTO entities_fts(rowid, id, namespace, name, summary)
SELECT rowid, id, namespace, name, COALESCE(summary, '') FROM entities;

-- Update schema version
INSERT OR REPLACE INTO cortex_metadata (key, value) VALUES ('schema_version', '2');
`,
	},
}

// runMigrations executes all pending migrations.
func runMigrations(ctx context.Context, db *sql.DB) error {
	// Create metadata table if it doesn't exist
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS cortex_metadata (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create metadata table: %w", err)
	}

	// Get current schema version
	var currentVersion int
	row := db.QueryRowContext(ctx, "SELECT value FROM cortex_metadata WHERE key = 'schema_version'")
	if err := row.Scan(&currentVersion); err != nil {
		if err == sql.ErrNoRows {
			currentVersion = 0
		} else {
			return fmt.Errorf("failed to get schema version: %w", err)
		}
	}

	// Check FTS5 availability once
	fts5Available := checkFTS5Available(ctx, db)

	// Run pending migrations
	for _, m := range migrations {
		if m.Version <= currentVersion {
			continue
		}

		// Skip FTS5 migration if FTS5 is not available
		if m.Name == "fts5_hybrid_search" && !fts5Available {
			// Record that we skipped FTS5 and update version
			_, _ = db.ExecContext(ctx, "INSERT OR REPLACE INTO cortex_metadata (key, value) VALUES ('fts5_available', 'false')")
			_, _ = db.ExecContext(ctx, "INSERT OR REPLACE INTO cortex_metadata (key, value) VALUES ('schema_version', ?)", m.Version)
			continue
		}

		// Execute migration in a transaction
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin migration transaction: %w", err)
		}

		if _, err := tx.ExecContext(ctx, m.Up); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to run migration %d (%s): %w", m.Version, m.Name, err)
		}

		// Update schema version
		if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO cortex_metadata (key, value) VALUES ('schema_version', ?)", m.Version); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to update schema version: %w", err)
		}

		// Record FTS5 as available if we successfully ran the FTS5 migration
		if m.Name == "fts5_hybrid_search" {
			if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO cortex_metadata (key, value) VALUES ('fts5_available', 'true')"); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to record fts5 availability: %w", err)
			}
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration: %w", err)
		}
	}

	return nil
}

// checkFTS5Available tests if FTS5 module is available in this SQLite build.
func checkFTS5Available(ctx context.Context, db *sql.DB) bool {
	// Try to create a temporary FTS5 table to check availability
	_, err := db.ExecContext(ctx, "CREATE VIRTUAL TABLE IF NOT EXISTS _fts5_check USING fts5(test)")
	if err != nil {
		return false
	}
	// Clean up the test table
	_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS _fts5_check")
	return true
}

// IsFTS5Available checks if FTS5 was successfully initialized.
func IsFTS5Available(ctx context.Context, db *sql.DB) bool {
	var value string
	row := db.QueryRowContext(ctx, "SELECT value FROM cortex_metadata WHERE key = 'fts5_available'")
	if err := row.Scan(&value); err != nil {
		return false
	}
	return value == "true"
}

// GetSchemaVersion returns the current schema version.
func GetSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	row := db.QueryRowContext(ctx, "SELECT value FROM cortex_metadata WHERE key = 'schema_version'")
	if err := row.Scan(&version); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return version, nil
}
