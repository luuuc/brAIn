package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/config"
	"github.com/luuuc/brain/internal/markdown"
)

const initLongHelp = `Create a .brain/ memory directory.

Scaffolds the five layer directories, the trust directory, and a fully
commented brain.yml showing every setting at its default. Every setting in
the generated file is commented out, so brAIn keeps using its built-in
defaults until you deliberately uncomment one.

It also writes a .gitignore that keeps machine-local lock files out of the
shared history, and a .gitkeep in each layer directory so an empty one
survives a commit.

init is idempotent: anything that already exists is left untouched and
reported as "exists". It never overwrites a brain.yml you have edited.

Without --dir, the directory is created at .brain/ in the current
directory. Unlike every other command, init does not search parent
directories — creating a nested .brain/ by accident is worse than an
explicit path.`

// gitignoreFileName is the ignore file written inside the memory directory.
const gitignoreFileName = ".gitignore"

// gitignoreTemplate keeps lock files out of the shared history. They are
// flock targets holding no memory, recreated on demand by the code that
// takes them, and a lock file from someone else's machine means nothing in
// your clone. One "*.lock" covers both the effectiveness ".lock" and
// "trust.yml.lock" — gitignore globs match a leading dot.
const gitignoreTemplate = `# Machine-local lock files, recreated on demand.
*.lock
`

// keepFileName marks an otherwise empty layer directory so git carries it.
// Nothing depends on the directories existing — every write path creates its
// own parent — so this is not load-bearing. It keeps the promise init's own
// output makes: what it reports creating is what a clone gets back.
const keepFileName = ".gitkeep"

// ignoreScope says how much of the memory directory git is ignoring.
type ignoreScope string

const (
	ignoreNothing ignoreScope = ""
	ignoreDir     ignoreScope = "dir"    // the whole .brain/ tree
	ignoreConfig  ignoreScope = "config" // only brain.yml
)

// initEntry is one scaffolded path and what happened to it.
type initEntry struct {
	Name    string `json:"name"`
	Created bool   `json:"created"`
}

type initResult struct {
	Path    string      `json:"path"`
	Entries []initEntry `json:"entries"`
	Created int         `json:"created"`
	Skipped int         `json:"skipped"`

	// Ignored names what git is ignoring, if anything. Empty when nothing is
	// ignored, git is absent, or this is not a repository.
	Ignored ignoreScope `json:"ignored,omitempty"`
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create a .brain/ memory directory",
		Long:  initLongHelp,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := initTargetDir(cmd)
			if err != nil {
				return err
			}
			res, err := scaffold(cmd.Context(), dir)
			if err != nil {
				return err
			}
			printResult(cmd, res, func() string { return res.text() })
			return nil
		},
	}
}

// initTargetDir resolves where to scaffold. With --dir, that path is used
// verbatim and created if missing. Without it, .brain/ in the current
// directory — deliberately not the parent search resolveBrainDir does, since
// init creates rather than finds.
func initTargetDir(cmd *cobra.Command) (string, error) {
	dirFlag, err := cmd.Flags().GetString("dir")
	if err != nil {
		return "", err
	}
	if dirFlag != "" {
		return filepath.Abs(dirFlag)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}
	return filepath.Join(cwd, ".brain"), nil
}

// scaffold creates the .brain/ tree, skipping anything already present.
func scaffold(ctx context.Context, brainDir string) (initResult, error) {
	res := initResult{Path: brainDir}

	dirs := append(markdown.LayerDirs(), "trust")
	for _, name := range dirs {
		dir := filepath.Join(brainDir, name)
		created, err := ensureDir(dir)
		if err != nil {
			return initResult{}, err
		}
		// The keep file rides along with its directory rather than getting a
		// result line of its own: it is how "created facts/" survives a
		// commit, not a separate thing anyone asked for.
		if _, err := ensureFile(filepath.Join(dir, keepFileName), ""); err != nil {
			return initResult{}, err
		}
		res.add(name+string(os.PathSeparator), created)
	}

	cfgPath := filepath.Join(brainDir, config.FileName)
	createdConfig, err := ensureFile(cfgPath, config.Template())
	if err != nil {
		return initResult{}, err
	}
	res.add(config.FileName, createdConfig)

	createdIgnore, err := ensureFile(filepath.Join(brainDir, gitignoreFileName), gitignoreTemplate)
	if err != nil {
		return initResult{}, err
	}
	res.add(gitignoreFileName, createdIgnore)

	res.Ignored = gitIgnoring(ctx, brainDir, cfgPath)

	return res, nil
}

