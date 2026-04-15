package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run executes the CLI with the given args and captures stdout.
// Returns the exit code and captured stdout.
//
// Captures os.Stdout via pipe — do not use t.Parallel() in tests that
// call this helper.
func run(t *testing.T, brainDir string, args ...string) (int, string) {
	t.Helper()
	return runWithStdin(t, brainDir, nil, args...)
}

// runStdin is like run but pipes content to stdin.
func runStdin(t *testing.T, brainDir, stdin string, args ...string) (int, string) {
	t.Helper()
	return runWithStdin(t, brainDir, strings.NewReader(stdin), args...)
}

func runWithStdin(t *testing.T, brainDir string, stdin io.Reader, args ...string) (int, string) {
	t.Helper()

	fullArgs := append([]string{"--dir", brainDir}, args...)

	// Capture stdout via pipe.
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	cmd := rootCmd()
	registerSubcommands(cmd)
	cmd.SetArgs(fullArgs)
	if stdin != nil {
		cmd.SetIn(stdin)
	}

	var exitCode int
	if err := cmd.Execute(); err != nil {
		jsonMode, _ := cmd.PersistentFlags().GetBool("json")
		printError(err, jsonMode)
		if code, ok := exitCodeFromError(err); ok {
			exitCode = code
		} else {
			exitCode = 1
		}
	}

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stdout = old

	return exitCode, buf.String()
}

func setupBrainDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".brain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// (a) remember → recall verifies memory appears ranked correctly
func TestIntegration_RememberThenRecall(t *testing.T) {
	dir := setupBrainDir(t)

	// Remember a fact and a correction in the same domain.
	code, _ := run(t, dir, "remember", "Users table has 12M rows", "--domain", "database", "--layer", "fact")
	if code != 0 {
		t.Fatalf("remember fact: exit %d", code)
	}
	code, _ = run(t, dir, "remember", "Stop using raw SQL for migrations", "--domain", "database", "--layer", "correction")
	if code != 0 {
		t.Fatalf("remember correction: exit %d", code)
	}

	// Recall should return correction first (higher authority).
	code, out := run(t, dir, "recall", "--domain", "database")
	if code != 0 {
		t.Fatalf("recall: exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got:\n%s", out)
	}
	if !strings.Contains(lines[0], "[correction]") {
		t.Errorf("first result should be correction, got: %s", lines[0])
	}
}

// (b) remember with --layer vs. auto-classification
func TestIntegration_AutoClassification(t *testing.T) {
	dir := setupBrainDir(t)

	// Explicit layer
	code, out := run(t, dir, "--json", "remember", "Some fact", "--domain", "db", "--layer", "lesson")
	if code != 0 {
		t.Fatalf("remember explicit: exit %d", code)
	}
	var explicit RememberResult
	if err := json.Unmarshal([]byte(out), &explicit); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if explicit.Layer != "lesson" {
		t.Errorf("explicit layer = %q, want lesson", explicit.Layer)
	}

	// Auto-classified: "We decided" → decision
	code, out = run(t, dir, "--json", "remember", "We decided to use Cobra for CLI", "--domain", "tooling")
	if code != 0 {
		t.Fatalf("remember auto: exit %d", code)
	}
	var auto RememberResult
	if err := json.Unmarshal([]byte(out), &auto); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if auto.Layer != "decision" {
		t.Errorf("auto layer = %q, want decision", auto.Layer)
	}
}

// (c) recall with --domain filters correctly
func TestIntegration_RecallDomainFilter(t *testing.T) {
	dir := setupBrainDir(t)

	run(t, dir, "remember", "DB fact", "--domain", "database", "--layer", "fact")
	run(t, dir, "remember", "API fact", "--domain", "api", "--layer", "fact")

	code, out := run(t, dir, "--json", "recall", "--domain", "api")
	if code != 0 {
		t.Fatalf("recall: exit %d", code)
	}
	var result RecallResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Memories) != 1 {
		t.Fatalf("got %d memories, want 1", len(result.Memories))
	}
	if result.Memories[0].Domain != "api" {
		t.Errorf("domain = %q, want api", result.Memories[0].Domain)
	}
}

// (d) recall with --json produces valid JSON matching MCP response structure
func TestIntegration_RecallJSON(t *testing.T) {
	dir := setupBrainDir(t)

	run(t, dir, "remember", "Test memory", "--domain", "db", "--layer", "fact")

	code, out := run(t, dir, "--json", "recall", "--domain", "db")
	if code != 0 {
		t.Fatalf("recall: exit %d", code)
	}

	var result RecallResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, out)
	}
	if len(result.Memories) != 1 {
		t.Fatalf("got %d memories, want 1", len(result.Memories))
	}
	m := result.Memories[0]
	if m.Path == "" || m.Layer == "" || m.Domain == "" || m.Title == "" || m.Body == "" {
		t.Errorf("JSON memory has empty fields: %+v", m)
	}
}

