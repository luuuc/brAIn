package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/luuuc/brain/internal/config"
	"github.com/luuuc/brain/internal/trust"
)

// chdir moves to dir for the duration of the test.
func chdir(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
}

// A session-start hook that exits non-zero blocks the session. Every state
// below is ordinary rather than exceptional — a project that has never heard
// of brAIn, one where init ran a minute ago, a stale --dir in someone's
// settings.json — and none of them is a reason to refuse to start work.
func TestSessionStart_neverFailsASession(t *testing.T) {
	t.Run("no .brain/ anywhere", func(t *testing.T) {
		chdir(t, t.TempDir())
		code, out := executeAndCapture(t, nil, "hooks", "session-start")
		assertSilentSuccess(t, code, out)
	})

	t.Run("initialized but empty", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), ".brain")
		if code, out := runInit(t, target); code != 0 {
			t.Fatalf("init: exit %d, out=%s", code, out)
		}
		code, out := run(t, target, "hooks", "session-start")
		assertSilentSuccess(t, code, out)
	})

	t.Run("--dir points at nothing", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "gone")
		code, out := executeAndCapture(t, nil, "--dir", missing, "hooks", "session-start")
		assertSilentSuccess(t, code, out)
	})
}

func assertSilentSuccess(t *testing.T, code int, out string) {
	t.Helper()
	if code != 0 {
		t.Errorf("exit %d, want 0 — a non-zero exit blocks the session", code)
	}
	if out != "" {
		t.Errorf("printed context with nothing to say:\n%s", out)
	}
}

// With memories to show, the hook injects them highest-authority first and
// says so. A model reading a bare list has no way to know that a correction
// outranks a fact.
func TestSessionStart_injectsRankedMemories(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".brain")
	if code, out := runInit(t, target); code != 0 {
		t.Fatalf("init: exit %d, out=%s", code, out)
	}
	for _, m := range []struct{ content, domain string }{
		{"The users table has 12M rows", "database"},
		{"Stop flagging nullable email columns", "testing"},
		{"We chose camelCase for API responses", "api"},
	} {
		if code, out := run(t, target, "remember", m.content, "--domain", m.domain); code != 0 {
			t.Fatalf("remember %q: exit %d, out=%s", m.content, code, out)
		}
	}

	code, out := run(t, target, "hooks", "session-start")
	if code != 0 {
		t.Fatalf("exit %d, out=%s", code, out)
	}

	for _, want := range []string{
		"Project memory (brAIn)",
		"non-negotiable",
		"Stop flagging nullable email columns",
		"We chose camelCase for API responses",
		"The users table has 12M rows",
		"brain recall --domain",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	// Authority order, not insertion order: the correction was remembered
	// second and the fact first, but the correction has to lead.
	correction := strings.Index(out, "[correction]")
	decision := strings.Index(out, "[decision]")
	fact := strings.Index(out, "[fact]")
	if correction < 0 || decision < 0 || fact < 0 {
		t.Fatalf("expected all three layers in output:\n%s", out)
	}
	if correction >= decision || decision >= fact {
		t.Errorf("layers out of authority order (correction=%d decision=%d fact=%d):\n%s",
			correction, decision, fact, out)
	}
}

// The injection budget is a standing cost: every session pays for it before
// anyone types. Five is the shaped number.
func TestSessionStart_capsWhatItInjects(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".brain")
	if code, out := runInit(t, target); code != 0 {
		t.Fatalf("init: exit %d, out=%s", code, out)
	}
	for i := range sessionStartLimit + 3 {
		content := "Fact number " + string(rune('A'+i)) + " about the system"
		if code, out := run(t, target, "remember", content, "--domain", "database"); code != 0 {
			t.Fatalf("remember: exit %d, out=%s", code, out)
		}
	}

	code, out := run(t, target, "hooks", "session-start")
	if code != 0 {
		t.Fatalf("exit %d, out=%s", code, out)
	}
	if got := strings.Count(out, "domain: database"); got != sessionStartLimit {
		t.Errorf("injected %d memories, want %d", got, sessionStartLimit)
	}
}

// Effectiveness memories reach a session like any other, and on a young
// brain they reach it first: with fewer than five higher-ranked memories
// they fill the remaining slots. Rendering one as its body's first line puts
// a bare "## Outcomes" heading in front of the model.
func TestSessionStart_rendersEffectivenessReadably(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".brain")
	if code, out := runInit(t, target); code != 0 {
		t.Fatalf("init: exit %d, out=%s", code, out)
	}
	if code, out := run(t, target, "track", "--domain", "payments",
		"--persona", "kent-beck", "--outcome", "accepted"); code != 0 {
		t.Fatalf("track: exit %d, out=%s", code, out)
	}

	code, out := run(t, target, "hooks", "session-start")
	if code != 0 {
		t.Fatalf("exit %d, out=%s", code, out)
	}
	if !strings.Contains(out, "kent-beck effectiveness in payments") {
		t.Errorf("effectiveness memory not identified by persona and domain:\n%s", out)
	}
	if strings.Contains(out, "## Outcomes") {
		t.Errorf("injected a raw markdown heading as a memory title:\n%s", out)
	}
}

