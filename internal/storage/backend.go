package storage

import (
	"context"
	"errors"

	"github.com/petal-labs/cortex/pkg/types"
)

// Common errors returned by storage backends.
var (
	ErrNotFound        = errors.New("not found")
	ErrAlreadyExists   = errors.New("already exists")
	ErrVersionConflict = errors.New("version conflict")
)

// Backend defines the interface for all storage operations.
// Implementations must be safe for concurrent use.
type Backend interface {
	// Conversation memory operations
	ConversationStorage

	// Knowledge store operations
	KnowledgeStorage

	// Workflow context operations
	ContextStorage

	// Entity memory operations
	EntityStorage

	// Lifecycle operations
	Lifecycle
}

// ConversationStorage defines operations for conversation memory.
type ConversationStorage interface {
	// AppendMessage adds a message to a thread, creating the thread if it doesn't exist.
	AppendMessage(ctx context.Context, msg *types.Message) error

	// GetMessages retrieves messages from a thread, ordered by creation time.
	// limit specifies the maximum number of messages to return (most recent first).
	GetMessages(ctx context.Context, namespace, threadID string, limit int, cursor string) ([]*types.Message, string, error)

	// SearchMessages performs semantic search across messages in a namespace.
	SearchMessages(ctx context.Context, namespace string, embedding []float32, opts MessageSearchOpts) ([]*types.MessageResult, error)

	// ListThreads returns all threads in a namespace.
	ListThreads(ctx context.Context, namespace string, cursor string, limit int) ([]*types.Thread, string, error)

	// GetThread retrieves a single thread by ID.
	GetThread(ctx context.Context, namespace, threadID string) (*types.Thread, error)

	// UpdateThread updates a thread's metadata (title, summary).
	UpdateThread(ctx context.Context, thread *types.Thread) error

	// DeleteThread removes a thread and all its messages.
	DeleteThread(ctx context.Context, namespace, threadID string) error

	// StoreMessageEmbedding stores the embedding for a message.
	StoreMessageEmbedding(ctx context.Context, messageID string, embedding []float32) error

	// MarkMessagesSummarized marks messages as having been included in a summary.
	MarkMessagesSummarized(ctx context.Context, namespace, threadID string, beforeTime int64) error
}

// MessageSearchOpts configures message search behavior.
type MessageSearchOpts struct {
	TopK     int
	MinScore float64
	ThreadID *string // Optional: limit to a specific thread
}

// KnowledgeStorage defines operations for the knowledge store.
type KnowledgeStorage interface {
	// InsertDocument adds a new document to the store.
	InsertDocument(ctx context.Context, doc *types.Document) error

	// InsertChunks adds chunks for a document. Embeddings should already be populated.
	InsertChunks(ctx context.Context, chunks []*types.Chunk) error

	// SearchChunks performs semantic search across chunks in a namespace.
	SearchChunks(ctx context.Context, namespace string, embedding []float32, opts ChunkSearchOpts) ([]*types.ChunkResult, error)

	// GetDocument retrieves a document by ID.
	GetDocument(ctx context.Context, namespace, docID string) (*types.Document, error)

	// DeleteDocument removes a document and all its chunks.
	DeleteDocument(ctx context.Context, namespace, docID string) error

	// GetAdjacentChunks retrieves chunks adjacent to a given chunk for context.
	// window specifies how many chunks before and after to retrieve.
	GetAdjacentChunks(ctx context.Context, chunkID string, window int) ([]*types.Chunk, error)

	// ListCollections returns all collections in a namespace.
	ListCollections(ctx context.Context, namespace string, cursor string, limit int) ([]*types.Collection, string, error)

	// GetCollection retrieves a collection by ID.
	GetCollection(ctx context.Context, namespace, collectionID string) (*types.Collection, error)

	// CreateCollection creates a new collection.
	CreateCollection(ctx context.Context, col *types.Collection) error

	// DeleteCollection removes a collection and all its documents.
	DeleteCollection(ctx context.Context, namespace, collectionID string) error

	// CollectionStats returns statistics for a collection.
	CollectionStats(ctx context.Context, namespace, collectionID string) (*types.CollectionStats, error)
}

// ChunkSearchOpts configures chunk search behavior.
type ChunkSearchOpts struct {
	TopK         int
	MinScore     float64
	CollectionID *string           // Optional: limit to a specific collection
	Filters      map[string]string // Metadata filters
}

