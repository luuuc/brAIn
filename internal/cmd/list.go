package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/memory"
)

func listCmd() *cobra.Command {
	var (
		domain         string
		layer          string
		includeRetired bool
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List memories",
		RunE: func(cmd *cobra.Command, args []string) error {
			eng := engineFrom(cmd)

			opts := engine.RecallOptions{
				Domain:         domain,
				Limit:          0, // no limit for list
				IncludeRetired: includeRetired,
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

			jr := ListResult{Count: len(memories)}
			for _, m := range memories {
				jr.Memories = append(jr.Memories, ListMemory{
					Path:    m.Path,
					Layer:   string(m.Layer),
					Domain:  m.Domain,
					Created: m.Created.Format("2006-01-02"),
					Retired: m.Retired,
				})
			}

			printResult(cmd, jr, func() string {
				var sb strings.Builder
				for _, m := range memories {
					retired := ""
					if m.Retired {
						retired = " (retired)"
					}
					fmt.Fprintf(&sb, "%-40s [%-10s] %-12s %s%s\n",
						m.Path, m.Layer, m.Domain, m.Created.Format("2006-01-02"), retired)
				}
				fmt.Fprintf(&sb, "\n%d memories\n", len(memories))
				return sb.String()
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&domain, "domain", "", "filter by domain")
	cmd.Flags().StringVar(&layer, "layer", "", "filter by layer")
	cmd.Flags().BoolVar(&includeRetired, "include-retired", false, "include retired memories")
	return cmd
}
