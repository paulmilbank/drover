// Package test provides test utilities for Epic A
//
// This file is part of task-1767947032628722706 implementation
// Epic: epic-1767947014204239074 (Epic A)
// Description: Test task in epic A with detailed description
package test

import (
	"fmt"
	"testing"
)

// EpicATest represents a test case for Epic A functionality
type EpicATest struct {
	Name        string
	Description string
	Completed   bool
}

// Run executes the Epic A test
func (t *EpicATest) Run() error {
	t.Completed = true
	return nil
}

// TestEpicA verifies that Epic A tasks can be executed properly
func TestEpicA(t *testing.T) {
	test := &EpicATest{
		Name:        "Task in epic A",
		Description: "This is a test task in epic A with detailed description",
		Completed:   false,
	}

	if err := test.Run(); err != nil {
		t.Fatalf("Epic A test failed: %v", err)
	}

	if !test.Completed {
		t.Error("Expected test to be completed")
	}
}

// GetEpicATaskInfo returns information about the Epic A task
func GetEpicATaskInfo() map[string]interface{} {
	return map[string]interface{}{
		"task_id":     "task-1767947032628722706",
		"epic_id":     "epic-1767947014204239074",
		"title":       "Task in epic A",
		"description": "This is a test task in epic A with detailed description",
		"status":      "completed",
	}
}

// ExampleEpicATest_Run demonstrates how to use Epic A functionality
func ExampleEpicATest_Run() {
	test := &EpicATest{
		Name:        "Example Epic A Task",
		Description: "Demonstrates Epic A test implementation",
	}
	test.Run()
	fmt.Println("Epic A test completed successfully")
	// Output: Epic A test completed successfully
}
