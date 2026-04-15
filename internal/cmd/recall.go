package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/memory"
)

func recallCmd() *cobra.Command {
	var (
		domain string
		query  string
		layer  string
		limit  int
	)

	cmd := &cobra.Command{
		Use:   "recall",
		Short: "Retrieve relevant memories",
		RunE: func(cmd *cobra.Command, args []string) error {
			eng := engineFrom(cmd)

			opts := engine.RecallOptions{
				Domain: domain,
				Query:  query,
				Limit:  limit,
			}

			if layer != "" {
				l := memory.Layer(layer)
				if !validLayer(l) {
					return &ExitError{Code: 3, Err: fmt.Errorf("invalid layer %q (valid: fact, lesson, decision, effectiveness, correction)", layer)}
				}
				opts.Layer = &l
			}

			memories, err := eng.Recall(cmd.Context(), opts)
			if err != nil {
				return err
			}

			if len(memories) == 0 {
				return &ExitError{Code: 2, Err: fmt.Errorf("no memories found")}
			}

			jr := RecallResult{}
			for _, m := range memories {
				jr.Memories = append(jr.Memories, RecallMemory{
					Path:   m.Path,
					Layer:  string(m.Layer),
					Domain: m.Domain,
					Title:  firstLine(m.Body),
					Body:   m.Body,
					Tags:   m.Tags,
				})
			}

			printResult(cmd, jr, func() string {
				var sb strings.Builder
				for i, m := range memories {
					if i > 0 {
						sb.WriteString("\n")
					}
					fmt.Fprintf(&sb, "[%s] %s\n", m.Layer, firstLine(m.Body))
					fmt.Fprintf(&sb, "  domain: %s  path: %s\n", m.Domain, m.Path)
				}
				return sb.String()
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&domain, "domain", "", "filter by domain")
	cmd.Flags().StringVar(&query, "query", "", "search query")
	cmd.Flags().StringVar(&layer, "layer", "", "filter by layer")
	cmd.Flags().IntVar(&limit, "limit", 5, "max results")
	return cmd
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
