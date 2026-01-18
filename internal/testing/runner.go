// Package testing provides automated test execution for Drover tasks
package testing

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TestMode defines how test failures are handled
type TestMode string

const (
	// TestModeStrict means test failures block task completion
	TestModeStrict TestMode = "strict"
	// TestModeLenient means test failures are logged but don't block completion
	TestModeLenient TestMode = "lenient"
	// TestModeDisabled means tests are not run
	TestModeDisabled TestMode = "disabled"
)

// TestScope defines which tests to run
type TestScope string

const (
	// TestScopeAll runs all project tests
	TestScopeAll TestScope = "all"
	// TestScopeDiff runs only tests affected by changed files
	TestScopeDiff TestScope = "diff"
	// TestScopeSkip skips running tests if no changes detected
	TestScopeSkip TestScope = "skip"
)

// TestConfig configures automated test execution
type TestConfig struct {
	Mode        TestMode `json:"mode"`                  // How to handle test failures
	Scope       TestScope `json:"scope"`                 // Which tests to run
	Timeout     time.Duration `json:"timeout"`           // Maximum time to wait for tests
	Command     string   `json:"command,omitempty"`      // Custom test command (optional)
}

// DefaultTestConfig returns the default test configuration
func DefaultTestConfig() *TestConfig {
	return &TestConfig{
		Mode:    TestModeStrict,
		Scope:   TestScopeDiff,
		Timeout: 5 * time.Minute,
	}
}

// TestResult represents the outcome of running tests
type TestResult struct {
	Success    bool          `json:"success"`
	Passed     int           `json:"passed"`
	Failed     int           `json:"failed"`
	Skipped    int           `json:"skipped"`
	Total      int           `json:"total"`
	Duration   time.Duration `json:"duration"`
	Output     string        `json:"output"`
	Error      string        `json:"error,omitempty"`
	RunTests   bool          `json:"run_tests"`   // Whether tests were actually run
}

// Runner executes tests based on configuration
type Runner struct {
	config      *TestConfig
	baseDir     string // Base repository directory
	verbose     bool
}

// NewRunner creates a new test runner
func NewRunner(config *TestConfig, baseDir string) *Runner {
	if config == nil {
		config = DefaultTestConfig()
	}
	return &Runner{
		config:  config,
		baseDir: baseDir,
		verbose: false,
	}
}

// SetVerbose enables or disables verbose logging
func (r *Runner) SetVerbose(v bool) {
	r.verbose = v
}

// Run executes tests for a task in the given worktree directory
func (r *Runner) Run(worktreePath string, taskID string) *TestResult {
	result := &TestResult{
		Success:  true,
		RunTests: false,
	}

	start := time.Now()
	defer func() {
		result.Duration = time.Since(start)
	}()

	// Check if tests should be disabled
	if r.config.Mode == TestModeDisabled {
		if r.verbose {
			log.Printf("🧪 Tests disabled for task %s", taskID)
		}
		return result
	}

	// Determine which tests to run based on scope
	shouldRun, err := r.shouldRunTests(worktreePath)
	if err != nil {
		result.Error = fmt.Sprintf("checking test scope: %v", err)
		result.Success = r.config.Mode != TestModeStrict
		return result
	}

	if !shouldRun {
		if r.verbose {
			log.Printf("🧪 Skipping tests for task %s (no relevant changes)", taskID)
		}
		return result
	}

	result.RunTests = true

	if r.verbose {
		log.Printf("🧪 Running tests for task %s (mode: %s, scope: %s)", taskID, r.config.Mode, r.config.Scope)
	}

	// Get the test command to run
	cmd, args, err := r.getTestCommand(worktreePath)
	if err != nil {
		result.Error = fmt.Sprintf("determining test command: %v", err)
		result.Success = r.config.Mode != TestModeStrict
		return result
	}

	// Run the tests
	output, err := r.runCommand(worktreePath, cmd, args)
	result.Output = output

	if err != nil {
		result.Success = false
		result.Error = err.Error()

		// Parse test output for counts
		result.Passed, result.Failed, result.Skipped = r.parseTestOutput(output)
		result.Total = result.Passed + result.Failed + result.Skipped

		// Log failure
		if r.config.Mode == TestModeStrict {
			log.Printf("❌ Tests failed for task %s: %d passed, %d failed, %d skipped (blocking completion)",
				taskID, result.Passed, result.Failed, result.Skipped)
		} else {
			log.Printf("❌ Tests failed for task %s: %d passed, %d failed, %d skipped (warning only)",
				taskID, result.Passed, result.Failed, result.Skipped)
		}

		// In lenient mode, we still consider the result "successful" for the workflow
		if r.config.Mode == TestModeLenient {
			result.Success = true
		}

		return result
	}

	result.Success = true
	result.Passed, result.Failed, result.Skipped = r.parseTestOutput(output)
	result.Total = result.Passed + result.Failed + result.Skipped

	log.Printf("✅ Tests passed for task %s: %d passed, %d failed, %d skipped",
		taskID, result.Passed, result.Failed, result.Skipped)

	return result
}

