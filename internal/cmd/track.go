package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/store"
)

// TrackResult is the JSON output of brain track.
//
// AcceptanceRate is a pointer so "no samples in window" (nil) is distinct
// from "100% overridden" (0.0). Consumers that parse this struct directly
// get an unambiguous wire format.
type TrackResult struct {
	Persona        string   `json:"persona"`
	Domain         string   `json:"domain"`
	Recorded       bool     `json:"recorded"`
	Outcome        string   `json:"outcome,omitempty"`
	Accepted       int      `json:"accepted"`
	Overridden     int      `json:"overridden"`
	Total          int      `json:"total"`
	AcceptanceRate *float64 `json:"acceptance_rate,omitempty"`
}

func trackCmd() *cobra.Command {
	var (
		persona string
		domain  string
		outcome string
		reason  string
	)

	cmd := &cobra.Command{
		Use:   "track",
		Short: "Track persona effectiveness in a domain",
		Long: `Track records acceptance or override outcomes for a Council
persona in a domain, and reports the rolling 90-day acceptance rate.

With --outcome, records a new outcome. Without --outcome, prints current
stats for the persona-domain pair.

Exit codes:
  0  success
  2  stats requested for an unknown persona-domain (no outcomes recorded)
  3  invalid input (missing flag, bad outcome, bad slug)
  4  lock conflict (another process holds the effectiveness lock)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			persona = strings.TrimSpace(persona)
			domain = strings.TrimSpace(domain)
			if persona == "" {
				return &ExitError{Code: 3, Err: fmt.Errorf("--persona is required")}
			}
			if domain == "" {
				return &ExitError{Code: 3, Err: fmt.Errorf("--domain is required")}
			}
			eng := engineFrom(cmd)

			if outcome == "" {
				return runTrackView(cmd, eng, persona, domain)
			}
			o := engine.Outcome(outcome)
			if !o.Valid() {
				return &ExitError{Code: 3, Err: fmt.Errorf("invalid --outcome %q (valid: accepted, overridden)", outcome)}
			}
			stats, err := eng.Track(cmd.Context(), persona, domain, o, reason)
			if err != nil {
				return mapTrackErr(err)
			}
			printResult(cmd, trackResultJSON(stats, true, o), func() string {
				return formatTrackResult(stats, true, o, reason)
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&persona, "persona", "", "persona slug (e.g. kent-beck)")
	cmd.Flags().StringVar(&domain, "domain", "", "domain slug (e.g. testing)")
	cmd.Flags().StringVar(&outcome, "outcome", "", "accepted or overridden (omit to view stats)")
	cmd.Flags().StringVar(&reason, "reason", "", "optional reason appended to the outcome log")
	return cmd
}

func runTrackView(cmd *cobra.Command, eng *engine.Engine, persona, domain string) error {
	stats, err := eng.EffectivenessStatsFor(cmd.Context(), persona, domain)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return &ExitError{Code: 2, Err: fmt.Errorf("no effectiveness data for persona=%s domain=%s", persona, domain)}
		}
		return mapTrackErr(err)
	}
	printResult(cmd, trackResultJSON(stats, false, ""), func() string {
		return formatTrackResult(stats, false, "", "")
	})
	return nil
}

// mapTrackErr turns engine errors into CLI exit codes. Validation errors
// (ErrInvalidArgs) → 3; lock timeouts (store.ErrConflict) → 4; everything
// else falls through to the default exit 1 so operators can grep the code.
func mapTrackErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, engine.ErrInvalidArgs):
		return &ExitError{Code: 3, Err: err}
	case errors.Is(err, store.ErrConflict):
		return &ExitError{Code: 4, Err: err}
	default:
		return err
	}
}

func trackResultJSON(s engine.EffectivenessStats, recorded bool, o engine.Outcome) TrackResult {
	r := TrackResult{
		Persona:    s.Persona,
		Domain:     s.Domain,
		Recorded:   recorded,
		Accepted:   s.Accepted,
		Overridden: s.Overridden,
		Total:      s.Total,
	}
	if s.HasSamples() {
		rate := s.AcceptanceRate
		r.AcceptanceRate = &rate
	}
	if recorded {
		r.Outcome = string(o)
	}
	return r
}

func formatTrackResult(s engine.EffectivenessStats, recorded bool, o engine.Outcome, reason string) string {
	var sb strings.Builder
	if recorded {
		fmt.Fprintf(&sb, "Recorded %s for %s in %s", o, s.Persona, s.Domain)
		if reason != "" {
			fmt.Fprintf(&sb, " — %s", reason)
		}
		sb.WriteString(".\n")
	} else {
		fmt.Fprintf(&sb, "Effectiveness for %s in %s:\n", s.Persona, s.Domain)
	}
	fmt.Fprintf(&sb, "Accepted: %d\n", s.Accepted)
	fmt.Fprintf(&sb, "Overridden: %d\n", s.Overridden)
	fmt.Fprintf(&sb, "Total: %d\n", s.Total)
	if s.HasSamples() {
		fmt.Fprintf(&sb, "Acceptance rate: %.2f\n", s.AcceptanceRate)
	} else {
		sb.WriteString("Acceptance rate: n/a\n")
	}
	return sb.String()
}
