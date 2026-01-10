// Package workflow tests for sub-task execution
package workflow

import (
	"path/filepath"
	"testing"

	"github.com/cloud-shuttle/drover/internal/db"
	"github.com/cloud-shuttle/drover/pkg/types"
)

// TestSubTaskExecutionFlow tests that sub-tasks execute when parent runs
func TestSubTaskExecutionFlow(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(); err != nil {
		t.Fatalf("Failed to initialize schema: %v", err)
	}

	// Create a parent task with sub-tasks
	parent, err := store.CreateTask("Parent task", "Parent description", "", 0, nil)
	if err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	sub1, _ := store.CreateSubTask("Sub task 1", "Desc 1", parent.ID, 0, nil)
	sub2, _ := store.CreateSubTask("Sub task 2", "Desc 2", parent.ID, 0, nil)
	sub3, _ := store.CreateSubTask("Sub task 3", "Desc 3", parent.ID, 0, nil)

	// 1. Verify HasSubTasks works
	hasChildren, err := store.HasSubTasks(parent.ID)
	if err != nil {
		t.Fatalf("HasSubTasks failed: %v", err)
	}
	if !hasChildren {
		t.Error("Expected HasSubTasks to return true")
	}

	// 2. Verify GetSubTasks returns in order
	subTasks, err := store.GetSubTasks(parent.ID)
	if err != nil {
		t.Fatalf("GetSubTasks failed: %v", err)
	}
	if len(subTasks) != 3 {
		t.Fatalf("Expected 3 sub-tasks, got %d", len(subTasks))
	}
	// Verify order
	if subTasks[0].ID != sub1.ID || subTasks[1].ID != sub2.ID || subTasks[2].ID != sub3.ID {
		t.Error("Sub-tasks not in correct order")
	}

	// 3. Simulate the execution flow
	// When parent is claimed, mark it as in_progress
	err = store.UpdateTaskStatus(parent.ID, types.TaskStatusInProgress, "")
	if err != nil {
		t.Fatalf("Failed to update status: %v", err)
	}

	// Check sub-tasks are still ready (they haven't run yet)
	for _, st := range subTasks {
		status, _ := store.GetTaskStatus(st.ID)
		if status != types.TaskStatusReady {
			t.Errorf("Sub-task %s should be ready, got %s", st.ID, status)
		}
	}

	// 4. Simulate sub-tasks executing
	// Mark each sub-task as completed (simulating successful execution)
	for _, st := range subTasks {
		err = store.UpdateTaskStatus(st.ID, types.TaskStatusCompleted, "")
		if err != nil {
			t.Fatalf("Failed to mark sub-task complete: %v", err)
		}
	}

	// 5. Now mark parent as completed
	err = store.UpdateTaskStatus(parent.ID, types.TaskStatusCompleted, "")
	if err != nil {
		t.Fatalf("Failed to mark parent complete: %v", err)
	}

	// 6. Verify final state
	parentStatus, _ := store.GetTaskStatus(parent.ID)
	if parentStatus != types.TaskStatusCompleted {
		t.Errorf("Parent should be completed, got %s", parentStatus)
	}

	for _, st := range subTasks {
		status, _ := store.GetTaskStatus(st.ID)
		if status != types.TaskStatusCompleted {
			t.Errorf("Sub-task %s should be completed, got %s", st.ID, status)
		}
	}
}

// TestSubTaskFailurePropagation tests that parent fails if sub-task fails
func TestSubTaskFailurePropagation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(); err != nil {
		t.Fatalf("Failed to initialize schema: %v", err)
	}

	// Create parent with sub-tasks
	parent, _ := store.CreateTask("Parent", "", "", 0, nil)
	_, _ = store.CreateSubTask("Sub 1", "", parent.ID, 0, nil)
	_, _ = store.CreateSubTask("Sub 2", "", parent.ID, 0, nil)
	_, _ = store.CreateSubTask("Sub 3", "", parent.ID, 0, nil)

	// Mark parent as in_progress
	store.UpdateTaskStatus(parent.ID, types.TaskStatusInProgress, "")

	// Mark first sub-task as failed
	store.UpdateTaskStatus(parent.ID+".1", types.TaskStatusFailed, "Simulated failure")

	// Verify other sub-tasks are still ready
	status, _ := store.GetTaskStatus(parent.ID + ".2")
	if status != types.TaskStatusReady {
		t.Errorf("Sub-task 2 should still be ready, got %s", status)
	}

	// Mark parent as failed (simulating what the orchestrator would do)
	store.UpdateTaskStatus(parent.ID, types.TaskStatusFailed, "Sub-task failed")

	// Verify parent is failed
	parentStatus, _ := store.GetTaskStatus(parent.ID)
	if parentStatus != types.TaskStatusFailed {
		t.Errorf("Parent should be failed, got %s", parentStatus)
	}
}

// TestSubTaskSequencing tests that sequence numbers are assigned correctly
func TestSubTaskSequencing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer store.Close()

	if err := store.InitSchema(); err != nil {
		t.Fatalf("Failed to initialize schema: %v", err)
	}

	parent, _ := store.CreateTask("Parent", "", "", 0, nil)

	// Create sub-tasks with --parent flag (auto-sequencing)
	s1, _ := store.CreateSubTask("Auto 1", "", parent.ID, 0, nil)
	s2, _ := store.CreateSubTask("Auto 2", "", parent.ID, 0, nil)
	s3, _ := store.CreateSubTask("Auto 3", "", parent.ID, 0, nil)

	if s1.SequenceNumber != 1 {
		t.Errorf("Expected sequence 1, got %d", s1.SequenceNumber)
	}
	if s2.SequenceNumber != 2 {
		t.Errorf("Expected sequence 2, got %d", s2.SequenceNumber)
	}
	if s3.SequenceNumber != 3 {
		t.Errorf("Expected sequence 3, got %d", s3.SequenceNumber)
	}

	// Create sub-task with specific sequence (via hierarchical syntax)
	s10, _ := store.CreateSubTaskWithSequence("Manual 10", "", parent.ID, 10, 0, nil)
	s20, _ := store.CreateSubTaskWithSequence("Manual 20", "", parent.ID, 20, 0, nil)

	if s10.SequenceNumber != 10 {
		t.Errorf("Expected sequence 10, got %d", s10.SequenceNumber)
	}
	if s20.SequenceNumber != 20 {
		t.Errorf("Expected sequence 20, got %d", s20.SequenceNumber)
	}

	// Verify IDs
	if s10.ID != parent.ID+".10" {
		t.Errorf("Expected ID %s.10, got %s", parent.ID, s10.ID)
	}
	if s20.ID != parent.ID+".20" {
		t.Errorf("Expected ID %s.20, got %s", parent.ID, s20.ID)
	}
}