// shouldRunTests determines if tests should be run based on scope configuration
func (r *Runner) shouldRunTests(worktreePath string) (bool, error) {
	switch r.config.Scope {
	case TestScopeAll:
		return true, nil
	case TestScopeSkip:
		// Check if there are any changes
		return r.hasChanges(worktreePath)
	case TestScopeDiff:
		// Check if there are any changes (simplified - could be smarter)
		return r.hasChanges(worktreePath)
	default:
		return true, nil
	}
}

// hasChanges checks if the worktree has any changes compared to main
func (r *Runner) hasChanges(worktreePath string) (bool, error) {
	cmd := exec.Command("git", "diff", "--quiet", "main")
	cmd.Dir = worktreePath
	err := cmd.Run()

	// git diff --quiet returns exit code 1 if there are differences
	if err != nil {
		if cmd.ProcessState.ExitCode() == 1 {
			return true, nil
		}
		return false, fmt.Errorf("checking for changes: %w", err)
	}

	return false, nil
}

// getTestCommand determines the test command and arguments based on the project
func (r *Runner) getTestCommand(worktreePath string) (string, []string, error) {
	// If a custom command is specified, use it
	if r.config.Command != "" {
		parts := strings.Fields(r.config.Command)
		if len(parts) == 0 {
			return "", nil, fmt.Errorf("empty test command")
		}
		return parts[0], parts[1:], nil
	}

	// Auto-detect the project type and use appropriate test command
	if r.hasFile(worktreePath, "go.mod") {
		return "go", []string{"test", "-v", "./..."}, nil
	}
	if r.hasFile(worktreePath, "package.json") {
		// Check for test scripts
		pkgJSON := filepath.Join(worktreePath, "package.json")
		if content, err := os.ReadFile(pkgJSON); err == nil {
			contentStr := string(content)
			if strings.Contains(contentStr, "\"test\"") {
				// Use npm test
				return "npm", []string{"test"}, nil
			}
		}
	}
	if r.hasFile(worktreePath, "Cargo.toml") {
		return "cargo", []string{"test"}, nil
	}
	if r.hasFile(worktreePath, "pyproject.toml") || r.hasFile(worktreePath, "setup.py") {
		return "python", []string{"-m", "pytest"}, nil
	}

	return "", nil, fmt.Errorf("unable to determine test command for project")
}

// hasFile checks if a file exists in the worktree
func (r *Runner) hasFile(worktreePath, filename string) bool {
	path := filepath.Join(worktreePath, filename)
	_, err := os.Stat(path)
	return err == nil
}

// runCommand executes a command in the worktree and returns its output
func (r *Runner) runCommand(worktreePath string, cmd string, args []string) (string, error) {
	var stdout, stderr bytes.Buffer

	command := exec.Command(cmd, args...)
	command.Dir = worktreePath
	command.Stdout = &stdout
	command.Stderr = &stderr

	if r.config.Timeout > 0 {
		var cancel func()
		ctx, cancel := context.WithTimeout(context.Background(), r.config.Timeout)
		defer cancel()
		command = exec.CommandContext(ctx, cmd, args...)
		command.Dir = worktreePath
		command.Stdout = &stdout
		command.Stderr = &stderr
	}

	err := command.Run()

	output := stdout.String()
	if stderr.String() != "" {
		output += "\n" + stderr.String()
	}

	return output, err
}

// parseTestOutput attempts to parse test counts from the output
func (r *Runner) parseTestOutput(output string) (passed, failed, skipped int) {
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		line = strings.ToLower(line)

		// Go test format
		if strings.Contains(line, "pass") || strings.Contains(line, "ok") {
			if strings.Contains(line, "passed") {
				fmt.Sscanf(line, "%d passed", &passed)
			}
		}
		if strings.Contains(line, "fail") {
			fmt.Sscanf(line, "%d failed", &failed)
		}

		// Common test formats
		if strings.Contains(line, "tests passed") || strings.Contains(line, "test(s) passed") {
			fmt.Sscanf(line, "%d test", &passed)
		}
		if strings.Contains(line, "tests failed") || strings.Contains(line, "test(s) failed") {
			fmt.Sscanf(line, "%d test", &failed)
		}
		if strings.Contains(line, "tests skipped") || strings.Contains(line, "test(s) skipped") {
			fmt.Sscanf(line, "%d test", &skipped)
		}

		// Python pytest format
		if strings.Contains(line, "passed") && strings.Contains(line, "failed") {
			fmt.Sscanf(line, "%d passed", &passed)
			fmt.Sscanf(line, "%d failed", &failed)
		}
		if strings.Contains(line, "skipped") {
			fmt.Sscanf(line, "%d skipped", &skipped)
		}

		// Cargo format
		if strings.Contains(line, "test result:") {
			if strings.Contains(line, "ok") {
				// Parse "ok. <n> passed" format
				fmt.Sscanf(line, "test result: ok. %d passed", &passed)
			}
		}
	}

	// If we couldn't parse, check for pass/fail indicators in output
	if passed == 0 && failed == 0 {
		if strings.Contains(output, "PASS") || strings.Contains(output, "pass") {
			passed = 1 // At least one test passed
		}
		if strings.Contains(output, "FAIL") || strings.Contains(output, "fail") {
			failed = 1 // At least one test failed
		}
	}

	return passed, failed, skipped
}
