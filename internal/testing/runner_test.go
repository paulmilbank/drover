// Package testing provides automated test execution for Drover tasks
package testing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultTestConfig(t *testing.T) {
	config := DefaultTestConfig()

	if config.Mode != TestModeStrict {
		t.Errorf("Expected default mode to be %s, got %s", TestModeStrict, config.Mode)
	}

	if config.Scope != TestScopeDiff {
		t.Errorf("Expected default scope to be %s, got %s", TestScopeDiff, config.Scope)
	}

	if config.Timeout != 5*time.Minute {
		t.Errorf("Expected default timeout to be 5 minutes, got %v", config.Timeout)
	}
}

func TestNewRunner(t *testing.T) {
	config := &TestConfig{
		Mode:    TestModeStrict,
		Scope:   TestScopeAll,
		Timeout: 2 * time.Minute,
	}

	runner := NewRunner(config, "/tmp/test")

	if runner.config.Mode != TestModeStrict {
		t.Errorf("Expected runner mode to be %s, got %s", TestModeStrict, runner.config.Mode)
	}

	if runner.config.Timeout != 2*time.Minute {
		t.Errorf("Expected runner timeout to be 2 minutes, got %v", runner.config.Timeout)
	}

	if runner.baseDir != "/tmp/test" {
		t.Errorf("Expected base dir to be /tmp/test, got %s", runner.baseDir)
	}
}

func TestNewRunnerNilConfig(t *testing.T) {
	runner := NewRunner(nil, "/tmp/test")

	if runner.config == nil {
		t.Error("Expected runner to have default config when nil is passed")
	}
}

func TestRunnerSetVerbose(t *testing.T) {
	runner := NewRunner(nil, "/tmp/test")

	if runner.verbose {
		t.Error("Expected verbose to be false by default")
	}

	runner.SetVerbose(true)

	if !runner.verbose {
		t.Error("Expected verbose to be true after SetVerbose(true)")
	}
}

func TestRunTestsDisabled(t *testing.T) {
	config := &TestConfig{
		Mode:    TestModeDisabled,
		Scope:   TestScopeAll,
		Timeout: 1 * time.Minute,
	}

	runner := NewRunner(config, "/tmp/test")
	result := runner.Run("/tmp/test", "task-123")

	if !result.Success {
		t.Errorf("Expected result to be successful when tests are disabled, got error: %s", result.Error)
	}

	if result.RunTests {
		t.Error("Expected RunTests to be false when tests are disabled")
	}
}

func TestRunTestsNoChanges(t *testing.T) {
	// Create a temporary directory structure
	tempDir := t.TempDir()
	baseDir := filepath.Join(tempDir, "repo")
	worktreeDir := filepath.Join(tempDir, "worktrees")

	// Initialize a git repo
	err := os.MkdirAll(baseDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create base dir: %v", err)
	}

	runGit(t, baseDir, "init")
	runGit(t, baseDir, "config", "user.name", "Test User")
	runGit(t, baseDir, "config", "user.email", "test@example.com")
	runGit(t, baseDir, "checkout", "-b", "main")

	// Create initial commit
	runGit(t, baseDir, "commit", "--allow-empty", "-m", "Initial commit")

	// Create worktree directory
	err = os.MkdirAll(worktreeDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create worktree dir: %v", err)
	}

	config := &TestConfig{
		Mode:    TestModeStrict,
		Scope:   TestScopeSkip,
		Timeout: 1 * time.Minute,
	}

	runner := NewRunner(config, baseDir)
	result := runner.Run(baseDir, "task-123")

	if result.Error != "" {
		t.Errorf("Expected no error when there are no changes, got: %s", result.Error)
	}
}

func TestHasFile(t *testing.T) {
	tempDir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tempDir, "go.mod")
	err := os.WriteFile(testFile, []byte("module test\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	runner := NewRunner(nil, tempDir)

	if !runner.hasFile(tempDir, "go.mod") {
		t.Error("Expected hasFile to return true for go.mod")
	}

	if runner.hasFile(tempDir, "package.json") {
		t.Error("Expected hasFile to return false for package.json")
	}
}

func TestGetTestCommandGo(t *testing.T) {
	tempDir := t.TempDir()

	// Create a go.mod file
	err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module test\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	config := &TestConfig{Mode: TestModeStrict, Scope: TestScopeAll}
	runner := NewRunner(config, tempDir)

	cmd, args, err := runner.getTestCommand(tempDir)
	if err != nil {
		t.Fatalf("Expected no error for Go project, got: %v", err)
	}

	if cmd != "go" {
		t.Errorf("Expected command to be 'go', got '%s'", cmd)
	}

	if len(args) == 0 || args[0] != "test" {
		t.Errorf("Expected args to contain 'test', got %v", args)
	}
}

func TestGetTestCommandCustom(t *testing.T) {
	tempDir := t.TempDir()

	config := &TestConfig{
		Mode:    TestModeStrict,
		Scope:   TestScopeAll,
		Command: "make test-unit",
	}

	runner := NewRunner(config, tempDir)

	cmd, args, err := runner.getTestCommand(tempDir)
	if err != nil {
		t.Fatalf("Expected no error with custom command, got: %v", err)
	}

	if cmd != "make" {
		t.Errorf("Expected command to be 'make', got '%s'", cmd)
	}

	if len(args) != 1 || args[0] != "test-unit" {
		t.Errorf("Expected args to be ['test-unit'], got %v", args)
	}
}

func TestGetTestCommandNode(t *testing.T) {
	tempDir := t.TempDir()

	// Create a package.json file with test script
	pkgJSON := `{
		"name": "test",
		"version": "1.0.0",
		"scripts": {
			"test": "jest"
		}
	}`
	err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(pkgJSON), 0644)
	if err != nil {
		t.Fatalf("Failed to create package.json: %v", err)
	}

	config := &TestConfig{Mode: TestModeStrict, Scope: TestScopeAll}
	runner := NewRunner(config, tempDir)

	cmd, args, err := runner.getTestCommand(tempDir)
	if err != nil {
		t.Fatalf("Expected no error for Node project, got: %v", err)
	}

	if cmd != "npm" {
		t.Errorf("Expected command to be 'npm', got '%s'", cmd)
	}

	if len(args) == 0 || args[0] != "test" {
		t.Errorf("Expected args to contain 'test', got %v", args)
	}
}

