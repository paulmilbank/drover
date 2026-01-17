// Package events provides real-time event streaming for task lifecycle events
package events

import (
	"encoding/json"
	"time"
)

// EventType represents the type of event
type EventType string

const (
	// EventTaskStarted is emitted when a task begins execution
	EventTaskStarted EventType = "task.started"
	// EventTaskCompleted is emitted when a task completes successfully
	EventTaskCompleted EventType = "task.completed"
	// EventTaskFailed is emitted when a task fails
	EventTaskFailed EventType = "task.failed"
	// EventTaskBlocked is emitted when a task is blocked by dependencies
	EventTaskBlocked EventType = "task.blocked"
	// EventTaskUnblocked is emitted when a blocked task's dependencies are resolved
	EventTaskUnblocked EventType = "task.unblocked"
	// EventTaskCancelled is emitted when a task is cancelled
	EventTaskCancelled EventType = "task.cancelled"
	// EventTaskClaimed is emitted when a worker claims a task
	EventTaskClaimed EventType = "task.claimed"
	// EventTaskPaused is emitted when a task is paused
	EventTaskPaused EventType = "task.paused"
	// EventTaskResumed is emitted when a paused task is resumed
	EventTaskResumed EventType = "task.resumed"
)

// Event represents a single task lifecycle event
type Event struct {
	ID        string              `json:"id" db:"id"`
	Type      EventType           `json:"type" db:"type"`
	Timestamp int64               `json:"timestamp" db:"timestamp"`
	TaskID    string              `json:"task_id" db:"task_id"`
	EpicID    string              `json:"epic_id,omitempty" db:"epic_id"`
	Data      map[string]any      `json:"data,omitempty" db:"data"` // JSON encoded
}

// MarshalData converts the Data map to JSON for storage
func (e *Event) MarshalData() ([]byte, error) {
	if len(e.Data) == 0 {
		return nil, nil
	}
	return json.Marshal(e.Data)
}

// UnmarshalData parses JSON data into the Data map
func (e *Event) UnmarshalData(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, &e.Data)
}

// NewEvent creates a new event with the current timestamp
func NewEvent(eventType EventType, taskID, epicID string, data map[string]any) *Event {
	return &Event{
		Type:      eventType,
		Timestamp: time.Now().Unix(),
		TaskID:    taskID,
		EpicID:    epicID,
		Data:      data,
	}
}

// EventFilter defines filters for querying events
type EventFilter struct {
	Types    []EventType `json:"types,omitempty"`
	EpicID   string      `json:"epic_id,omitempty"`
	TaskID   string      `json:"task_id,omitempty"`
	Since    int64       `json:"since,omitempty"`    // Unix timestamp
	Until    int64       `json:"until,omitempty"`    // Unix timestamp
	Limit    int         `json:"limit,omitempty"`    // Max events to return
	Follow   bool        `json:"follow,omitempty"`   // Whether to follow new events (streaming mode)
}

// StreamOptions configures the event stream
type StreamOptions struct {
	Filter    EventFilter
	JSONLines bool   // Output in JSONL format (one JSON object per line)
	Quiet     bool   // Suppress non-event messages
}