// settingsSnippet is the shape of the block brAIn tells people to paste.
type settingsSnippet struct {
	Hooks struct {
		SessionStart []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"SessionStart"`
	} `json:"hooks"`
}

// parseSnippet parses the snippet as an editor would. It gets pasted into a
// real settings.json, so malformed JSON here breaks the user's editor
// configuration and brAIn caused it.
func parseSnippet(t *testing.T) settingsSnippet {
	t.Helper()
	var cfg settingsSnippet
	if err := json.Unmarshal([]byte(claudeSettingsSnippet), &cfg); err != nil {
		t.Fatalf("snippet is not valid JSON: %v", err)
	}
	if len(cfg.Hooks.SessionStart) != 1 || len(cfg.Hooks.SessionStart[0].Hooks) != 1 {
		t.Fatalf("want exactly one SessionStart hook, got %+v", cfg.Hooks.SessionStart)
	}
	return cfg
}

func TestHooks_settingsSnippetIsWired(t *testing.T) {
	h := parseSnippet(t).Hooks.SessionStart[0].Hooks[0]
	if h.Type != "command" {
		t.Errorf("hook type = %q, want %q", h.Type, "command")
	}
	// Nothing inside the hook can bound a wedged recall, so the only bound
	// is this one. A snippet without it stalls session start.
	if h.Timeout <= 0 {
		t.Errorf("timeout = %d, want a positive bound", h.Timeout)
	}
}

// Memory has to survive the events that empty a session's context, not just
// the one that starts it. Compaction is the case that matters most: it is
// where a correction gets summarised away and the model quietly loses the
// thing it was told not to do.
func TestHooks_settingsSnippetSurvivesContextLoss(t *testing.T) {
	matcher := parseSnippet(t).Hooks.SessionStart[0].Matcher
	for _, want := range []string{"startup", "clear", "compact"} {
		if !strings.Contains(matcher, want) {
			t.Errorf("matcher %q does not cover %q", matcher, want)
		}
	}
	// Both inherit a context that already carries the injection, so firing
	// again would duplicate it.
	for _, unwanted := range []string{"resume", "fork"} {
		if strings.Contains(matcher, unwanted) {
			t.Errorf("matcher %q re-injects into %q, which already has it", matcher, unwanted)
		}
	}
}

// The snippet names a command. If that command is renamed, the hook fails
// silently inside someone's editor — so the name has to be one brAIn answers
// to, checked by running it.
func TestHooks_settingsSnippetNamesARealCommand(t *testing.T) {
	fields := strings.Fields(parseSnippet(t).Hooks.SessionStart[0].Hooks[0].Command)
	if len(fields) < 2 || fields[0] != "brain" {
		t.Fatalf("command %q is not a brain invocation", fields)
	}
	// Point it at an empty store rather than letting resolveBrainDir walk up
	// into this repository's own .brain/. What is asserted here is that the
	// command exists and succeeds, not what a recall happens to return today.
	empty := filepath.Join(t.TempDir(), ".brain")
	if code, out := runInit(t, empty); code != 0 {
		t.Fatalf("init: exit %d, out=%s", code, out)
	}
	args := append([]string{"--dir", empty}, fields[1:]...)
	if code, out := executeAndCapture(t, nil, args...); code != 0 {
		t.Errorf("%v: exit %d, out=%s — the snippet points at a command that fails",
			fields, code, out)
	}
}

// brain hooks is a meta command: someone runs it to find out how to set
// brAIn up, which is by definition before they have a .brain/.
func TestHooks_printsSetupWithoutABrainDir(t *testing.T) {
	chdir(t, t.TempDir())

	code, out := executeAndCapture(t, nil, "hooks")
	if code != 0 {
		t.Fatalf("exit %d, out=%s", code, out)
	}
	for _, want := range []string{
		".claude/settings.json",
		claudeSettingsSnippet,
		"SessionStart",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("setup output missing %q:\n%s", want, out)
		}
	}
}

// findSub returns parent's immediate subcommand named name, or nil.
func findSub(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// Rules that tell a model to run a command brAIn does not have are worse
// than no rules: the model runs it, gets an error, and learns to ignore the
// section. The definition docs already carry one of these — 06-mcp-and-cli
// documents "brain recall --for council", a flag that has never existed — so
// this scans every command the printed setup names and resolves it against
// the real command tree.
// assertNamesRealCommands resolves every "brain ..." invocation in text
// against the real command tree.
func assertNamesRealCommands(t *testing.T, what, text string) {
	t.Helper()
	root := rootCmd()
	registerSubcommands(root)

	resolved := make(map[string]bool)
	for _, m := range regexp.MustCompile(`brain((?: [a-z][a-z-]*)+)`).FindAllStringSubmatch(text, -1) {
		words := strings.Fields(m[1])
		cur, depth := root, 0
		for _, w := range words {
			next := findSub(cur, w)
			if next == nil {
				break
			}
			cur, depth = next, depth+1
		}
		if depth == 0 {
			t.Errorf("%s names `brain %s`, which is not a brain command", what, words[0])
			continue
		}
		resolved[strings.Join(words[:depth], " ")] = true
	}

	// Guard against a scan that silently matches nothing and passes.
	if len(resolved) < 3 {
		t.Errorf("%s: only resolved %d commands (%v); the scan is probably broken",
			what, len(resolved), resolved)
	}
}

func TestHooks_namesOnlyRealCommands(t *testing.T) {
	for _, tool := range []string{toolClaudeCode, toolCursor} {
		code, out := executeAndCapture(t, nil, "hooks", "--tool", tool)
		if code != 0 {
			t.Fatalf("hooks --tool %s: exit %d, out=%s", tool, code, out)
		}
		assertNamesRealCommands(t, "the "+tool+" setup", out)
	}
}

// The README teaches the commands people type first, so a stale one there
// costs more than a stale one anywhere else — it is the first thing that
// fails, for someone with no way to tell whether they or the tool is wrong.
func TestREADME_namesOnlyRealCommands(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Skipf("README not readable from here: %v", err)
	}
	assertNamesRealCommands(t, "README.md", string(readme))
}

// The numbers in the rules are the ones the engines actually apply. Typing
// them out by hand is how a template starts lying after a config change.
func TestHooks_rulesQuoteRealThresholds(t *testing.T) {
	rules := memoryRules(claudeIntro)

	if want := fmt.Sprintf("%d days", config.Default().Facts.StaleAfterDays); !strings.Contains(rules, want) {
		t.Errorf("rules do not state the real staleness window (%q):\n%s", want, rules)
	}
	for _, level := range []trust.Level{
		trust.LevelAsk, trust.LevelNotify, trust.LevelAutoShip, trust.LevelFullAuto,
	} {
		if !strings.Contains(rules, string(level)) {
			t.Errorf("rules never mention the %q trust level", level)
		}
	}
}

// Two things the rules exist to say. Both are no-gos from the pitch, and
// both are the kind of paragraph a later edit trims as negative-sounding.
//
// The restraint half is the whole value: a model told to "remember what you
// learned" will always find something, and the folder fills with things
// nobody reads. The trust half stops a domain climbing the ladder on
// finished tasks rather than on work that shipped.
func TestHooks_rulesSayWhatNotToDo(t *testing.T) {
	rules := memoryRules(claudeIntro)
	for _, want := range []string{
		"Not worth remembering",
		"leave it out",
		"Trust outcomes are not yours to record",
		// Without an explicit layer, brAIn guesses from wording. The
		// classifier is a substring matcher; the model is not.
		"--layer",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("rules dropped %q — see the pitch's no-gos", want)
		}
	}
}

// Cursor reads the frontmatter, so a malformed header means the rule is
// silently ignored and the user believes brAIn is set up when it is not.
func TestHooks_cursorFrontmatterIsValid(t *testing.T) {
	body := strings.TrimSpace(cursorFrontmatter)
	body = strings.TrimPrefix(body, "---")
	body = strings.TrimSuffix(body, "---")

	var fm struct {
		Description string `yaml:"description"`
		Globs       string `yaml:"globs"`
		AlwaysApply bool   `yaml:"alwaysApply"`
	}
	if err := yaml.Unmarshal([]byte(body), &fm); err != nil {
		t.Fatalf("frontmatter is not valid YAML: %v", err)
	}
	if fm.Description == "" {
		t.Error("frontmatter has no description")
	}
	// Memory is not file-scoped context. Globbing it would hide a correction
	// exactly when someone works somewhere nobody predicted.
	if !fm.AlwaysApply {
		t.Error("alwaysApply is false; the rule would only load for matching files")
	}
	if fm.Globs != "" {
		t.Errorf("globs = %q, want empty alongside alwaysApply", fm.Globs)
	}
}

// Each editor's rules have to describe how that editor actually gets its
// memory. Telling Cursor to rely on an injection that never happens is worse
// than telling it to go and fetch it.
func TestHooks_cursorSetupDoesNotPromiseAnInjection(t *testing.T) {
	code, out := executeAndCapture(t, nil, "hooks", "--tool", toolCursor)
	if code != 0 {
		t.Fatalf("exit %d, out=%s", code, out)
	}

	for _, want := range []string{".cursor/rules/brain.mdc", "alwaysApply: true", "Not worth remembering"} {
		if !strings.Contains(out, want) {
			t.Errorf("cursor setup missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "session-start hook loads") {
		t.Error("cursor setup claims a hook loads memory; nothing does in that setup")
	}
	if !strings.Contains(out, "Nothing injects it for you") {
		t.Error("cursor setup does not tell the model to recall for itself")
	}
}

func TestHooks_unknownToolIsRejected(t *testing.T) {
	code, _ := executeAndCapture(t, nil, "hooks", "--tool", "vim")
	if code != 3 {
		t.Errorf("exit %d, want 3 (invalid input)", code)
	}
}