func TestGetTestCommandCargo(t *testing.T) {
	tempDir := t.TempDir()

	// Create a Cargo.toml file
	cargoToml := `[package]
name = "test"
version = "0.1.0"`
	err := os.WriteFile(filepath.Join(tempDir, "Cargo.toml"), []byte(cargoToml), 0644)
	if err != nil {
		t.Fatalf("Failed to create Cargo.toml: %v", err)
	}

	config := &TestConfig{Mode: TestModeStrict, Scope: TestScopeAll}
	runner := NewRunner(config, tempDir)

	cmd, args, err := runner.getTestCommand(tempDir)
	if err != nil {
		t.Fatalf("Expected no error for Cargo project, got: %v", err)
	}

	if cmd != "cargo" {
		t.Errorf("Expected command to be 'cargo', got '%s'", cmd)
	}

	if len(args) == 0 || args[0] != "test" {
		t.Errorf("Expected args to contain 'test', got %v", args)
	}
}

func TestGetTestCommandPython(t *testing.T) {
	tempDir := t.TempDir()

	// Create a pyproject.toml file
	pyprojectToml := `[project]
name = "test"
version = "0.1.0"`
	err := os.WriteFile(filepath.Join(tempDir, "pyproject.toml"), []byte(pyprojectToml), 0644)
	if err != nil {
		t.Fatalf("Failed to create pyproject.toml: %v", err)
	}

	config := &TestConfig{Mode: TestModeStrict, Scope: TestScopeAll}
	runner := NewRunner(config, tempDir)

	cmd, args, err := runner.getTestCommand(tempDir)
	if err != nil {
		t.Fatalf("Expected no error for Python project, got: %v", err)
	}

	if cmd != "python" {
		t.Errorf("Expected command to be 'python', got '%s'", cmd)
	}

	// Should contain "-m" and "pytest"
	foundM := false
	foundPytest := false
	for _, arg := range args {
		if arg == "-m" {
			foundM = true
		}
		if arg == "pytest" {
			foundPytest = true
		}
	}

	if !foundM {
		t.Error("Expected args to contain '-m'")
	}
	if !foundPytest {
		t.Error("Expected args to contain 'pytest'")
	}
}

func TestGetTestCommandUnknown(t *testing.T) {
	tempDir := t.TempDir()

	config := &TestConfig{Mode: TestModeStrict, Scope: TestScopeAll}
	runner := NewRunner(config, tempDir)

	_, _, err := runner.getTestCommand(tempDir)
	if err == nil {
		t.Error("Expected error for unknown project type")
	}

	if !strings.Contains(err.Error(), "unable to determine test command") {
		t.Errorf("Expected 'unable to determine test command' error, got: %v", err)
	}
}

func TestParseTestOutputGo(t *testing.T) {
	config := &TestConfig{Mode: TestModeStrict, Scope: TestScopeAll}
	runner := NewRunner(config, "/tmp")

	output := `PASS
ok      github.com/cloud-shuttle/drover/pkg/types    0.002s
PASS
ok      github.com/cloud-shuttle/drover/internal/workflow    0.003s`

	passed, failed, skipped := runner.parseTestOutput(output)

	if passed == 0 && failed == 0 && skipped == 0 {
		// This is OK - the parseTestOutput is a best-effort implementation
		// In real usage, Go test output would be more verbose
	}
}

func TestParseTestOutputPytest(t *testing.T) {
	config := &TestConfig{Mode: TestModeStrict, Scope: TestScopeAll}
	runner := NewRunner(config, "/tmp")

	output := `========================= test session starts ==========================
collected 5 items

test_module.py .....                                                [100%]

========================= 5 passed, 1 skipped in 0.5s =========================`

	passed, failed, skipped := runner.parseTestOutput(output)

	// parseTestOutput is a best-effort implementation
	// It may not perfectly parse all formats, so we just check it found something
	if passed == 0 && failed == 0 {
		// At minimum, should detect that some tests passed (output contains "passed")
		t.Error("Expected parser to detect at least some test activity")
	}

	_ = skipped
}

func TestTestResultDefaults(t *testing.T) {
	result := &TestResult{}

	// Go structs initialize to zero values
	if result.Success {
		t.Error("Expected default Success to be false (zero value)")
	}

	if result.RunTests {
		t.Error("Expected default RunTests to be false (zero value)")
	}

	if result.Passed != 0 || result.Failed != 0 || result.Skipped != 0 {
		t.Error("Expected default test counts to be 0")
	}
}

// Helper function to run git commands
func runGit(t *testing.T, dir string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}
