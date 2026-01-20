// Package conversation provides persistent storage for Claude conversations
package conversation

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/cloud-shuttle/drover/pkg/types"
	"github.com/google/uuid"
)

// SQLiteStore implements Store using SQLite
type SQLiteStore struct {
	db           *sql.DB
	compressor   Compressor
	tokenCounter TokenCounter
}

// NewSQLiteStore creates a new SQLite-backed conversation store
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{
		db:           db,
		compressor:   &NoopCompressor{},
		tokenCounter: &SimpleTokenCounter{},
	}
}

// SetCompressor sets the compressor for this store
func (s *SQLiteStore) SetCompressor(c Compressor) {
	s.compressor = c
}

// SetTokenCounter sets the token counter for this store
func (s *SQLiteStore) SetTokenCounter(c TokenCounter) {
	s.tokenCounter = c
}

// CreateConversation creates a new conversation for a task
func (s *SQLiteStore) CreateConversation(ctx context.Context, taskID, worktree string) (*types.Conversation, error) {
	id := generateConversationID()
	now := time.Now().Unix()

	conv := &types.Conversation{
		ID:             id,
		TaskID:         taskID,
		Worktree:       worktree,
		Status:         types.ConversationStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
		TurnCount:      0,
		TotalTokens:    0,
		CompressionType: string(s.compressor.Type()),
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversations (id, task_id, worktree, status, created_at, updated_at, turn_count, total_tokens, compression_type)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, conv.ID, conv.TaskID, conv.Worktree, conv.Status, conv.CreatedAt, conv.UpdatedAt, conv.TurnCount, conv.TotalTokens, conv.CompressionType)

	if err != nil {
		return nil, fmt.Errorf("creating conversation: %w", err)
	}

	return conv, nil
}

// GetConversation retrieves a conversation by ID
func (s *SQLiteStore) GetConversation(ctx context.Context, conversationID string) (*types.Conversation, error) {
	var conv types.Conversation
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, worktree, status, created_at, updated_at, turn_count, total_tokens, last_message_at, compression_type
		FROM conversations WHERE id = ?
	`, conversationID).Scan(
		&conv.ID, &conv.TaskID, &conv.Worktree, &conv.Status,
		&conv.CreatedAt, &conv.UpdatedAt, &conv.TurnCount, &conv.TotalTokens,
		&conv.LastMessageAt, &conv.CompressionType,
	)

	if err == sql.ErrNoRows {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting conversation: %w", err)
	}

	return &conv, nil
}

// GetConversationByTask retrieves the active conversation for a task
func (s *SQLiteStore) GetConversationByTask(ctx context.Context, taskID string) (*types.Conversation, error) {
	var conv types.Conversation
	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, worktree, status, created_at, updated_at, turn_count, total_tokens, last_message_at, compression_type
		FROM conversations WHERE task_id = ? AND status = 'active'
		ORDER BY created_at DESC LIMIT 1
	`, taskID).Scan(
		&conv.ID, &conv.TaskID, &conv.Worktree, &conv.Status,
		&conv.CreatedAt, &conv.UpdatedAt, &conv.TurnCount, &conv.TotalTokens,
		&conv.LastMessageAt, &conv.CompressionType,
	)

	if err == sql.ErrNoRows {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting conversation by task: %w", err)
	}

	return &conv, nil
}

