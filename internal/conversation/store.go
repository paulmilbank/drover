// Package conversation provides persistent storage for Claude conversations
package conversation

import (
	"context"
	"io"
	"time"

	"github.com/cloud-shuttle/drover/pkg/types"
)

// Store manages conversation persistence
type Store interface {
	// Conversation management

	// CreateConversation creates a new conversation for a task
	CreateConversation(ctx context.Context, taskID, worktree string) (*types.Conversation, error)

	// GetConversation retrieves a conversation by ID
	GetConversation(ctx context.Context, conversationID string) (*types.Conversation, error)

	// GetConversationByTask retrieves the active conversation for a task
	GetConversationByTask(ctx context.Context, taskID string) (*types.Conversation, error)

	// UpdateConversationStatus updates the status of a conversation
	UpdateConversationStatus(ctx context.Context, conversationID string, status types.ConversationStatus) error

	// DeleteConversation deletes a conversation and all its turns
	DeleteConversation(ctx context.Context, conversationID string) error

	// Conversation turns

	// AppendTurn adds a new turn to a conversation
	AppendTurn(ctx context.Context, turn *types.ConversationTurn) error

	// GetTurn retrieves a specific turn by ID
	GetTurn(ctx context.Context, turnID string) (*types.ConversationTurn, error)

	// GetRecentTurns retrieves the N most recent turns from a conversation
	GetRecentTurns(ctx context.Context, conversationID string, limit int) ([]*types.ConversationTurn, error)

	// GetTurnsByTokenBudget retrieves turns up to a token budget
	GetTurnsByTokenBudget(ctx context.Context, conversationID string, maxTokens int) ([]*types.ConversationTurn, error)

	// GetTurnsByRange retrieves turns in a specific range
	GetTurnsByRange(ctx context.Context, conversationID string, start, end int) ([]*types.ConversationTurn, error)

	// Search

	// SearchTurns performs full-text search on conversation turns
	SearchTurns(ctx context.Context, query string, limit int) ([]*types.ConversationSearchResult, error)

	// SearchTurnsInConversation searches within a specific conversation
	SearchTurnsInConversation(ctx context.Context, conversationID, query string, limit int) ([]*types.ConversationSearchResult, error)

	// Context building

	// BuildContext loads conversation context for a task
	BuildContext(ctx context.Context, taskID string, options *BuildContextOptions) (*types.ConversationContext, error)

	// ResumeConversation loads conversation context for resuming a task
	ResumeConversation(ctx context.Context, taskID string) (*types.ConversationContext, error)

	// Statistics

	// GetStats returns statistics about a conversation
	GetStats(ctx context.Context, conversationID string) (*types.ConversationStats, error)

	// PruneConversation removes old turns from a conversation
	PruneConversation(ctx context.Context, conversationID string, options *PruneOptions) (int, error)

	// ArchiveConversation moves a completed conversation to archive
	ArchiveConversation(ctx context.Context, conversationID string) error
}

// BuildContextOptions specifies options for building conversation context
type BuildContextOptions struct {
	MaxTurns       int   // Maximum number of recent turns to include
	MaxTokens      int   // Maximum tokens to include (for context window)
	IncludeSystem  bool  // Include system messages
	IncludeTools   bool  // Include tool use messages
	MinTurns       int   // Minimum number of turns to include (even if over token budget)
	CompressionOK  bool  // Allow compressed content in context
}

// PruneOptions specifies options for pruning conversation history
type PruneOptions struct {
	KeepLastN      int           // Keep the last N turns (default: keep all)
	OlderThan      time.Duration // Remove turns older than this duration
	KeepSystem     bool          // Keep system messages
	MaxTokens      int           // Prune until total tokens is under this limit
	DryRun         bool          // Don't actually delete, just return count
}

// CompressionType represents the compression algorithm used
type CompressionType string

const (
	CompressionNone CompressionType = "none"
	CompressionZstd CompressionType = "zstd"
)

// Compressor handles compression/decompression of conversation content
type Compressor interface {
	// Compress compresses the input data
	Compress(data []byte) ([]byte, error)

	// Decompress decompresses the input data
	Decompress(data []byte) ([]byte, error)

	// Type returns the compression type
	Type() CompressionType
}

// NoopCompressor is a compressor that does nothing
type NoopCompressor struct{}

func (c *NoopCompressor) Compress(data []byte) ([]byte, error) {
	return data, nil
}

func (c *NoopCompressor) Decompress(data []byte) ([]byte, error) {
	return data, nil
}

func (c *NoopCompressor) Type() CompressionType {
	return CompressionNone
}

// TokenCounter estimates token count for text
type TokenCounter interface {
	// CountTokens estimates the number of tokens in the text
	CountTokens(text string) int
}

// SimpleTokenCounter is a simple token counter (rough estimate: ~4 chars per token)
type SimpleTokenCounter struct{}

func (c *SimpleTokenCounter) CountTokens(text string) int {
	// Rough estimate: ~4 characters per token
	return len(text) / 4
}

// Writer is an interface for writing turns to a conversation
type Writer interface {
	io.WriteCloser

	// WriteTurn writes a turn to the conversation
	WriteTurn(turn *types.ConversationTurn) error

	// Flush ensures all turns are written
	Flush() error
}

// Reader is an interface for reading turns from a conversation
type Reader interface {
	io.Closer

	// Next returns the next turn in the conversation
	Next() (*types.ConversationTurn, error)

	// Seek moves to a specific turn number
	Seek(turnNumber int) error
}
