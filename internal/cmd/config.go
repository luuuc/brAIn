package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/config"
	"github.com/luuuc/brain/internal/trust"
)

const configLongHelp = `Show the resolved configuration and where each value came from.

Values resolve in three layers: built-in defaults, then .brain/brain.yml,
then BRAIN_* environment variables. This command shows the winner and which
layer produced it, so you never have to guess whether a setting took effect.

Trust levels are listed alongside the promotion thresholds, because the two
interact in a way that surprises people: the thresholds gate the edges
between levels, so raising one does not demote a domain that is already
above it. Use "brain trust override" to move a domain down.

This command is read-only. Edit .brain/brain.yml to change anything.`

// configSetting is one resolved key as reported to the user.
type configSetting struct {
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Source string `json:"source"`
	EnvVar string `json:"env_var,omitempty"`
}

// configDomain is a domain's standing against the configured thresholds.
type configDomain struct {
	Domain     string `json:"domain"`
	Level      string `json:"level"`
	CleanShips int    `json:"clean_ships"`
	// PromoteAt is the streak that promotes out of the current level, or 0
	// at full_auto where there is nothing left to climb.
	PromoteAt int `json:"promote_at,omitempty"`
}

type configResult struct {
	Path     string          `json:"path"`
	Settings []configSetting `json:"settings"`
	Domains  []configDomain  `json:"domains"`
}

func configCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Show the resolved configuration and each value's source",
		Long:  configLongHelp,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := configFrom(cmd)

			// Non-nil so JSON emits [] rather than null — this output is
			// meant to be scripted against.
			res := configResult{Path: brainDirFrom(cmd), Domains: []configDomain{}}
			for _, key := range config.Keys() {
				res.Settings = append(res.Settings, configSetting{
					Key:    key,
					Value:  cfg.Value(key),
					Source: string(cfg.Source(key)),
					EnvVar: config.EnvVar(key),
				})
			}

			decisions, err := trustEngineFrom(cmd).List(cmd.Context())
			if err != nil {
				return err
			}
			for _, d := range decisions {
				res.Domains = append(res.Domains, configDomain{
					Domain:     d.Domain,
					Level:      string(d.Level),
					CleanShips: d.CleanShips,
					PromoteAt:  trust.PromoteThreshold(cfg.Trust, d.Level),
				})
			}

			printResult(cmd, res, func() string { return res.text() })
			return nil
		},
	}
}

func (r configResult) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Configuration for %s\n\n", r.Path)

	width := 0
	for _, s := range r.Settings {
		if len(s.Key) > width {
			width = len(s.Key)
		}
	}
	for _, s := range r.Settings {
		fmt.Fprintf(&b, "  %-*s  %-12s  %s\n", width, s.Key,
			displayValue(s.Value), describeSource(s))
	}

	b.WriteString("\nTrust levels\n")
	if len(r.Domains) == 0 {
		b.WriteString("  (no domains recorded yet)\n")
	}
	for _, d := range r.Domains {
		if d.PromoteAt == 0 {
			fmt.Fprintf(&b, "  %-14s %s\n", d.Domain, d.Level)
			continue
		}
		fmt.Fprintf(&b, "  %-14s %-10s %d/%d clean ships to the next level\n",
			d.Domain, d.Level, d.CleanShips, d.PromoteAt)
	}
	b.WriteString("\nRaising a threshold does not demote a domain already above it.\n" +
		"Use \"brain trust override\" to move one down.\n")

	return b.String()
}

// displayValue renders a resolved value, marking empty strings as unset so a
// blank column is never ambiguous.
func displayValue(v any) string {
	if s, ok := v.(string); ok && s == "" {
		return "(unset)"
	}
	return fmt.Sprint(v)
}

// describeSource names the layer a value came from, and for environment
// values the variable to unset.
func describeSource(s configSetting) string {
	if s.Source == string(config.SourceEnv) {
		return "env ($" + s.EnvVar + ")"
	}
	return s.Source
}