// UpdateConversationStatus updates the status of a conversation
func (s *SQLiteStore) UpdateConversationStatus(ctx context.Context, conversationID string, status types.ConversationStatus) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		UPDATE conversations SET status = ?, updated_at = ? WHERE id = ?
	`, status, now, conversationID)

	if err != nil {
		return fmt.Errorf("updating conversation status: %w", err)
	}

	return nil
}

// DeleteConversation deletes a conversation and all its turns
func (s *SQLiteStore) DeleteConversation(ctx context.Context, conversationID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, conversationID)
	if err != nil {
		return fmt.Errorf("deleting conversation: %w", err)
	}
	return nil
}

// AppendTurn adds a new turn to a conversation
func (s *SQLiteStore) AppendTurn(ctx context.Context, turn *types.ConversationTurn) error {
	// Compress content if enabled and large enough
	content := turn.Content
	var compressed bool

	if s.compressor != nil && s.compressor.Type() != CompressionNone && len(content) > 1024 {
		compressedData, err := s.compressor.Compress([]byte(content))
		if err == nil {
			content = string(compressedData)
			compressed = true
		}
		// If compression fails, continue with uncompressed
	}

	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversation_turns (id, conversation_id, turn_number, role, content, tool_use_id, tool_name, tool_input, tool_result, tokens_used, created_at, compressed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, turn.ID, turn.ConversationID, turn.TurnNumber, turn.Role, content, turn.ToolUseID, turn.ToolName, turn.ToolInput, turn.ToolResult, turn.TokensUsed, now, compressed)

	if err != nil {
		return fmt.Errorf("appending turn: %w", err)
	}

	// Update conversation metadata
	_, err = s.db.ExecContext(ctx, `
		UPDATE conversations
		SET turn_count = turn_count + 1,
		    total_tokens = total_tokens + ?,
		    last_message_at = ?,
		    updated_at = ?
		WHERE id = ?
	`, turn.TokensUsed, now, now, turn.ConversationID)

	if err != nil {
		return fmt.Errorf("updating conversation metadata: %w", err)
	}

	return nil
}

// GetTurn retrieves a specific turn by ID
func (s *SQLiteStore) GetTurn(ctx context.Context, turnID string) (*types.ConversationTurn, error) {
	var turn types.ConversationTurn
	var compressed bool

	err := s.db.QueryRowContext(ctx, `
		SELECT id, conversation_id, turn_number, role, content, tool_use_id, tool_name, tool_input, tool_result, tokens_used, created_at, compressed
		FROM conversation_turns WHERE id = ?
	`, turnID).Scan(
		&turn.ID, &turn.ConversationID, &turn.TurnNumber, &turn.Role, &turn.Content,
		&turn.ToolUseID, &turn.ToolName, &turn.ToolInput, &turn.ToolResult,
		&turn.TokensUsed, &turn.CreatedAt, &compressed,
	)

	if err == sql.ErrNoRows {
		return nil, ErrTurnNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("getting turn: %w", err)
	}

	// Decompress if needed
	if compressed && s.compressor != nil && s.compressor.Type() != CompressionNone {
		data, err := s.compressor.Decompress([]byte(turn.Content))
		if err != nil {
			return nil, fmt.Errorf("decompressing turn content: %w", err)
		}
		turn.Content = string(data)
	}

	return &turn, nil
}

// GetRecentTurns retrieves the N most recent turns from a conversation
func (s *SQLiteStore) GetRecentTurns(ctx context.Context, conversationID string, limit int) ([]*types.ConversationTurn, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conversation_id, turn_number, role, content, tool_use_id, tool_name, tool_input, tool_result, tokens_used, created_at, compressed
		FROM conversation_turns
		WHERE conversation_id = ?
		ORDER BY turn_number DESC
		LIMIT ?
	`, conversationID, limit)

	if err != nil {
		return nil, fmt.Errorf("querying recent turns: %w", err)
	}
	defer rows.Close()

	var turns []*types.ConversationTurn
	for rows.Next() {
		var turn types.ConversationTurn
		var compressed bool

		err := rows.Scan(
			&turn.ID, &turn.ConversationID, &turn.TurnNumber, &turn.Role, &turn.Content,
			&turn.ToolUseID, &turn.ToolName, &turn.ToolInput, &turn.ToolResult,
			&turn.TokensUsed, &turn.CreatedAt, &compressed,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning turn row: %w", err)
		}

		// Decompress if needed
		if compressed && s.compressor != nil && s.compressor.Type() != CompressionNone {
			data, err := s.compressor.Decompress([]byte(turn.Content))
			if err != nil {
				return nil, fmt.Errorf("decompressing turn content: %w", err)
			}
			turn.Content = string(data)
		}

		turns = append(turns, &turn)
	}

	// Reverse to get chronological order
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}

	return turns, nil
}

