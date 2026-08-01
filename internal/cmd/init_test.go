package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/brain/internal/config"
	"github.com/luuuc/brain/internal/markdown"
)

// runInit executes init with an explicit target directory.
func runInit(t *testing.T, target string, extra ...string) (int, string) {
	t.Helper()
	args := append([]string{"--dir", target, "init"}, extra...)
	return executeAndCapture(t, nil, args...)
}

func TestInit_createsFullTree(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".brain")

	code, out := runInit(t, target)
	if code != 0 {
		t.Fatalf("init: exit %d, out=%s", code, out)
	}

	for _, name := range append(markdown.LayerDirs(), "trust") {
		info, err := os.Stat(filepath.Join(target, name))
		if err != nil || !info.IsDir() {
			t.Errorf("%s/ missing after init (err=%v)", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, config.FileName)); err != nil {
		t.Errorf("%s missing after init: %v", config.FileName, err)
	}
	if !strings.Contains(out, "created") {
		t.Errorf("output %q does not report what was created", out)
	}
}

// init must work with no .brain/ anywhere — that is the whole point, and it
// is why init is in runsWithoutBrainDir.
func TestInit_worksWithNoExistingBrainDir(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	code, out := executeAndCapture(t, nil, "init")
	if code != 0 {
		t.Fatalf("init in empty dir: exit %d, out=%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".brain", config.FileName)); err != nil {
		t.Errorf("init did not create .brain/ in cwd: %v", err)
	}
}

// Running init twice must change nothing and say so.
func TestInit_isIdempotent(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".brain")
	if code, out := runInit(t, target); code != 0 {
		t.Fatalf("first init: exit %d, out=%s", code, out)
	}

	// Edit the config, then re-init. The edit must survive.
	cfgPath := filepath.Join(target, config.FileName)
	edited := "trust:\n  promote_to_notify: 3\n"
	if err := os.WriteFile(cfgPath, []byte(edited), 0o644); err != nil {
		t.Fatalf("editing config: %v", err)
	}

	code, out := runInit(t, target)
	if code != 0 {
		t.Fatalf("second init: exit %d, out=%s", code, out)
	}
	if strings.Contains(out, "created") {
		t.Errorf("second init reported a creation: %s", out)
	}
	if !strings.Contains(out, "Already initialized") {
		t.Errorf("second init did not report the directory as existing: %s", out)
	}

	got, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if string(got) != edited {
		t.Error("init overwrote an edited brain.yml")
	}
}

func TestInit_partialTreeFillsGaps(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".brain")
	if err := os.MkdirAll(filepath.Join(target, "facts"), 0o755); err != nil {
		t.Fatalf("pre-creating facts/: %v", err)
	}

	code, out := runInit(t, target, "--json")
	if code != 0 {
		t.Fatalf("init: exit %d, out=%s", code, out)
	}
	var res initResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal: %v (out=%s)", err, out)
	}

	if res.Skipped != 1 || res.Created != len(res.Entries)-1 {
		t.Errorf("created=%d skipped=%d, want exactly one skip", res.Created, res.Skipped)
	}
	for _, e := range res.Entries {
		if strings.HasPrefix(e.Name, "facts") && e.Created {
			t.Error("pre-existing facts/ reported as created")
		}
	}
}

// The generated config must load without error and must not change any
// setting — every key is commented out, so everything stays a default.
func TestInit_generatedConfigIsAllDefaults(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".brain")
	if code, out := runInit(t, target); code != 0 {
		t.Fatalf("init: exit %d, out=%s", code, out)
	}

	cfg, err := config.Load(target)
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	want := config.Default()
	if cfg.Trust != want.Trust || cfg.Facts != want.Facts ||
		cfg.Lessons != want.Lessons || cfg.Storage != want.Storage {
		t.Errorf("generated config changed a value: got %+v, want %+v", cfg, want)
	}
	for _, k := range config.Keys() {
		if src := cfg.Source(k); src != config.SourceDefault {
			t.Errorf("Source(%q) = %q, want %q — a key was left uncommented",
				k, src, config.SourceDefault)
		}
	}
}

