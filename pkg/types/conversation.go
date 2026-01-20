// Package types defines core data structures for Drover
package types

// ConversationStatus represents the state of a conversation
type ConversationStatus string

const (
	ConversationStatusActive    ConversationStatus = "active"
	ConversationStatusPaused    ConversationStatus = "paused"
	ConversationStatusCompleted ConversationStatus = "completed"
	ConversationStatusFailed    ConversationStatus = "failed"
)

// ConversationRole represents the role of a message sender
type ConversationRole string

const (
	ConversationRoleUser      ConversationRole = "user"
	ConversationRoleAssistant ConversationRole = "assistant"
	ConversationRoleSystem    ConversationRole = "system"
	ConversationRoleTool      ConversationRole = "tool"
)

// Conversation represents a conversation with an AI agent
type Conversation struct {
	ID        string            `json:"id" db:"id"`
	TaskID    string            `json:"task_id" db:"task_id"`
	Worktree  string            `json:"worktree,omitempty" db:"worktree"`
	Status    ConversationStatus `json:"status" db:"status"`
	CreatedAt int64             `json:"created_at" db:"created_at"`
	UpdatedAt int64             `json:"updated_at" db:"updated_at"`

	// Metadata
	TurnCount       int       `json:"turn_count" db:"turn_count"`
	TotalTokens     int       `json:"total_tokens" db:"total_tokens"`
	LastMessageAt   int64     `json:"last_message_at" db:"last_message_at"`
	CompressionType string    `json:"compression_type,omitempty" db:"compression_type"` // "none", "zstd"
}

// ConversationTurn represents a single message or tool use in a conversation
type ConversationTurn struct {
	ID          string            `json:"id" db:"id"`
	ConversationID string         `json:"conversation_id" db:"conversation_id"`
	TurnNumber  int               `json:"turn_number" db:"turn_number"`
	Role        ConversationRole  `json:"role" db:"role"`
	Content     string            `json:"content" db:"content"` // May be compressed

	// Tool use fields (only populated when role == "tool")
	ToolUseID   string `json:"tool_use_id,omitempty" db:"tool_use_id"`
	ToolName    string `json:"tool_name,omitempty" db:"tool_name"`
	ToolInput   string `json:"tool_input,omitempty" db:"tool_input"`
	ToolResult  string `json:"tool_result,omitempty" db:"tool_result"` // May be compressed

	// Token usage
	TokensUsed  int    `json:"tokens_used,omitempty" db:"tokens_used"`
	CreatedAt   int64  `json:"created_at" db:"created_at"`

	// Metadata
	Compressed bool   `json:"-" db:"compressed"` // Internal flag indicating content is compressed
}

// ConversationTurnFTS represents the FTS5 searchable version of a turn
type ConversationTurnFTS struct {
	RowID       int64  `json:"row_id"`
	TurnID      string `json:"turn_id"`
	Content     string `json:"content"`
	ToolName    string `json:"tool_name"`
	Role        string `json:"role"`
}

// ConversationSearchResult represents a search result from FTS5
type ConversationSearchResult struct {
	Turn        *ConversationTurn `json:"turn"`
	Rank        float64           `json:"rank"`
	MatchCount  int               `json:"match_count"`
	Highlighted string            `json:"highlighted,omitempty"` // Search term highlights
}

// ConversationContext represents the context loaded for a conversation
type ConversationContext struct {
	ConversationID string             `json:"conversation_id"`
	TaskID         string             `json:"task_id"`
	Turns          []*ConversationTurn `json:"turns"`
	TotalTokens    int                `json:"total_tokens"`
	TokenBudget    int                `json:"token_budget"`
	LoadedAt       int64              `json:"loaded_at"`
}

// ConversationStats represents statistics about a conversation
type ConversationStats struct {
	TotalTurns        int     `json:"total_turns"`
	UserTurns         int     `json:"user_turns"`
	AssistantTurns    int     `json:"assistant_turns"`
	ToolTurns         int     `json:"tool_turns"`
	TotalTokens       int     `json:"total_tokens"`
	AvgTokensPerTurn  float64 `json:"avg_tokens_per_turn"`
	StorageBytes      int64   `json:"storage_bytes"`
	Duration          int64   `json:"duration"` // in seconds
}