// GetTurnsByTokenBudget retrieves turns up to a token budget
func (s *SQLiteStore) GetTurnsByTokenBudget(ctx context.Context, conversationID string, maxTokens int) ([]*types.ConversationTurn, error) {
	// First get all turns
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conversation_id, turn_number, role, content, tool_use_id, tool_name, tool_input, tool_result, tokens_used, created_at, compressed
		FROM conversation_turns
		WHERE conversation_id = ?
		ORDER BY turn_number ASC
	`, conversationID)

	if err != nil {
		return nil, fmt.Errorf("querying turns: %w", err)
	}
	defer rows.Close()

	var turns []*types.ConversationTurn
	totalTokens := 0

	for rows.Next() {
		var turn types.ConversationTurn
		var compressed bool

		err := rows.Scan(
			&turn.ID, &turn.ConversationID, &turn.TurnNumber, &turn.Role, &turn.Content,
			&turn.ToolUseID, &turn.ToolName, &turn.ToolInput, &turn.ToolResult,
			&turn.TokensUsed, &turn.CreatedAt, &compressed,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning turn row: %w", err)
		}

		// Use tokens_used if available, otherwise estimate
		tokens := turn.TokensUsed
		if tokens == 0 {
			tokens = s.tokenCounter.CountTokens(turn.Content)
		}

		if totalTokens+tokens > maxTokens && len(turns) > 0 {
			break
		}

		// Decompress if needed
		if compressed && s.compressor != nil && s.compressor.Type() != CompressionNone {
			data, err := s.compressor.Decompress([]byte(turn.Content))
			if err != nil {
				return nil, fmt.Errorf("decompressing turn content: %w", err)
			}
			turn.Content = string(data)
		}

		turns = append(turns, &turn)
		totalTokens += tokens
	}

	return turns, nil
}

// GetTurnsByRange retrieves turns in a specific range
func (s *SQLiteStore) GetTurnsByRange(ctx context.Context, conversationID string, start, end int) ([]*types.ConversationTurn, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conversation_id, turn_number, role, content, tool_use_id, tool_name, tool_input, tool_result, tokens_used, created_at, compressed
		FROM conversation_turns
		WHERE conversation_id = ? AND turn_number >= ? AND turn_number <= ?
		ORDER BY turn_number ASC
	`, conversationID, start, end)

	if err != nil {
		return nil, fmt.Errorf("querying turns by range: %w", err)
	}
	defer rows.Close()

	var turns []*types.ConversationTurn
	for rows.Next() {
		var turn types.ConversationTurn
		var compressed bool

		err := rows.Scan(
			&turn.ID, &turn.ConversationID, &turn.TurnNumber, &turn.Role, &turn.Content,
			&turn.ToolUseID, &turn.ToolName, &turn.ToolInput, &turn.ToolResult,
			&turn.TokensUsed, &turn.CreatedAt, &compressed,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning turn row: %w", err)
		}

		// Decompress if needed
		if compressed && s.compressor != nil && s.compressor.Type() != CompressionNone {
			data, err := s.compressor.Decompress([]byte(turn.Content))
			if err != nil {
				return nil, fmt.Errorf("decompressing turn content: %w", err)
			}
			turn.Content = string(data)
		}

		turns = append(turns, &turn)
	}

	return turns, nil
}

