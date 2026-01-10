// Package db tests for hierarchical task operations
package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloud-shuttle/drover/pkg/types"
)

// setupTestDB creates a temporary database for testing
func setupTestDB(t *testing.T) (*Store, string) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	if err := store.InitSchema(); err != nil {
		store.Close()
		t.Fatalf("Failed to initialize schema: %v", err)
	}

	return store, dbPath
}

func TestCreateSubTask(t *testing.T) {
	store, dbPath := setupTestDB(t)
	defer store.Close()
	defer os.Remove(dbPath)

	// Create a parent task first
	parent, err := store.CreateTask("Parent task", "Parent description", "", 0, nil)
	if err != nil {
		t.Fatalf("Failed to create parent task: %v", err)
	}

	// Create a sub-task
	subTask, err := store.CreateSubTask("Sub task 1", "Sub description", parent.ID, 0, nil)
	if err != nil {
		t.Fatalf("Failed to create sub-task: %v", err)
	}

	// Verify sub-task properties
	if subTask.ParentID != parent.ID {
		t.Errorf("Expected ParentID %s, got %s", parent.ID, subTask.ParentID)
	}
	if subTask.SequenceNumber != 1 {
		t.Errorf("Expected SequenceNumber 1, got %d", subTask.SequenceNumber)
	}
	if subTask.ID != parent.ID+".1" {
		t.Errorf("Expected ID %s.1, got %s", parent.ID, subTask.ID)
	}
	if subTask.EpicID != parent.EpicID {
		t.Errorf("Expected EpicID %s, got %s", parent.EpicID, subTask.EpicID)
	}

	// Create another sub-task to test sequence numbering
	subTask2, err := store.CreateSubTask("Sub task 2", "Sub description 2", parent.ID, 0, nil)
	if err != nil {
		t.Fatalf("Failed to create second sub-task: %v", err)
	}

	if subTask2.SequenceNumber != 2 {
		t.Errorf("Expected SequenceNumber 2, got %d", subTask2.SequenceNumber)
	}
	if subTask2.ID != parent.ID+".2" {
		t.Errorf("Expected ID %s.2, got %s", parent.ID, subTask2.ID)
	}
}

func TestCreateSubTaskMaxDepth(t *testing.T) {
	store, dbPath := setupTestDB(t)
	defer store.Close()
	defer os.Remove(dbPath)

	// Create parent task
	parent, err := store.CreateTask("Parent task", "Parent description", "", 0, nil)
	if err != nil {
		t.Fatalf("Failed to create parent task: %v", err)
	}

	// Create a sub-task
	subTask, err := store.CreateSubTask("Sub task 1", "Sub description", parent.ID, 0, nil)
	if err != nil {
		t.Fatalf("Failed to create sub-task: %v", err)
	}

	// Try to create a sub-task of the sub-task (should fail - max depth is 2)
	_, err = store.CreateSubTask("Sub-sub task", "This should fail", subTask.ID, 0, nil)
	if err == nil {
		t.Error("Expected error when creating sub-task of sub-task (max depth exceeded)")
	}
}

func TestCreateSubTaskNonExistentParent(t *testing.T) {
	store, dbPath := setupTestDB(t)
	defer store.Close()
	defer os.Remove(dbPath)

	// Try to create a sub-task with non-existent parent
	_, err := store.CreateSubTask("Sub task", "Sub description", "non-existent-id", 0, nil)
	if err == nil {
		t.Error("Expected error when creating sub-task with non-existent parent")
	}
}

func TestGetSubTasks(t *testing.T) {
	store, dbPath := setupTestDB(t)
	defer store.Close()
	defer os.Remove(dbPath)

	// Create parent task
	parent, err := store.CreateTask("Parent task", "Parent description", "", 0, nil)
	if err != nil {
		t.Fatalf("Failed to create parent task: %v", err)
	}

	// Create multiple sub-tasks
	subTask1, _ := store.CreateSubTask("Sub task 1", "Desc 1", parent.ID, 0, nil)
	subTask2, _ := store.CreateSubTask("Sub task 2", "Desc 2", parent.ID, 0, nil)
	subTask3, _ := store.CreateSubTask("Sub task 3", "Desc 3", parent.ID, 0, nil)

	// Get sub-tasks
	subTasks, err := store.GetSubTasks(parent.ID)
	if err != nil {
		t.Fatalf("Failed to get sub-tasks: %v", err)
	}

	// Verify count
	if len(subTasks) != 3 {
		t.Errorf("Expected 3 sub-tasks, got %d", len(subTasks))
	}

	// Verify ordering (should be by sequence_number)
	if subTasks[0].ID != subTask1.ID {
		t.Errorf("Expected first sub-task %s, got %s", subTask1.ID, subTasks[0].ID)
	}
	if subTasks[1].ID != subTask2.ID {
		t.Errorf("Expected second sub-task %s, got %s", subTask2.ID, subTasks[1].ID)
	}
	if subTasks[2].ID != subTask3.ID {
		t.Errorf("Expected third sub-task %s, got %s", subTask3.ID, subTasks[2].ID)
	}

	// Get sub-tasks of a task with no children
	emptySubTasks, err := store.GetSubTasks(subTask1.ID)
	if err != nil {
		t.Fatalf("Failed to get sub-tasks: %v", err)
	}
	if len(emptySubTasks) != 0 {
		t.Errorf("Expected 0 sub-tasks, got %d", len(emptySubTasks))
	}
}