// The template documents both threshold asymmetries. They are the two
// behaviours users are most likely to guess wrong about.
func TestTemplate_documentsThresholdAsymmetries(t *testing.T) {
	tmpl := config.Template()
	for _, phrase := range []string{
		"does not demote",
		"brain trust override",
		"Retirement is permanent",
	} {
		if !strings.Contains(tmpl, phrase) {
			t.Errorf("template does not mention %q", phrase)
		}
	}
}

// A file where a directory belongs is a real misconfiguration, not something
// to silently work around.
func TestInit_failsWhenPathIsAFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".brain")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "facts"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("writing blocker: %v", err)
	}

	code, _ := runInit(t, target)
	if code == 0 {
		t.Error("init succeeded with a file where facts/ belongs")
	}
}

// gitRepo creates a temp git repository with the given .gitignore contents
// and returns its path. Skips if git is unavailable.
func gitRepo(t *testing.T, gitignore string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v (%s)", err, out)
	}
	if gitignore != "" {
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o644); err != nil {
			t.Fatalf("writing .gitignore: %v", err)
		}
	}
	return dir
}

// initInRepo runs init with .brain/ inside repo, returning the JSON result.
func initInRepo(t *testing.T, repo string) initResult {
	t.Helper()
	code, out := runInit(t, filepath.Join(repo, ".brain"), "--json")
	if code != 0 {
		t.Fatalf("init: exit %d, out=%s", code, out)
	}
	var res initResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal: %v (out=%s)", err, out)
	}
	return res
}

// An unanchored brain.yml rule — the exact trap this repo's own .gitignore
// had — must be reported, not silently swallowed.
func TestInit_warnsWhenConfigIsGitIgnored(t *testing.T) {
	repo := gitRepo(t, "brain.yml\n")
	if got := initInRepo(t, repo).Ignored; got != "config" {
		t.Errorf("Ignored = %q, want %q", got, "config")
	}

	code, out := runInit(t, filepath.Join(repo, ".brain"))
	if code != 0 {
		t.Fatalf("init: exit %d", code)
	}
	for _, want := range []string{"Warning", "git is ignoring", "/brain.yml"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

// A whole ignored .brain/ is the worse case and gets its own message.
func TestInit_warnsWhenWholeDirIsGitIgnored(t *testing.T) {
	repo := gitRepo(t, ".brain/\n")
	if got := initInRepo(t, repo).Ignored; got != "dir" {
		t.Errorf("Ignored = %q, want %q", got, "dir")
	}
}

// No warning when git is tracking everything, and none outside a repository.
func TestInit_noWarningWhenTracked(t *testing.T) {
	if got := initInRepo(t, gitRepo(t, "")).Ignored; got != "" {
		t.Errorf("Ignored = %q in a clean repo, want empty", got)
	}

	target := filepath.Join(t.TempDir(), ".brain")
	code, out := runInit(t, target, "--json")
	if code != 0 {
		t.Fatalf("init outside a repo: exit %d, out=%s", code, out)
	}
	var res initResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Ignored != "" {
		t.Errorf("Ignored = %q outside a git repo, want empty", res.Ignored)
	}
}

// This repository's own .gitignore must not swallow a project's brain.yml.
// The rule that used to do exactly that is the reason this test exists.
func TestRepoGitignore_doesNotIgnoreNestedBrainYML(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not in a git repository")
	}
	repo := strings.TrimSpace(string(root))

	cmd := exec.Command("git", "check-ignore", "-q", "--", ".brain/brain.yml")
	cmd.Dir = repo
	if err := cmd.Run(); err == nil {
		t.Error(".brain/brain.yml is git-ignored by this repo's .gitignore; " +
			"anchor the brain.yml rule to /brain.yml")
	}
}