// (e) forget → recall verifies memory no longer appears
func TestIntegration_ForgetThenRecall(t *testing.T) {
	dir := setupBrainDir(t)

	code, out := run(t, dir, "--json", "remember", "Ephemeral fact", "--domain", "db", "--layer", "fact")
	if code != 0 {
		t.Fatalf("remember: exit %d", code)
	}
	var rr RememberResult
	if err := json.Unmarshal([]byte(out), &rr); err != nil {
		t.Fatalf("unmarshal remember: %v", err)
	}

	code, _ = run(t, dir, "forget", rr.Path, "--reason", "no longer needed")
	if code != 0 {
		t.Fatalf("forget: exit %d", code)
	}

	code, _ = run(t, dir, "recall", "--domain", "db")
	if code != 2 {
		t.Errorf("recall after forget: exit %d, want 2 (not found)", code)
	}
}

// (f) list --include-retired shows forgotten memories
func TestIntegration_ListIncludeRetired(t *testing.T) {
	dir := setupBrainDir(t)

	code, out := run(t, dir, "--json", "remember", "Will be forgotten", "--domain", "db", "--layer", "fact")
	if code != 0 {
		t.Fatalf("remember: exit %d", code)
	}
	var rr RememberResult
	if err := json.Unmarshal([]byte(out), &rr); err != nil {
		t.Fatalf("unmarshal remember: %v", err)
	}

	run(t, dir, "forget", rr.Path)

	// Without --include-retired: empty
	code, out = run(t, dir, "--json", "list")
	if code != 0 {
		t.Fatalf("list: exit %d", code)
	}
	var listResult ListResult
	if err := json.Unmarshal([]byte(out), &listResult); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if listResult.Count != 0 {
		t.Errorf("list count = %d, want 0", listResult.Count)
	}

	// With --include-retired: shows the memory
	code, out = run(t, dir, "--json", "list", "--include-retired")
	if code != 0 {
		t.Fatalf("list --include-retired: exit %d", code)
	}
	if err := json.Unmarshal([]byte(out), &listResult); err != nil {
		t.Fatalf("unmarshal list --include-retired: %v", err)
	}
	if listResult.Count != 1 {
		t.Errorf("list --include-retired count = %d, want 1", listResult.Count)
	}
	if !listResult.Memories[0].Retired {
		t.Error("memory should be marked retired")
	}
}

// (g) remember via stdin pipe
func TestIntegration_RememberStdin(t *testing.T) {
	dir := setupBrainDir(t)

	code, out := runStdin(t, dir, "Piped content from stdin", "--json", "remember", "--domain", "db", "--layer", "fact")
	if code != 0 {
		t.Fatalf("remember stdin: exit %d", code)
	}
	var rr RememberResult
	if err := json.Unmarshal([]byte(out), &rr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rr.Path == "" {
		t.Error("expected non-empty path")
	}

	// Verify the content was stored
	code, out = run(t, dir, "--json", "recall", "--domain", "db")
	if code != 0 {
		t.Fatalf("recall: exit %d", code)
	}
	var result RecallResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("unmarshal recall: %v", err)
	}
	if len(result.Memories) != 1 {
		t.Fatalf("got %d memories, want 1", len(result.Memories))
	}
	if !strings.Contains(result.Memories[0].Body, "Piped content") {
		t.Errorf("body = %q, want to contain 'Piped content'", result.Memories[0].Body)
	}
}

// (h) error cases
func TestIntegration_ErrorCases(t *testing.T) {
	dir := setupBrainDir(t)

	t.Run("recall empty store exits 2", func(t *testing.T) {
		code, _ := run(t, dir, "recall")
		if code != 2 {
			t.Errorf("exit %d, want 2", code)
		}
	})

	t.Run("forget nonexistent path exits 2", func(t *testing.T) {
		code, _ := run(t, dir, "forget", "facts/nonexistent.md")
		if code != 2 {
			t.Errorf("exit %d, want 2", code)
		}
	})

	t.Run("remember with invalid layer exits 3", func(t *testing.T) {
		code, _ := run(t, dir, "remember", "test", "--domain", "db", "--layer", "bogus")
		if code != 3 {
			t.Errorf("exit %d, want 3", code)
		}
	})

	t.Run("remember without domain exits 3", func(t *testing.T) {
		code, _ := run(t, dir, "remember", "test")
		if code != 3 {
			t.Errorf("exit %d, want 3", code)
		}
	})

	t.Run("remember without content exits 3", func(t *testing.T) {
		code, _ := run(t, dir, "remember", "--domain", "db")
		if code != 3 {
			t.Errorf("exit %d, want 3", code)
		}
	})
}
