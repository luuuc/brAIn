package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
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
	return executeAndCapture(t, stdin, fullArgs...)
}

// executeAndCapture builds a fresh rootCmd, runs it with args, and captures
// stdout via pipe. Callers arrange cwd, stdin, and any --dir flag themselves.
//
// Captures os.Stdout globally — do not use t.Parallel() in tests that call
// this helper.
func executeAndCapture(t *testing.T, stdin io.Reader, args ...string) (int, string) {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	cmd := rootCmd()
	registerSubcommands(cmd)
	cmd.SetArgs(args)
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

	if err := w.Close(); err != nil {
		t.Fatalf("closing pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("reading pipe: %v", err)
	}
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

// writeBrainYML drops a brain.yml into an existing .brain/ directory.
func writeBrainYML(t *testing.T, brainDir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(brainDir, "brain.yml"), []byte(body), 0o644); err != nil {
		t.Fatalf("writing brain.yml: %v", err)
	}
}

// End-to-end proof that facts.stale_after_days from brain.yml reaches the
// write path. Without the wiring in root.go the file would carry the 30-day
// default, or before card 4, no stale_after at all.
func TestIntegration_FactStalenessComesFromConfig(t *testing.T) {
	dir := setupBrainDir(t)
	writeBrainYML(t, dir, "facts:\n  stale_after_days: 7\n")

	code, out := run(t, dir, "remember", "Users table has 12M rows",
		"--domain", "database", "--layer", "fact")
	if code != 0 {
		t.Fatalf("remember: exit %d, out=%s", code, out)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "facts", "*.md"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one fact file, got %v (err=%v)", matches, err)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("reading fact: %v", err)
	}

	var fm struct {
		Created    time.Time `yaml:"created"`
		StaleAfter time.Time `yaml:"stale_after"`
	}
	parts := strings.SplitN(string(body), "---", 3)
	if len(parts) < 3 {
		t.Fatalf("no frontmatter in %s", body)
	}
	if err := yaml.Unmarshal([]byte(parts[1]), &fm); err != nil {
		t.Fatalf("parsing frontmatter: %v", err)
	}

	if fm.StaleAfter.IsZero() {
		t.Fatal("stale_after missing — the fact will never go stale")
	}
	if want := fm.Created.AddDate(0, 0, 7); !fm.StaleAfter.Equal(want) {
		t.Errorf("stale_after = %v, want created+7d = %v", fm.StaleAfter, want)
	}
}

// A decision is only reopenable if the reader can see what would reopen it.
// The rules brAIn ships tell a model to "reopen one only when its revisit
// condition has actually been met", so the condition has to reach every path
// a consumer reads: the frontmatter, the JSON, and the text.
func TestRemember_revisitIfReachesEveryReader(t *testing.T) {
	dir := setupBrainDir(t)

	if code, out := run(t, dir, "remember", "camelCase for API responses",
		"--domain", "api", "--layer", "decision", "--revisit-if", "GraphQL adoption"); code != 0 {
		t.Fatalf("remember: exit %d, out=%s", code, out)
	}

	t.Run("frontmatter", func(t *testing.T) {
		matches, err := filepath.Glob(filepath.Join(dir, "decisions", "*.md"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("want one decision file, got %v (err=%v)", matches, err)
		}
		raw, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatalf("reading decision: %v", err)
		}
		if !strings.Contains(string(raw), "revisit_if: GraphQL adoption") {
			t.Errorf("revisit_if missing from frontmatter:\n%s", raw)
		}
	})

	t.Run("recall --json", func(t *testing.T) {
		code, out := run(t, dir, "--json", "recall", "--domain", "api")
		if code != 0 {
			t.Fatalf("recall: exit %d, out=%s", code, out)
		}
		var res RecallResult
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("unmarshal: %v (out=%s)", err, out)
		}
		if len(res.Memories) != 1 || res.Memories[0].RevisitIf != "GraphQL adoption" {
			t.Errorf("revisit_if missing from JSON: %+v", res.Memories)
		}
	})

	t.Run("recall text", func(t *testing.T) {
		code, out := run(t, dir, "recall", "--domain", "api")
		if code != 0 {
			t.Fatalf("recall: exit %d, out=%s", code, out)
		}
		if !strings.Contains(out, "revisit if: GraphQL adoption") {
			t.Errorf("revisit_if missing from text output:\n%s", out)
		}
	})

	t.Run("session-start injection", func(t *testing.T) {
		code, out := run(t, dir, "hooks", "session-start")
		if code != 0 {
			t.Fatalf("session-start: exit %d, out=%s", code, out)
		}
		if !strings.Contains(out, "revisit if: GraphQL adoption") {
			t.Errorf("revisit_if missing from the injected context:\n%s", out)
		}
	})
}

