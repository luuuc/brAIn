package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/memory"
)

func rememberCmd() *cobra.Command {
	var (
		domain string
		layer  string
		tags   []string
	)

	cmd := &cobra.Command{
		Use:   "remember [content]",
		Short: "Store a new memory",
		Long:  "Store a new memory. Content can be passed as arguments or piped via stdin.",
		RunE: func(cmd *cobra.Command, args []string) error {
			content, err := resolveContent(args, cmd.InOrStdin())
			if err != nil {
				return err
			}
			if content == "" {
				return &ExitError{Code: 3, Err: fmt.Errorf("content is required (pass as argument or pipe via stdin)")}
			}

			if domain == "" {
				return &ExitError{Code: 3, Err: fmt.Errorf("--domain is required")}
			}

			m := memory.Memory{
				Domain:  domain,
				Body:    content,
				Created: time.Now(),
				Source:  memory.SourceHuman,
				Tags:    tags,
			}

			if layer != "" {
				l := memory.Layer(layer)
				if !l.Valid() {
					return &ExitError{Code: 3, Err: fmt.Errorf("invalid layer %q (valid: fact, lesson, decision, effectiveness, correction)", layer)}
				}
				m.Layer = l
			}

			eng := engineFrom(cmd)
			result, err := eng.Remember(cmd.Context(), m)
			if err != nil {
				return err
			}

			jr := RememberResult{
				Path:     result.Path,
				Layer:    string(result.Layer),
				Domain:   m.Domain,
				Warnings: result.Warnings,
			}

			printResult(cmd, jr, func() string {
				var sb strings.Builder
				fmt.Fprintf(&sb, "Remembered: %s\n", result.Path)
				for _, w := range result.Warnings {
					fmt.Fprintf(&sb, "Warning: %s\n", w)
				}
				return sb.String()
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&domain, "domain", "", "memory domain (required)")
	cmd.Flags().StringVar(&layer, "layer", "", "memory layer (fact, lesson, decision, effectiveness, correction)")
	cmd.Flags().StringSliceVar(&tags, "tags", nil, "comma-separated tags")
	return cmd
}

// resolveContent gets memory content from args or stdin.
func resolveContent(args []string, stdin io.Reader) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	// Check if stdin has data (not a terminal)
	if f, ok := stdin.(*os.File); ok {
		info, err := f.Stat()
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			return "", nil // terminal, no piped data
		}
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("reading stdin: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