// gitIgnoring reports how much of the memory directory git is ignoring.
//
// A git-ignored .brain/ defeats the point of a git-backed memory folder —
// memory that never reaches the team is memory that doesn't compound — and
// the failure is silent, so it is worth one subprocess at init time to catch.
// Any git failure (not installed, not a repository) means no warning.
//
// Asking git rather than parsing .gitignore is deliberate: this also catches
// rules from .git/info/exclude and the user's global excludes file, which a
// hand-rolled parser would miss.
func gitIgnoring(ctx context.Context, brainDir, cfgPath string) ignoreScope {
	if isGitIgnored(ctx, brainDir, brainDir) {
		return ignoreDir
	}
	if isGitIgnored(ctx, brainDir, cfgPath) {
		return ignoreConfig
	}
	return ignoreNothing
}

// isGitIgnored runs git check-ignore, whose exit status is 0 when the path is
// ignored, 1 when it is not, and 128 on error. Only a clean 0 counts.
//
// Context-bound so a git that hangs — a stale network mount is the realistic
// case — is cancellable rather than wedging init indefinitely.
func isGitIgnored(ctx context.Context, workDir, path string) bool {
	cmd := exec.CommandContext(ctx, "git", "check-ignore", "-q", "--", path)
	cmd.Dir = workDir
	return cmd.Run() == nil
}

// ensureDir creates dir if absent, reporting whether it created it.
func ensureDir(dir string) (bool, error) {
	switch info, err := os.Stat(dir); {
	case err == nil && info.IsDir():
		return false, nil
	case err == nil:
		return false, fmt.Errorf("%s exists but is not a directory", dir)
	case !os.IsNotExist(err):
		return false, fmt.Errorf("checking %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("creating %s: %w", dir, err)
	}
	return true, nil
}

// ensureFile writes content to path only if nothing is there already,
// reporting whether it wrote. O_EXCL makes the "don't clobber" check and the
// write one atomic step, so two concurrent inits cannot both decide the file
// is missing.
func ensureFile(path, content string) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("creating %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString(content); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	// Flush before returning. A half-written brain.yml truncated mid-comment
	// is still valid YAML, so it would load as all-defaults and look healthy
	// — and init never overwrites an existing file, so nothing would ever
	// repair it. A missing file, by contrast, is harmless: brAIn runs on
	// defaults and a second init recreates it.
	if err := f.Sync(); err != nil {
		return false, fmt.Errorf("flushing %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	return true, nil
}

func (r *initResult) add(name string, created bool) {
	r.Entries = append(r.Entries, initEntry{Name: name, Created: created})
	if created {
		r.Created++
		return
	}
	r.Skipped++
}

func (r initResult) text() string {
	var b strings.Builder
	if r.Created == 0 {
		fmt.Fprintf(&b, "Already initialized: %s\n\n", r.Path)
	} else {
		fmt.Fprintf(&b, "Initialized brAIn memory at %s\n\n", r.Path)
	}
	for _, e := range r.Entries {
		status := "exists"
		if e.Created {
			status = "created"
		}
		fmt.Fprintf(&b, "  %-8s %s\n", status, e.Name)
	}
	switch r.Ignored {
	case ignoreDir:
		fmt.Fprintf(&b, "\nWarning: git is ignoring %s\n"+
			"Nothing in it will ever be committed, so this memory stays on this\n"+
			"machine. Remove the matching rule from .gitignore, or add:\n"+
			"    !.brain/\n", r.Path)
	case ignoreConfig:
		fmt.Fprintf(&b, "\nWarning: git is ignoring %s\n"+
			"Your settings will not reach the rest of the team. A common cause is\n"+
			"an unanchored \"brain.yml\" rule; anchor it to \"/brain.yml\" or add:\n"+
			"    !.brain/%s\n", config.FileName, config.FileName)
	default:
		if r.Created > 0 {
			b.WriteString("\nCommit .brain/ to git so your memory travels with the project.\n")
		}
	}
	return b.String()
}
