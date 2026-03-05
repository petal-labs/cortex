package storage

import (
	"github.com/petal-labs/cortex/pkg/types"
)

// ExportData represents all data for a namespace that can be exported/imported.
type ExportData struct {
	Version    int    `json:"version"`
	Namespace  string `json:"namespace"`
	ExportedAt int64  `json:"exported_at"`

	// Conversation data
	Threads  []*ThreadExport  `json:"threads,omitempty"`
	Messages []*MessageExport `json:"messages,omitempty"`

	// Knowledge data
	Collections []*types.Collection `json:"collections,omitempty"`
	Documents   []*DocumentExport   `json:"documents,omitempty"`
	Chunks      []*ChunkExport      `json:"chunks,omitempty"`

	// Context data
	ContextEntries []*types.ContextEntry   `json:"context_entries,omitempty"`
	ContextHistory []*ContextHistoryExport `json:"context_history,omitempty"`

	// Entity data
	Entities      []*types.Entity             `json:"entities,omitempty"`
	EntityAliases []*EntityAliasExport        `json:"entity_aliases,omitempty"`
	Mentions      []*types.EntityMention      `json:"entity_mentions,omitempty"`
	Relationships []*types.EntityRelationship `json:"entity_relationships,omitempty"`
}

// ThreadExport includes thread data with embedding.
type ThreadExport struct {
	*types.Thread
}

// MessageExport includes message data with embedding.
type MessageExport struct {
	*types.Message
	Embedding []float32 `json:"embedding,omitempty"`
}

// DocumentExport includes document metadata.
type DocumentExport struct {
	*types.Document
}

// ChunkExport includes chunk data with embedding.
type ChunkExport struct {
	*types.Chunk
	Embedding []float32 `json:"embedding,omitempty"`
}

// EntityAliasExport represents an entity alias for export.
type EntityAliasExport struct {
	Namespace string `json:"namespace"`
	Alias     string `json:"alias"`
	EntityID  string `json:"entity_id"`
}

// ContextHistoryExport includes full context history with namespace and key.
type ContextHistoryExport struct {
	Namespace string  `json:"namespace"`
	Key       string  `json:"key"`
	RunID     *string `json:"run_id,omitempty"`
	*types.ContextHistoryEntry
}

// ExportOptions configures what data to export.
type ExportOptions struct {
	IncludeConversations bool
	IncludeKnowledge     bool
	IncludeContext       bool
	IncludeEntities      bool
	IncludeEmbeddings    bool
}

// DefaultExportOptions returns options that export everything.
func DefaultExportOptions() ExportOptions {
	return ExportOptions{
		IncludeConversations: true,
		IncludeKnowledge:     true,
		IncludeContext:       true,
		IncludeEntities:      true,
		IncludeEmbeddings:    true,
	}
}