// SearchTurns performs full-text search on conversation turns
func (s *SQLiteStore) SearchTurns(ctx context.Context, query string, limit int) ([]*types.ConversationSearchResult, error) {
	// Use FTS5 search with BM25 ranking
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			ct.id,
			ct.conversation_id,
			ct.turn_number,
			ct.role,
			ct.content,
			ct.tool_use_id,
			ct.tool_name,
			ct.tool_input,
			ct.tool_result,
			ct.tokens_used,
			ct.created_at,
			ct.compressed,
			fts.rank,
			snippet(conversation_turns_fts.content, 2, '...', '...', 20) as highlight
		FROM conversation_turns_fts fts
		INNER JOIN conversation_turns ct ON ct.rowid = fts.rowid
		WHERE conversation_turns_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, sanitizeFTSQuery(query), limit)

	if err != nil {
		return nil, fmt.Errorf("searching turns: %w", err)
	}
	defer rows.Close()

	var results []*types.ConversationSearchResult
	for rows.Next() {
		var turn types.ConversationTurn
		var compressed bool
		var rank float64
		var highlight sql.NullString

		err := rows.Scan(
			&turn.ID, &turn.ConversationID, &turn.TurnNumber, &turn.Role, &turn.Content,
			&turn.ToolUseID, &turn.ToolName, &turn.ToolInput, &turn.ToolResult,
			&turn.TokensUsed, &turn.CreatedAt, &compressed, &rank, &highlight,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning search result: %w", err)
		}

		// Decompress if needed
		if compressed && s.compressor != nil && s.compressor.Type() != CompressionNone {
			data, err := s.compressor.Decompress([]byte(turn.Content))
			if err != nil {
				return nil, fmt.Errorf("decompressing turn content: %w", err)
			}
			turn.Content = string(data)
		}

		result := &types.ConversationSearchResult{
			Turn:       &turn,
			Rank:       rank,
			MatchCount: int(rank * 10), // Rough estimate
		}
		if highlight.Valid {
			result.Highlighted = highlight.String
		}

		results = append(results, result)
	}

	return results, nil
}

// SearchTurnsInConversation searches within a specific conversation
func (s *SQLiteStore) SearchTurnsInConversation(ctx context.Context, conversationID, query string, limit int) ([]*types.ConversationSearchResult, error) {
	// Use FTS5 search with conversation filter
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			ct.id,
			ct.conversation_id,
			ct.turn_number,
			ct.role,
			ct.content,
			ct.tool_use_id,
			ct.tool_name,
			ct.tool_input,
			ct.tool_result,
			ct.tokens_used,
			ct.created_at,
			ct.compressed,
			fts.rank,
			snippet(conversation_turns_fts.content, 2, '...', '...', 20) as highlight
		FROM conversation_turns_fts fts
		INNER JOIN conversation_turns ct ON ct.rowid = fts.rowid
		WHERE ct.conversation_id = ? AND conversation_turns_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, conversationID, sanitizeFTSQuery(query), limit)

	if err != nil {
		return nil, fmt.Errorf("searching turns in conversation: %w", err)
	}
	defer rows.Close()

	var results []*types.ConversationSearchResult
	for rows.Next() {
		var turn types.ConversationTurn
		var compressed bool
		var rank float64
		var highlight sql.NullString

		err := rows.Scan(
			&turn.ID, &turn.ConversationID, &turn.TurnNumber, &turn.Role, &turn.Content,
			&turn.ToolUseID, &turn.ToolName, &turn.ToolInput, &turn.ToolResult,
			&turn.TokensUsed, &turn.CreatedAt, &compressed, &rank, &highlight,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning search result: %w", err)
		}

		// Decompress if needed
		if compressed && s.compressor != nil && s.compressor.Type() != CompressionNone {
			data, err := s.compressor.Decompress([]byte(turn.Content))
			if err != nil {
				return nil, fmt.Errorf("decompressing turn content: %w", err)
			}
			turn.Content = string(data)
		}

		result := &types.ConversationSearchResult{
			Turn:       &turn,
			Rank:       rank,
			MatchCount: int(rank * 10), // Rough estimate
		}
		if highlight.Valid {
			result.Highlighted = highlight.String
		}

		results = append(results, result)
	}

	return results, nil
}

