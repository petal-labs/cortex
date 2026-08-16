package dto

import (
	"time"

	ctxengine "github.com/petal-labs/cortex/internal/context"
	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/internal/entity"
	"github.com/petal-labs/cortex/internal/knowledge"
	"github.com/petal-labs/cortex/pkg/types"
)

var messageFixture = types.Message{
	ID:        "msg-1",
	Namespace: "ns",
	ThreadID:  "thread-1",
	Role:      "user",
	Content:   "hello",
	Metadata:  map[string]string{"k": "v"},
	CreatedAt: ts1,
}

var conversationHistoryInput = &conversation.HistoryResult{
	Messages: []*types.Message{
		&messageFixture,
		{ID: "msg-2", Namespace: "ns", ThreadID: "thread-1", Role: "assistant", Content: "hi", CreatedAt: ts2},
	},
	Summary:    "a summary",
	NextCursor: "",
	ThreadID:   "thread-1",
}

var searchInput = &conversation.SearchResult{
	Results: []*types.MessageResult{
		{Message: &messageFixture, Score: 0.95, Rank: 1, ThreadID: "thread-1"},
	},
	Query: "hello",
}

var summarizeInput = &conversation.SummarizeResult{
	Summary:            "a summary",
	MessagesSummarized: 4,
	MessagesKept:       10,
	ThreadID:           "thread-1",
}

var ingestInput = &knowledge.IngestResult{
	DocumentID:    "doc-1",
	ChunksCreated: 3,
	CollectionID:  "col-1",
}

var knowledgeSearchInput = &knowledge.SearchResult{
	Results: []*types.ChunkResult{
		{
			Chunk: &types.Chunk{
				ID:           "chunk-1",
				DocumentID:   "doc-1",
				Namespace:    "ns",
				CollectionID: "col-1",
				Content:      "chunk content",
				Index:        0,
				TokenCount:   2,
			},
			Score:         0.91,
			Rank:          1,
			DocumentTitle: "Doc",
			Source:        "test://src",
		},
	},
	Query:      "chunk",
	TotalFound: 1,
}

var collectionFixture = &types.Collection{
	ID:        "col-1",
	Namespace: "ns",
	Name:      "docs",
	ChunkConfig: types.ChunkConfig{
		Strategy:  "sentence",
		MaxTokens: 512,
		Overlap:   50,
	},
	CreatedAt: ts1,
}

var collectionListInput = []*types.Collection{collectionFixture}

var bulkIngestInput = &knowledge.BulkIngestResult{
	CollectionID:   "col-1",
	TotalDocuments: 2,
	Succeeded:      1,
	Failed:         1,
	TotalChunks:    3,
	Documents: []*knowledge.BulkIngestDocResult{
		{Index: 0, DocumentID: "doc-1", Title: "ok", ChunksCreated: 3, Success: true},
		{Index: 1, Title: "bad", Success: false, Error: "empty content"},
	},
}

var contextGetInput = &ctxengine.GetResult{
	Key:       "theme",
	Value:     map[string]any{"color": "dark"},
	Version:   2,
	UpdatedAt: ts1,
	Exists:    true,
}

var contextSetInput = &ctxengine.SetResult{
	Key:             "theme",
	Version:         3,
	PreviousVersion: 2,
}

var contextMergeInput = &ctxengine.MergeResult{
	Key:         "theme",
	Version:     4,
	MergedValue: map[string]any{"color": "dark"},
}

var contextListInput = &ctxengine.ListResult{
	Keys:  []string{"a", "b"},
	Count: 2,
}

var contextHistoryInput = &ctxengine.HistoryResult{
	Key: "theme",
	History: []*types.ContextHistoryEntry{
		{Version: 1, Value: "v1", Operation: "set", UpdatedAt: ts1, UpdatedBy: "agent"},
		{Version: 2, Value: map[string]any{"color": "dark"}, Operation: "merge", UpdatedAt: ts2},
	},
}

var entityFixture = types.Entity{
	ID:           "ent-1",
	Namespace:    "ns",
	Name:         "Acme",
	Type:         types.EntityTypeOrganization,
	Aliases:      []string{"ACME"},
	Summary:      "a corp",
	Attributes:   map[string]string{"industry": "tech"},
	MentionCount: 3,
	FirstSeenAt:  ts1,
	LastSeenAt:   ts2,
}

var entityPtrFixture = &entityFixture

var entityQueryInput = &types.EntityQueryResponse{
	Entity: entityPtrFixture,
	Relationships: []*types.EntityRelationship{
		{
			ID: "rel-1", Namespace: "ns", SourceEntityID: "ent-1", TargetEntityID: "ent-2",
			RelationType: "competes_with", Confidence: 0.8, MentionCount: 1,
			FirstSeenAt: ts1, LastSeenAt: ts2,
		},
	},
	Mentions: []*types.EntityMention{
		{ID: "m-1", EntityID: "ent-1", Namespace: "ns", SourceType: "conversation", SourceID: "msg-1", Context: "Acme said", Snippet: "Acme", CreatedAt: ts1},
	},
	Found: true,
}

var entitySearchInput = &entity.SearchResult{
	Results: []*types.EntityResult{
		{Entity: &entityFixture, Score: 0.9, Rank: 1},
	},
	Query:      "acme",
	TotalFound: 1,
}

var relationshipListInput = []*types.EntityRelationship{
	{
		ID: "rel-1", Namespace: "ns", SourceEntityID: "ent-1", TargetEntityID: "ent-2",
		RelationType: "competes_with", Description: "rivals", Confidence: 0.8, MentionCount: 2,
		FirstSeenAt: ts1, LastSeenAt: ts2,
	},
}

var entityMergeInput = &entity.MergeResult{
	KeptEntity:          &entityFixture,
	MergedMentions:      3,
	MergedRelationships: 1,
}

var entityListInput = &entity.ListResult{
	Entities: []*types.Entity{&entityFixture},
	Count:    1,
}

// silence unused warnings if fixtures change.
var _ = time.Time{}