// A memory with no revisit condition must not grow an empty line for it.
func TestRecall_noRevisitLineWhenUnset(t *testing.T) {
	dir := setupBrainDir(t)

	if code, out := run(t, dir, "remember", "The users table has 12M rows",
		"--domain", "database", "--layer", "fact"); code != 0 {
		t.Fatalf("remember: exit %d, out=%s", code, out)
	}
	code, out := run(t, dir, "recall", "--domain", "database")
	if code != 0 {
		t.Fatalf("recall: exit %d, out=%s", code, out)
	}
	if strings.Contains(out, "revisit if:") {
		t.Errorf("printed an empty revisit line:\n%s", out)
	}
}

// Superseding is how one decision replaces another. The replaced memory is
// retired, so recall stops surfacing it, and the replacement stands alone.
func TestRemember_supersedesRetiresTheOldDecision(t *testing.T) {
	dir := setupBrainDir(t)

	code, out := run(t, dir, "--json", "remember", "snake_case for API responses",
		"--domain", "api", "--layer", "decision")
	if code != 0 {
		t.Fatalf("remember old: exit %d, out=%s", code, out)
	}
	var first RememberResult
	if err := json.Unmarshal([]byte(out), &first); err != nil {
		t.Fatalf("unmarshal: %v (out=%s)", err, out)
	}

	if code, out := run(t, dir, "remember", "camelCase for API responses",
		"--domain", "api", "--layer", "decision", "--supersedes", first.Path); code != 0 {
		t.Fatalf("remember new: exit %d, out=%s", code, out)
	}

	code, out = run(t, dir, "recall", "--domain", "api")
	if code != 0 {
		t.Fatalf("recall: exit %d, out=%s", code, out)
	}
	if !strings.Contains(out, "camelCase") {
		t.Errorf("replacement missing from recall:\n%s", out)
	}
	if strings.Contains(out, "snake_case") {
		t.Errorf("superseded decision still surfaces in recall:\n%s", out)
	}
}

// A path that does not resolve is almost always a typo, and a typo must not
// buy two live decisions that contradict each other.
func TestRemember_supersedesRejectsAPathThatIsNotThere(t *testing.T) {
	dir := setupBrainDir(t)

	code, _ := run(t, dir, "remember", "camelCase for API responses",
		"--domain", "api", "--layer", "decision", "--supersedes", "decisions/typo.md")
	if code != 3 {
		t.Errorf("exit %d, want 3 (invalid input)", code)
	}

	// Nothing may have been written on the way to that rejection.
	listCode, listOut := run(t, dir, "list")
	if listCode != 0 {
		t.Fatalf("list: exit %d", listCode)
	}
	if !strings.Contains(listOut, "0 memories") {
		t.Errorf("a rejected remember left something behind:\n%s", listOut)
	}
}

// confidence was removed from the model. Files already carrying it — every
// correction brain trust override ever wrote — must keep loading, because
// frontmatter parsing ignores keys it does not know. That leniency is what
// made the removal free, and it is worth a test rather than an assumption.
func TestRemovedFields_doNotBreakExistingFiles(t *testing.T) {
	dir := setupBrainDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "corrections"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	legacy := "---\n" +
		"layer: correction\n" +
		"domain: testing\n" +
		"created: 2026-04-01T00:00:00Z\n" +
		"source: human\n" +
		"confidence: high\n" +
		"immutable: true\n" +
		"---\n" +
		"Stop flagging nullable email columns\n"
	if err := os.WriteFile(filepath.Join(dir, "corrections", "legacy.md"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("writing legacy correction: %v", err)
	}

	code, out := run(t, dir, "recall", "--domain", "testing")
	if code != 0 {
		t.Fatalf("recall: exit %d, out=%s", code, out)
	}
	if !strings.Contains(out, "Stop flagging nullable email columns") {
		t.Errorf("a file carrying the removed confidence field no longer loads:\n%s", out)
	}
}