// ContextStorage defines operations for workflow context.
type ContextStorage interface {
	// GetContext retrieves a context entry by key.
	// runID is optional: nil means persistent (cross-run) context.
	GetContext(ctx context.Context, namespace, key string, runID *string) (*types.ContextEntry, error)

	// SetContext stores a context entry, incrementing the version.
	// If expectedVersion is provided, the operation fails if the current version doesn't match.
	SetContext(ctx context.Context, entry *types.ContextEntry, expectedVersion *int64) error

	// ListContextKeys returns all keys in a namespace, optionally filtered by prefix.
	ListContextKeys(ctx context.Context, namespace string, prefix *string, runID *string, cursor string, limit int) ([]string, string, error)

	// DeleteContext removes a context entry.
	DeleteContext(ctx context.Context, namespace, key string, runID *string) error

	// GetContextHistory returns the version history for a context key.
	GetContextHistory(ctx context.Context, namespace, key string, runID *string, cursor string, limit int) ([]*types.ContextHistoryEntry, string, error)

	// CleanupExpiredContext removes entries past their TTL expiration.
	// Returns the number of entries deleted.
	CleanupExpiredContext(ctx context.Context) (int64, error)

	// CleanupRunContext removes all context entries for a specific run.
	CleanupRunContext(ctx context.Context, namespace, runID string) error
}

// EntityStorage defines operations for entity memory.
type EntityStorage interface {
	// UpsertEntity creates or updates an entity.
	UpsertEntity(ctx context.Context, entity *types.Entity) error

	// GetEntityByID retrieves an entity by its ID.
	GetEntityByID(ctx context.Context, namespace, entityID string) (*types.Entity, error)

	// GetEntityByName retrieves an entity by its canonical name (case-insensitive).
	GetEntityByName(ctx context.Context, namespace, name string) (*types.Entity, error)

	// ResolveAlias looks up an entity by an alias.
	ResolveAlias(ctx context.Context, namespace, alias string) (*types.Entity, error)

	// SearchEntities performs semantic search across entity summaries.
	SearchEntities(ctx context.Context, namespace string, embedding []float32, opts EntitySearchOpts) ([]*types.EntityResult, error)

	// ListEntities returns entities in a namespace with optional filtering.
	ListEntities(ctx context.Context, namespace string, opts EntityListOpts) ([]*types.Entity, string, error)

	// DeleteEntity removes an entity and all its mentions/relationships.
	DeleteEntity(ctx context.Context, namespace, entityID string) error

	// MergeEntities combines two entities, moving all data from source to target.
	// The source entity is deleted after merging.
	MergeEntities(ctx context.Context, namespace, sourceID, targetID string) error

	// InsertMention records a mention of an entity in source content.
	InsertMention(ctx context.Context, mention *types.EntityMention) error

	// GetMentions retrieves recent mentions of an entity.
	GetMentions(ctx context.Context, entityID string, limit int) ([]*types.EntityMention, error)

	// UpsertRelationship creates or updates a relationship between entities.
	UpsertRelationship(ctx context.Context, rel *types.EntityRelationship) error

	// GetRelationships retrieves relationships for an entity.
	GetRelationships(ctx context.Context, namespace, entityID string, opts RelationshipOpts) ([]*types.EntityRelationship, error)

	// RegisterAlias adds an alias for an entity.
	RegisterAlias(ctx context.Context, namespace, alias, entityID string) error

	// StoreEntityEmbedding stores the embedding for an entity's summary.
	StoreEntityEmbedding(ctx context.Context, entityID string, embedding []float32) error

	// EnqueueExtraction adds an item to the entity extraction queue.
	EnqueueExtraction(ctx context.Context, item *types.ExtractionQueueItem) error

	// DequeueExtraction retrieves pending items from the extraction queue.
	// Items are marked as "processing" to prevent duplicate processing.
	DequeueExtraction(ctx context.Context, batchSize int) ([]*types.ExtractionQueueItem, error)

	// CompleteExtraction marks an extraction queue item as completed or failed.
	CompleteExtraction(ctx context.Context, itemID int64, status string) error

	// GetExtractionQueueStats returns statistics about the extraction queue.
	GetExtractionQueueStats(ctx context.Context) (*ExtractionQueueStats, error)
}

// EntitySearchOpts configures entity search behavior.
type EntitySearchOpts struct {
	TopK       int
	MinScore   float64
	EntityType *types.EntityType
}

// EntityListOpts configures entity list behavior.
type EntityListOpts struct {
	EntityType *types.EntityType
	SortBy     types.EntitySortBy
	Limit      int
	Cursor     string
}

// RelationshipOpts configures relationship queries.
type RelationshipOpts struct {
	RelationType *string
	Direction    types.RelationshipDirection
}

// ExtractionQueueStats holds statistics about the extraction queue.
type ExtractionQueueStats struct {
	PendingCount    int64
	ProcessingCount int64
	FailedCount     int64
	DeadLetterCount int64
}

// Lifecycle defines operations for backend lifecycle management.
type Lifecycle interface {
	// Migrate runs any necessary schema migrations.
	Migrate(ctx context.Context) error

	// Close releases any resources held by the backend.
	Close() error

	// Health checks the health of the storage backend.
	Health(ctx context.Context) error
}
