package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/markdown"
	"github.com/luuuc/brain/internal/version"
)

type contextKey string

const (
	engineKey contextKey = "engine"
	jsonKey   contextKey = "json"
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
			brainDir, err := resolveBrainDir(dirFlag)
			if err != nil {
				return err
			}
			s := markdown.New(brainDir)
			eng, err := engine.NewEngine(cmd.Context(), s)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			ctx = context.WithValue(ctx, engineKey, eng)
			ctx = context.WithValue(ctx, jsonKey, jsonFlag)
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
