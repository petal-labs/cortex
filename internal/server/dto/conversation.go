package dto

import (
	"time"

	"github.com/petal-labs/cortex/internal/conversation"
	"github.com/petal-labs/cortex/pkg/types"
)

// Message mirrors the wire shape of types.Message.
type Message struct {
	ID         string            `json:"id"`
	Namespace  string            `json:"namespace"`
	ThreadID   string            `json:"thread_id"`
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	SourceUser string            `json:"source_user,omitempty"`
	TenantID   string            `json:"tenant_id,omitempty"`
	Summarized bool              `json:"summarized,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
}

func newMessage(m *types.Message) *Message {
	if m == nil {
		return nil
	}
	return &Message{
		ID:         m.ID,
		Namespace:  m.Namespace,
		ThreadID:   m.ThreadID,
		Role:       m.Role,
		Content:    m.Content,
		Metadata:   m.Metadata,
		SourceUser: m.SourceUser,
		TenantID:   m.TenantID,
		Summarized: m.Summarized,
		CreatedAt:  m.CreatedAt,
	}
}

// MessageResult mirrors types.MessageResult.
type MessageResult struct {
	Message  *Message `json:"message"`
	Score    float64  `json:"score"`
	Rank     int      `json:"rank,omitempty"`
	ThreadID string   `json:"thread_id"`
}

func newMessageResult(r *types.MessageResult) *MessageResult {
	if r == nil {
		return nil
	}
	return &MessageResult{
		Message:  newMessage(r.Message),
		Score:    r.Score,
		Rank:     r.Rank,
		ThreadID: r.ThreadID,
	}
}

// ConversationAppend is the conversation_append response.
type ConversationAppend struct {
	Contract
	Message
}

// NewConversationAppend maps an engine append result.
func NewConversationAppend(m *types.Message) ConversationAppend {
	return ConversationAppend{Contract{SchemaVersion}, *newMessage(m)}
}

// ConversationHistory is the conversation_history response.
type ConversationHistory struct {
	Contract
	Messages   []Message `json:"messages"`
	Summary    string    `json:"summary,omitempty"`
	NextCursor string    `json:"next_cursor,omitempty"`
	ThreadID   string    `json:"thread_id"`
}

// NewConversationHistory maps an engine history result.
func NewConversationHistory(r *conversation.HistoryResult) ConversationHistory {
	if r == nil {
		return ConversationHistory{Contract: Contract{SchemaVersion}}
	}
	out := ConversationHistory{
		Contract:   Contract{SchemaVersion},
		Messages:   make([]Message, 0, len(r.Messages)),
		Summary:    r.Summary,
		NextCursor: r.NextCursor,
		ThreadID:   r.ThreadID,
	}
	for _, m := range r.Messages {
		out.Messages = append(out.Messages, *newMessage(m))
	}
	return out
}

// ConversationSearch is the conversation_search response.
type ConversationSearch struct {
	Contract
	Results []MessageResult `json:"results"`
	Query   string          `json:"query"`
}

// NewConversationSearch maps an engine search result.
func NewConversationSearch(r *conversation.SearchResult) ConversationSearch {
	if r == nil {
		return ConversationSearch{Contract: Contract{SchemaVersion}}
	}
	out := ConversationSearch{
		Contract: Contract{SchemaVersion},
		Results:  make([]MessageResult, 0, len(r.Results)),
		Query:    r.Query,
	}
	for _, res := range r.Results {
		out.Results = append(out.Results, *newMessageResult(res))
	}
	return out
}

// ConversationSummarize is the conversation_summarize response.
type ConversationSummarize struct {
	Contract
	Summary            string `json:"summary"`
	MessagesSummarized int    `json:"messages_summarized"`
	MessagesKept       int    `json:"messages_kept"`
	ThreadID           string `json:"thread_id"`
}

// NewConversationSummarize maps an engine summarize result.
func NewConversationSummarize(r *conversation.SummarizeResult) ConversationSummarize {
	if r == nil {
		return ConversationSummarize{Contract: Contract{SchemaVersion}}
	}
	return ConversationSummarize{
		Contract:           Contract{SchemaVersion},
		Summary:            r.Summary,
		MessagesSummarized: r.MessagesSummarized,
		MessagesKept:       r.MessagesKept,
		ThreadID:           r.ThreadID,
	}
}
