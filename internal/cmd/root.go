package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/config"
	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/markdown"
	"github.com/luuuc/brain/internal/trust"
	"github.com/luuuc/brain/internal/version"
)

type contextKey string

const (
	engineKey      contextKey = "engine"
	jsonKey        contextKey = "json"
	trustEngineKey contextKey = "trust"
	configKey      contextKey = "config"
	brainDirKey    contextKey = "brain_dir"
)

func rootCmd() *cobra.Command {
	var (
		dirFlag  string
		jsonFlag bool
	)

	cmd := &cobra.Command{
		Use:     "brain",
		Short:   "Persistent, layered memory for AI-assisted projects",
		Version: version.Version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// --json is honored by every command (including meta commands
			// like version), so propagate it unconditionally.
			ctx := context.WithValue(cmd.Context(), jsonKey, jsonFlag)
			cmd.SetContext(ctx)

			// Some commands must work on a fresh install before any
			// .brain/ directory exists — skip engine setup for them.
			if runsWithoutBrainDir(cmd) {
				return nil
			}

			brainDir, err := resolveBrainDir(dirFlag)
			if err != nil {
				return err
			}
			cfg, err := config.Load(brainDir)
			if err != nil {
				return err
			}

			s := markdown.New(brainDir)
			eng, err := engine.NewEngine(ctx, s,
				engine.WithLockDir(brainDir),
				engine.WithFacts(cfg.Facts))
			if err != nil {
				return err
			}
			trustDir := filepath.Join(brainDir, "trust")
			teng, err := trust.NewEngine(ctx, trustDir, s,
				trust.WithLockTimeoutFromEnv(),
				trust.WithThresholds(cfg.Trust),
				trust.WithLessons(cfg.Lessons))
			if err != nil {
				return err
			}
			ctx = context.WithValue(ctx, engineKey, eng)
			ctx = context.WithValue(ctx, trustEngineKey, teng)
			ctx = context.WithValue(ctx, brainDirKey, brainDir)
			ctx = context.WithValue(ctx, configKey, cfg)
			cmd.SetContext(ctx)
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().StringVar(&dirFlag, "dir", "", "path to .brain/ directory (default: auto-detect)")
	cmd.PersistentFlags().BoolVar(&jsonFlag, "json", false, "output as JSON")
	return cmd
}

// Execute is the entry point called from main.
func Execute() int {
	cmd := rootCmd()
	registerSubcommands(cmd)
	if err := cmd.Execute(); err != nil {
		// Read --json from the parsed flag set (available even if
		// PersistentPreRunE failed, since Cobra parses flags first).
		jsonMode, _ := cmd.PersistentFlags().GetBool("json")
		printError(err, jsonMode)
		if code, ok := exitCodeFromError(err); ok {
			return code
		}
		return 1
	}
	return 0
}

// engineFrom extracts the engine from the command's context.
// Returns nil if the engine was not set (e.g. --help or --version).
func engineFrom(cmd *cobra.Command) *engine.Engine {
	v, _ := cmd.Context().Value(engineKey).(*engine.Engine)
	return v
}

// configFrom extracts the resolved configuration from the command's context.
// Returns the built-in defaults if config was not set, which only happens for
// commands in runsWithoutBrainDir.
func configFrom(cmd *cobra.Command) config.Config {
	if v, ok := cmd.Context().Value(configKey).(config.Config); ok {
		return v
	}
	return config.Default()
}

// brainDirFrom extracts the resolved .brain/ path from the command's context.
func brainDirFrom(cmd *cobra.Command) string {
	v, _ := cmd.Context().Value(brainDirKey).(string)
	return v
}

// trustEngineFrom extracts the trust engine from the command's context.
func trustEngineFrom(cmd *cobra.Command) *trust.Engine {
	v, _ := cmd.Context().Value(trustEngineKey).(*trust.Engine)
	return v
}

// isJSON returns whether --json was set on this command.
func isJSON(cmd *cobra.Command) bool {
	v, _ := cmd.Context().Value(jsonKey).(bool)
	return v
}

// printResult renders a result as JSON or text.
func printResult(cmd *cobra.Command, v any, textFn func() string) {
	if isJSON(cmd) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)
		return
	}
	fmt.Print(textFn())
}

// printError writes an error to stderr (text mode) or stdout (JSON mode).
func printError(err error, jsonMode bool) {
	if jsonMode {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]string{"error": err.Error()})
		return
	}
	fmt.Fprintf(os.Stderr, "Error: %s\n", err)
}

// resolveBrainDir finds the .brain/ directory. If dirFlag is set, it uses
// that directly (and validates it exists). Otherwise it walks up from cwd,
// stopping at a .git directory or filesystem root.
func resolveBrainDir(dir string) (string, error) {
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("resolving --dir: %w", err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("--dir %q does not exist", dir)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("--dir %q is not a directory", dir)
		}
		return abs, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	cur := cwd
	for {
		candidate := filepath.Join(cur, ".brain")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}

		// Stop at .git boundary
		if info, err := os.Stat(filepath.Join(cur, ".git")); err == nil && info.IsDir() {
			break
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			break // filesystem root
		}
		cur = parent
	}

	return "", fmt.Errorf("no .brain/ directory found (searched from %s). Create one or use --dir", cwd)
}

// registerSubcommands adds all subcommands to the root command.
func registerSubcommands(root *cobra.Command) {
	root.AddCommand(rememberCmd())
	root.AddCommand(recallCmd())
	root.AddCommand(listCmd())
	root.AddCommand(forgetCmd())
	root.AddCommand(mcpCmd())
	root.AddCommand(trustCmd())
	root.AddCommand(trackCmd())
	root.AddCommand(versionCmd())
	root.AddCommand(initCmd())
	root.AddCommand(configCmd())
	root.AddCommand(hooksCmd())
}

// runsWithoutBrainDir reports whether cmd (or any ancestor) must work when
// no .brain/ directory exists yet. Walks the parent chain so "brain help
// remember" and "brain completion bash" are both recognized.
//
// Three kinds of command qualify: cobra's discovery commands plus brain
// version, which have to work on a fresh install; init, which creates the
// directory the others require; and hooks, which runs in whatever project
// an editor was opened in, most of which have no .brain/ at all.
//
// Contract: the names below are reserved. A command listed here skips engine
// setup entirely, so it must never call engineFrom, trustEngineFrom, or
// configFrom expecting a loaded value — all three return nil or defaults and
// the first two will nil-deref. init obeys this by touching only the
// filesystem; hooks obeys it by building its own engine and discarding
// every error (see sessionStartRecall).
func runsWithoutBrainDir(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "help", "version", "completion", "init", "hooks":
			return true
		}
	}
	return false
}

// ExitError wraps an error with an exit code.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

func exitCodeFromError(err error) (int, bool) {
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code, true
	}
	return 0, false
}