// BuildContext loads conversation context for a task
func (s *SQLiteStore) BuildContext(ctx context.Context, taskID string, options *BuildContextOptions) (*types.ConversationContext, error) {
	if options == nil {
		options = &BuildContextOptions{
			MaxTurns:      50,
			MaxTokens:     100000,
			IncludeSystem: true,
			IncludeTools:  true,
			MinTurns:      5,
		}
	}

	// Get the active conversation
	conv, err := s.GetConversationByTask(ctx, taskID)
	if err != nil {
		if err == ErrConversationNotFound {
			// No conversation exists, return empty context
			return &types.ConversationContext{
				TaskID:      taskID,
				Turns:       []*types.ConversationTurn{},
				TotalTokens: 0,
				TokenBudget: options.MaxTokens,
				LoadedAt:    time.Now().Unix(),
			}, nil
		}
		return nil, err
	}

	// Get turns based on options
	var turns []*types.ConversationTurn

	if options.MaxTokens > 0 {
		turns, err = s.GetTurnsByTokenBudget(ctx, conv.ID, options.MaxTokens)
	} else {
		turns, err = s.GetRecentTurns(ctx, conv.ID, options.MaxTurns)
	}

	if err != nil {
		return nil, fmt.Errorf("loading turns: %w", err)
	}

	// Filter based on options
	turns = filterTurns(turns, options)

	// Calculate total tokens
	totalTokens := 0
	for _, turn := range turns {
		if turn.TokensUsed > 0 {
			totalTokens += turn.TokensUsed
		} else {
			totalTokens += s.tokenCounter.CountTokens(turn.Content)
		}
	}

	return &types.ConversationContext{
		ConversationID: conv.ID,
		TaskID:         taskID,
		Turns:          turns,
		TotalTokens:    totalTokens,
		TokenBudget:    options.MaxTokens,
		LoadedAt:       time.Now().Unix(),
	}, nil
}

// ResumeConversation loads conversation context for resuming a task
func (s *SQLiteStore) ResumeConversation(ctx context.Context, taskID string) (*types.ConversationContext, error) {
	options := &BuildContextOptions{
		MaxTurns:      100, // Get more turns for resume
		MaxTokens:     200000,
		IncludeSystem: true,
		IncludeTools:  true,
		MinTurns:      10,
	}
	return s.BuildContext(ctx, taskID, options)
}

