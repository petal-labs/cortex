package types

import "time"

// Document represents a document in the knowledge store.
type Document struct {
	ID           string            `json:"id"`
	Namespace    string            `json:"namespace"`
	CollectionID string            `json:"collection_id"`
	Title        string            `json:"title,omitempty"`
	Content      string            `json:"content"`
	ContentType  string            `json:"content_type"` // "text", "markdown", "html"
	Source       string            `json:"source,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	SourceUser   string            `json:"source_user,omitempty"` // For future GDPR/PII compliance
	TenantID     string            `json:"tenant_id,omitempty"`   // For future multi-tenancy
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// Chunk represents a chunk of a document with its embedding.
type Chunk struct {
	ID           string            `json:"id"`
	DocumentID   string            `json:"document_id"`
	Namespace    string            `json:"namespace"`
	CollectionID string            `json:"collection_id"`
	Content      string            `json:"content"`
	Embedding    []float32         `json:"embedding,omitempty"`
	Index        int               `json:"index"` // Position within the document
	TokenCount   int               `json:"token_count,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// Collection represents a collection of documents.
type Collection struct {
	ID          string      `json:"id"`
	Namespace   string      `json:"namespace"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	ChunkConfig ChunkConfig `json:"chunk_config"`
	CreatedAt   time.Time   `json:"created_at"`
}

// ChunkConfig configures how documents are chunked.
type ChunkConfig struct {
	Strategy  string `json:"strategy"`            // "fixed", "sentence", "paragraph", "semantic"
	MaxTokens int    `json:"max_tokens"`          // Max tokens per chunk (default: 512)
	Overlap   int    `json:"overlap"`             // Overlap tokens between chunks (default: 50)
	Separator string `json:"separator,omitempty"` // Custom split delimiter
}

// DefaultChunkConfig returns a ChunkConfig with default values.
func DefaultChunkConfig() ChunkConfig {
	return ChunkConfig{
		Strategy:  "sentence",
		MaxTokens: 512,
		Overlap:   50,
	}
}

// ChunkResult represents a search result for a chunk.
type ChunkResult struct {
	Chunk         *Chunk            `json:"chunk"`
	Score         float64           `json:"score"`
	DocumentTitle string            `json:"document_title"`
	Source        string            `json:"source"`
	DocMetadata   map[string]string `json:"doc_metadata,omitempty"`
	ContextBefore string            `json:"context_before,omitempty"`
	ContextAfter  string            `json:"context_after,omitempty"`
}

// CollectionStats holds statistics for a collection.
type CollectionStats struct {
	DocumentCount int64     `json:"document_count"`
	ChunkCount    int64     `json:"chunk_count"`
	TotalTokens   int64     `json:"total_tokens"`
	LastIngest    time.Time `json:"last_ingest"`
}

// KnowledgeIngestResponse represents the response from knowledge.ingest.
type KnowledgeIngestResponse struct {
	DocumentID    string `json:"document_id"`
	ChunksCreated int    `json:"chunks_created"`
	CollectionID  string `json:"collection_id"`
}

// KnowledgeSearchResponse represents the response from knowledge.search.
type KnowledgeSearchResponse struct {
	Results    []*ChunkResult `json:"results"`
	TotalFound int            `json:"total_found"`
}
