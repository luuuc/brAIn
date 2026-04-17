package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/brain/internal/version"
)

// Meta commands (version/help/completion) must succeed without a .brain/
// directory, so a fresh install can run `brain version` or `brain help`
// before any memory state exists.

func TestVersion_NoBrainDir(t *testing.T) {
	code, out := runNoDir(t, "version")
	if code != 0 {
		t.Fatalf("version: exit %d, out: %s", code, out)
	}
	if !strings.Contains(out, version.Version) {
		t.Errorf("output %q missing version string %q", out, version.Version)
	}
}

func TestVersion_JSON_NoBrainDir(t *testing.T) {
	code, out := runNoDir(t, "--json", "version")
	if code != 0 {
		t.Fatalf("version --json: exit %d, out: %s", code, out)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, out)
	}
	if got["version"] != version.Version {
		t.Errorf("version = %q, want %q", got["version"], version.Version)
	}
}

func TestHelp_NoBrainDir(t *testing.T) {
	code, out := runNoDir(t, "help")
	if code != 0 {
		t.Fatalf("help: exit %d, out: %s", code, out)
	}
	for _, sub := range []string{"remember", "recall", "trust", "version"} {
		if !strings.Contains(out, sub) {
			t.Errorf("help output missing subcommand %q, got:\n%s", sub, out)
		}
	}
}

// TestMetaCommands_ParentChainBypass exercises nested meta commands to
// confirm isMetaCommand walks the parent chain. Without the walk, these
// would fail because their direct Name() ("remember", "bash", etc.) is
// not in the meta list — only their ancestor is.
func TestMetaCommands_ParentChainBypass(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"help_subcommand", []string{"help", "remember"}},
		{"help_nested", []string{"help", "trust", "record"}},
		{"completion_bash", []string{"completion", "bash"}},
		{"completion_zsh", []string{"completion", "zsh"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runNoDir(t, tc.args...)
			if code != 0 {
				t.Fatalf("%v: exit %d, out: %s", tc.args, code, out)
			}
			if out == "" {
				t.Errorf("%v: expected non-empty output", tc.args)
			}
		})
	}
}

// runNoDir executes brain from a cwd that contains no .brain/ and no .git/
// parent, so resolveBrainDir would fail if it were called. Used to prove
// that meta commands skip engine setup.
func runNoDir(t *testing.T, args ...string) (int, string) {
	t.Helper()

	wd := t.TempDir()
	for cur := wd; cur != filepath.Dir(cur); cur = filepath.Dir(cur) {
		if _, err := os.Stat(filepath.Join(cur, ".brain")); err == nil {
			t.Fatalf("unexpected .brain/ at %s — test assumes a clean tree", cur)
		}
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	return executeAndCapture(t, nil, args...)
}
