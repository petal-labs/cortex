package types

import "time"

// Message represents a single message in a conversation thread.
type Message struct {
	ID         string            `json:"id"`
	Namespace  string            `json:"namespace"`
	ThreadID   string            `json:"thread_id"`
	Role       string            `json:"role"` // "user", "assistant", "system", "tool"
	Content    string            `json:"content"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	SourceUser string            `json:"source_user,omitempty"` // For future GDPR/PII compliance
	TenantID   string            `json:"tenant_id,omitempty"`   // For future multi-tenancy
	Summarized bool              `json:"summarized,omitempty"`  // Whether this message has been summarized
	CreatedAt  time.Time         `json:"created_at"`
}

// Thread represents a conversation thread containing messages.
type Thread struct {
	ID        string            `json:"id"`
	Namespace string            `json:"namespace"`
	Title     string            `json:"title,omitempty"`
	Summary   string            `json:"summary,omitempty"` // LLM-generated summary of the thread
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// MessageResult represents a search result for a message.
type MessageResult struct {
	Message  *Message `json:"message"`
	Score    float64  `json:"score"`
	ThreadID string   `json:"thread_id"`
}

// ConversationHistoryResponse represents the response from conversation.history.
type ConversationHistoryResponse struct {
	Messages   []*Message `json:"messages"`
	Summary    string     `json:"summary,omitempty"`
	TotalCount int        `json:"total_count"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// ConversationSearchResponse represents the response from conversation.search.
type ConversationSearchResponse struct {
	Results []*MessageResult `json:"results"`
}