func TestHasSubTasks(t *testing.T) {
	store, dbPath := setupTestDB(t)
	defer store.Close()
	defer os.Remove(dbPath)

	// Create parent task
	parent, err := store.CreateTask("Parent task", "Parent description", "", 0, nil)
	if err != nil {
		t.Fatalf("Failed to create parent task: %v", err)
	}

	// Initially, no sub-tasks
	hasChildren, err := store.HasSubTasks(parent.ID)
	if err != nil {
		t.Fatalf("Failed to check for sub-tasks: %v", err)
	}
	if hasChildren {
		t.Error("Expected hasChildren to be false initially")
	}

	// Add a sub-task
	_, err = store.CreateSubTask("Sub task 1", "Desc 1", parent.ID, 0, nil)
	if err != nil {
		t.Fatalf("Failed to create sub-task: %v", err)
	}

	// Now should have sub-tasks
	hasChildren, err = store.HasSubTasks(parent.ID)
	if err != nil {
		t.Fatalf("Failed to check for sub-tasks: %v", err)
	}
	if !hasChildren {
		t.Error("Expected hasChildren to be true after adding sub-task")
	}
}

func TestGetParentTask(t *testing.T) {
	store, dbPath := setupTestDB(t)
	defer store.Close()
	defer os.Remove(dbPath)

	// Create parent task
	parent, err := store.CreateTask("Parent task", "Parent description", "", 0, nil)
	if err != nil {
		t.Fatalf("Failed to create parent task: %v", err)
	}

	// Create sub-task
	subTask, err := store.CreateSubTask("Sub task 1", "Desc 1", parent.ID, 0, nil)
	if err != nil {
		t.Fatalf("Failed to create sub-task: %v", err)
	}

	// Get parent from sub-task
	retrievedParent, err := store.GetParentTask(subTask.ID)
	if err != nil {
		t.Fatalf("Failed to get parent task: %v", err)
	}

	if retrievedParent.ID != parent.ID {
		t.Errorf("Expected parent ID %s, got %s", parent.ID, retrievedParent.ID)
	}

	// Try to get parent of a parent task (should fail)
	_, err = store.GetParentTask(parent.ID)
	if err == nil {
		t.Error("Expected error when getting parent of a task with no parent")
	}
}

func TestClaimTaskExcludesSubTasks(t *testing.T) {
	store, dbPath := setupTestDB(t)
	defer store.Close()
	defer os.Remove(dbPath)

	// Create parent task
	parent, err := store.CreateTask("Parent task", "Parent description", "", 0, nil)
	if err != nil {
		t.Fatalf("Failed to create parent task: %v", err)
	}

	// Create sub-task (should start as ready)
	_, err = store.CreateSubTask("Sub task 1", "Desc 1", parent.ID, 0, nil)
	if err != nil {
		t.Fatalf("Failed to create sub-task: %v", err)
	}

	// Try to claim - should get the parent task, not the sub-task
	claimed, err := store.ClaimTask("test-worker")
	if err != nil {
		t.Fatalf("Failed to claim task: %v", err)
	}

	if claimed == nil {
		t.Fatal("Expected to claim a task, got nil")
	}

	if claimed.ID != parent.ID {
		t.Errorf("Expected to claim parent task %s, got %s", parent.ID, claimed.ID)
	}

	// Verify sub-task was not claimed
	subTaskStatus, err := store.GetTaskStatus(parent.ID + ".1")
	if err != nil {
		t.Fatalf("Failed to get sub-task status: %v", err)
	}

	if subTaskStatus != types.TaskStatusReady {
		t.Errorf("Expected sub-task status 'ready', got '%s'", subTaskStatus)
	}
}

func TestTaskPersistenceWithHierarchy(t *testing.T) {
	store, dbPath := setupTestDB(t)
	defer store.Close()
	defer os.Remove(dbPath)

	// Create an epic first
	epic, err := store.CreateEpic("Test Epic", "Epic description")
	if err != nil {
		t.Fatalf("Failed to create epic: %v", err)
	}

	// Create parent task with epic
	parent, err := store.CreateTask("Parent task", "Parent description", epic.ID, 5, nil)
	if err != nil {
		t.Fatalf("Failed to create parent task: %v", err)
	}

	// Create sub-task
	subTask, err := store.CreateSubTask("Sub task 1", "Sub description", parent.ID, 3, nil)
	if err != nil {
		t.Fatalf("Failed to create sub-task: %v", err)
	}

	// Close and reopen database
	store.Close()
	store, err = Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to reopen database: %v", err)
	}
	defer store.Close()

	// Retrieve and verify parent
	retrievedParent, err := store.GetTask(parent.ID)
	if err != nil {
		t.Fatalf("Failed to retrieve parent task: %v", err)
	}

	if retrievedParent.ID != parent.ID {
		t.Errorf("Parent ID mismatch: expected %s, got %s", parent.ID, retrievedParent.ID)
	}

	// Retrieve and verify sub-task
	retrievedSubTask, err := store.GetTask(subTask.ID)
	if err != nil {
		t.Fatalf("Failed to retrieve sub-task: %v", err)
	}

	if retrievedSubTask.ParentID != parent.ID {
		t.Errorf("Sub-task ParentID mismatch: expected %s, got %s", parent.ID, retrievedSubTask.ParentID)
	}
	if retrievedSubTask.SequenceNumber != 1 {
		t.Errorf("Sub-task SequenceNumber mismatch: expected 1, got %d", retrievedSubTask.SequenceNumber)
	}
	if retrievedSubTask.EpicID != epic.ID {
		t.Errorf("Sub-task EpicID mismatch: expected %s, got %s", epic.ID, retrievedSubTask.EpicID)
	}
	if retrievedSubTask.Priority != 3 {
		t.Errorf("Sub-task Priority mismatch: expected 3, got %d", retrievedSubTask.Priority)
	}
}
