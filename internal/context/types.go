// Package context provides context window management for AI agents
package context

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloud-shuttle/drover/internal/project"
)

// ContentType represents the type of content being managed
type ContentType string

const (
	ContentTypeDescription ContentType = "description"
	ContentTypeDiff        ContentType = "diff"
	ContentTypeFile        ContentType = "file"
)

// ContentThresholds defines size limits for different content types
type ContentThresholds struct {
	MaxDescriptionSize project.ByteSize // Default: 250MB
	MaxDiffSize       project.ByteSize // Default: 250MB
	MaxFileSize       project.ByteSize // Default: 1MB
}

// DefaultThresholds returns the default content size thresholds
func DefaultThresholds() *ContentThresholds {
	return &ContentThresholds{
		MaxDescriptionSize: 250 * 1024 * 1024, // 250MB
		MaxDiffSize:       250 * 1024 * 1024, // 250MB
		MaxFileSize:       1024 * 1024,       // 1MB
	}
}

// ThresholdsFromConfig creates thresholds from project configuration
func ThresholdsFromConfig(cfg *project.Config) *ContentThresholds {
	return &ContentThresholds{
		MaxDescriptionSize: cfg.MaxDescriptionSize,
		MaxDiffSize:       cfg.MaxDiffSize,
		MaxFileSize:       cfg.MaxFileSize,
	}
}

// ContentInfo holds information about content that may be too large
type ContentInfo struct {
	Type      ContentType
	Path      string // File path or description
	Content   string
	Size      int64  // Size in bytes
	Threshold int64  // Size threshold in bytes
}

// ShouldUseReference returns true if content exceeds its threshold
func (c *ContentInfo) ShouldUseReference() bool {
	return c.Size > c.Threshold
}

// Manager handles context window management
type Manager struct {
	thresholds *ContentThresholds
}

// NewManager creates a new context manager with default thresholds
func NewManager() *Manager {
	return &Manager{
		thresholds: DefaultThresholds(),
	}
}

// NewManagerWithConfig creates a new context manager with configuration thresholds
func NewManagerWithConfig(cfg *project.Config) *Manager {
	return &Manager{
		thresholds: ThresholdsFromConfig(cfg),
	}
}

// NewManagerWithThresholds creates a new context manager with custom thresholds
func NewManagerWithThresholds(thresholds *ContentThresholds) *Manager {
	return &Manager{
		thresholds: thresholds,
	}
}

// GetThresholds returns the current thresholds
func (m *Manager) GetThresholds() *ContentThresholds {
	return m.thresholds
}

// ManageContent checks if content exceeds thresholds and returns either
// the content or a reference to it
func (m *Manager) ManageContent(contentType ContentType, path, content string) string {
	info := &ContentInfo{
		Type:      contentType,
		Path:      path,
		Content:   content,
		Size:      int64(len(content)),
		Threshold: m.getThreshold(contentType),
	}

	if info.ShouldUseReference() {
		return m.formatAsReference(info)
	}

	return content
}

// ManageDescription manages task description content
func (m *Manager) ManageDescription(description string) string {
	return m.ManageContent(ContentTypeDescription, "description", description)
}

// ManageDiff manages git diff content
func (m *Manager) ManageDiff(diff string) string {
	return m.ManageContent(ContentTypeDiff, "diff", diff)
}

// ManageFile manages file content
func (m *Manager) ManageFile(filePath, content string) string {
	return m.ManageContent(ContentTypeFile, filePath, content)
}

// getThreshold returns the size threshold for a content type
func (m *Manager) getThreshold(contentType ContentType) int64 {
	switch contentType {
	case ContentTypeDescription:
		return m.thresholds.MaxDescriptionSize.Bytes()
	case ContentTypeDiff:
		return m.thresholds.MaxDiffSize.Bytes()
	case ContentTypeFile:
		return m.thresholds.MaxFileSize.Bytes()
	default:
		return m.thresholds.MaxFileSize.Bytes()
	}
}

// formatAsReference formats content as a reference when it exceeds thresholds
func (m *Manager) formatAsReference(info *ContentInfo) string {
	var ref strings.Builder

	// Format reference table
	ref.WriteString("| Type | Path/SHA | Size | Fetch Command |\n")
	ref.WriteString("|------|----------|------|---------------|\n")

	switch info.Type {
	case ContentTypeFile:
		displayPath := info.Path
		if len(displayPath) > 50 {
			displayPath = "..." + displayPath[len(displayPath)-47:]
		}
		ref.WriteString(fmt.Sprintf("| file | %s | %s | `cat %s` |\n",
			displayPath, formatSize(info.Size), info.Path))

	case ContentTypeDiff:
		// For diffs, show SHA reference if available, otherwise show generic
		ref.WriteString(fmt.Sprintf("| diff | %s | %s | `git show %s` |\n",
			info.Path, formatSize(info.Size), info.Path))

	case ContentTypeDescription:
		ref.WriteString(fmt.Sprintf("| description | (task) | %s | See task description |\n",
			formatSize(info.Size)))
	}

	return ref.String()
}

// TruncateContent truncates content to fit within a size limit
// This is used as a fallback when references aren't appropriate
func (m *Manager) TruncateContent(content string, maxBytes int) string {
	if len(content) <= maxBytes {
		return content
	}

	// Reserve space for truncation message
	reserve := 100
	if maxBytes < reserve {
		maxBytes = 100
	} else {
		maxBytes -= reserve
	}

	truncated := content[:maxBytes]

	// Try to break at a reasonable point
	// Look for newline, then period, then space
	breakPoints := []string{"\n\n", "\n", ". ", " ", ""}
	for _, bp := range breakPoints {
		if idx := strings.LastIndex(truncated, bp); idx > maxBytes/2 {
			truncated = truncated[:idx+len(bp)]
			break
		}
	}

	truncated = strings.TrimSpace(truncated)
	return truncated + fmt.Sprintf("\n\n[... %d more bytes, see full content in workspace ...]", len(content)-maxBytes)
}

// EstimateTokens estimates the number of tokens in content
// Rough estimate: ~4 characters per token for English text
func EstimateTokens(content string) int {
	return len(content) / 4
}

// formatSize returns a human-readable size string
func formatSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	}
	if size < 1024*1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
	}
	return fmt.Sprintf("%.1fGB", float64(size)/(1024*1024*1024))
}

// GetRelativePath returns a relative path from base to target
func GetRelativePath(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	if rel == "." {
		return filepath.Base(target)
	}
	return rel
}

// CombineContext intelligently combines multiple content pieces
// with size awareness
func (m *Manager) CombineContext(pieces []string, maxTotalBytes int64) string {
	var combined strings.Builder
	currentSize := int64(0)

	for i, piece := range pieces {
		pieceSize := int64(len(piece))

		// Check if adding this piece would exceed the limit
		if currentSize+pieceSize > maxTotalBytes {
			// Add truncation notice
			remaining := len(pieces) - i
			combined.WriteString(fmt.Sprintf("\n\n[... %d more content items omitted due to size limits ...]", remaining))
			break
		}

		if i > 0 {
			combined.WriteString("\n\n")
		}
		combined.WriteString(piece)
		currentSize += pieceSize
	}

	return combined.String()
}

// ContentTooLarge returns true if content exceeds its threshold
func (m *Manager) ContentTooLarge(contentType ContentType, content string) bool {
	info := &ContentInfo{
		Type:      contentType,
		Size:      int64(len(content)),
		Threshold: m.getThreshold(contentType),
	}
	return info.ShouldUseReference()
}