// GetStats returns statistics about a conversation
func (s *SQLiteStore) GetStats(ctx context.Context, conversationID string) (*types.ConversationStats, error) {
	var stats types.ConversationStats

	// Get basic counts
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) as total_turns,
			COALESCE(SUM(tokens_used), 0) as total_tokens
		FROM conversation_turns WHERE conversation_id = ?
	`, conversationID).Scan(&stats.TotalTurns, &stats.TotalTokens)

	if err != nil {
		return nil, fmt.Errorf("getting conversation stats: %w", err)
	}

	// Get role breakdown
	rows, err := s.db.QueryContext(ctx, `
		SELECT role, COUNT(*) as count
		FROM conversation_turns
		WHERE conversation_id = ?
		GROUP BY role
	`, conversationID)

	if err != nil {
		return nil, fmt.Errorf("getting role breakdown: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var role string
		var count int
		if err := rows.Scan(&role, &count); err != nil {
			return nil, fmt.Errorf("scanning role count: %w", err)
		}
		switch types.ConversationRole(role) {
		case types.ConversationRoleUser:
			stats.UserTurns = count
		case types.ConversationRoleAssistant:
			stats.AssistantTurns = count
		case types.ConversationRoleTool:
			stats.ToolTurns = count
		}
	}

	// Calculate average
	if stats.TotalTurns > 0 {
		stats.AvgTokensPerTurn = float64(stats.TotalTokens) / float64(stats.TotalTurns)
	}

	// Get duration
	var createdAt, lastMessageAt int64
	err = s.db.QueryRowContext(ctx, `
		SELECT created_at, COALESCE(last_message_at, created_at)
		FROM conversations WHERE id = ?
	`, conversationID).Scan(&createdAt, &lastMessageAt)

	if err != nil {
		return nil, fmt.Errorf("getting conversation timestamps: %w", err)
	}

	stats.Duration = lastMessageAt - createdAt

	return &stats, nil
}

// PruneConversation removes old turns from a conversation
func (s *SQLiteStore) PruneConversation(ctx context.Context, conversationID string, options *PruneOptions) (int, error) {
	if options == nil {
		options = &PruneOptions{}
	}

	// Build DELETE query with conditions
	query := `DELETE FROM conversation_turns WHERE conversation_id = ?`
	args := []interface{}{conversationID}

	if options.KeepLastN > 0 {
		// Keep the last N turns
		query = `
			DELETE FROM conversation_turns
			WHERE conversation_id = ? AND turn_number < (
				SELECT COALESCE(MAX(turn_number) - ?, 0) FROM conversation_turns WHERE conversation_id = ?
			)
		`
		args = []interface{}{conversationID, options.KeepLastN, conversationID}
	}

	if options.OlderThan > 0 {
		cutoff := time.Now().Add(-options.OlderThan).Unix()
		if options.KeepLastN > 0 {
			// Combine with KeepLastN condition
			query += `
				AND created_at < ?
			`
			args = append(args, cutoff)
		} else {
			query = `
				DELETE FROM conversation_turns
				WHERE conversation_id = ? AND created_at < ?
			`
			args = []interface{}{conversationID, cutoff}
		}
	}

	// Execute delete
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("pruning conversation: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	return int(rowsAffected), nil
}

// ArchiveConversation moves a completed conversation to archive
func (s *SQLiteStore) ArchiveConversation(ctx context.Context, conversationID string) error {
	// Update status to archived
	_, err := s.db.ExecContext(ctx, `
		UPDATE conversations SET status = 'archived', updated_at = ? WHERE id = ?
	`, time.Now().Unix(), conversationID)

	if err != nil {
		return fmt.Errorf("archiving conversation: %w", err)
	}

	return nil
}

// Helper functions

func generateConversationID() string {
	return fmt.Sprintf("conv_%s", uuid.New().String()[:8])
}

func generateTurnID() string {
	return fmt.Sprintf("turn_%s", uuid.New().String()[:8])
}

// sanitizeFTSQuery sanitizes FTS5 query to prevent injection
func sanitizeFTSQuery(query string) string {
	// Remove special characters that could break FTS5
	// Allow: alphanumeric, spaces, quotes, asterisk, parenthesis
	safe := strings.Builder{}
	for _, r := range query {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == ' ', r == '"', r == '*', r == '(', r == ')', r == '-', r == '+':
			safe.WriteRune(r)
		default:
			// Skip unsafe characters
		}
	}
	return safe.String()
}

// filterTurns filters turns based on build context options
func filterTurns(turns []*types.ConversationTurn, options *BuildContextOptions) []*types.ConversationTurn {
	var filtered []*types.ConversationTurn

	for _, turn := range turns {
		// Filter system messages
		if !options.IncludeSystem && turn.Role == types.ConversationRoleSystem {
			continue
		}

		// Filter tool messages
		if !options.IncludeTools && turn.Role == types.ConversationRoleTool {
			continue
		}

		filtered = append(filtered, turn)
	}

	return filtered
}

// Errors
var (
	ErrConversationNotFound = fmt.Errorf("conversation not found")
	ErrTurnNotFound        = fmt.Errorf("turn not found")
)
