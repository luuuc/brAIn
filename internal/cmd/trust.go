package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/luuuc/brain/internal/store"
	"github.com/luuuc/brain/internal/trust"
)

// trustLongHelp is appended to every trust command's Long description so
// operators can see the exit-code contract and supported env vars without
// grepping source.
const trustLongHelp = `
Exit codes:
  0  success
  3  invalid input (missing flag, bad outcome, invalid urgency, ...)
  4  trust state conflict (lock acquisition timeout)

Environment:
  BRAIN_TRUST_LOCK_TIMEOUT_MS  override the advisory-lock acquisition
                               timeout in milliseconds (default: 5000).
                               Intended for tests and operator debugging;
                               leave unset in production.`

func trustCmd() *cobra.Command {
	var (
		domain  string
		urgency string
		verbose bool
	)

	cmd := &cobra.Command{
		Use:   "trust",
		Short: "Check, record, and manage trust levels",
		Long: `Trust tracks per-domain autonomy levels for AI-produced work.

With --domain X, prints the current trust level and the recommendation
(escalate / ship_notify / ship). Subcommands record outcomes, record
overrides, repair corrupted state, or list all known domains.` + trustLongHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			if domain == "" {
				return &ExitError{Code: 3, Err: fmt.Errorf("--domain is required (or pass a subcommand: record, override, list, repair)")}
			}
			teng := trustEngineFrom(cmd)

			opts := trust.CheckOptions{}
			if urgency != "" {
				if urgency != "hotfix" {
					return &ExitError{Code: 3, Err: fmt.Errorf("invalid --urgency %q (valid: hotfix)", urgency)}
				}
				opts.Hotfix = true
			}

			dec, err := teng.Check(cmd.Context(), domain, opts)
			if err != nil {
				return mapTrustErr(err)
			}

			printResult(cmd, trust.DecisionJSON(dec, verbose), func() string { return formatCheck(dec, verbose) })
			return nil
		},
	}
	cmd.Flags().StringVar(&domain, "domain", "", "trust domain to check")
	cmd.Flags().StringVar(&urgency, "urgency", "", "urgency override (hotfix)")
	cmd.Flags().BoolVar(&verbose, "verbose", false, "include history context")

	cmd.AddCommand(trustRecordCmd())
	cmd.AddCommand(trustOverrideCmd())
	cmd.AddCommand(trustListCmd())
	cmd.AddCommand(trustRepairCmd())
	return cmd
}

func trustRecordCmd() *cobra.Command {
	var (
		domain  string
		outcome string
		ref     string
		reason  string
	)
	cmd := &cobra.Command{
		Use:   "record",
		Short: "Record an outcome (clean or failure) for a domain",
		Long:  "Record an outcome (clean or failure) for a domain." + trustLongHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			if domain == "" {
				return &ExitError{Code: 3, Err: fmt.Errorf("--domain is required")}
			}
			o := trust.Outcome(outcome)
			if !o.Valid() {
				return &ExitError{Code: 3, Err: fmt.Errorf("invalid --outcome %q (valid: clean, failure)", outcome)}
			}
			teng := trustEngineFrom(cmd)
			r, err := teng.Record(cmd.Context(), domain, o, trust.RecordOptions{Ref: ref, Reason: reason})
			if err != nil {
				return mapTrustErr(err)
			}

			printResult(cmd, jsonRecord(r), func() string { return formatRecord(r) })
			return nil
		},
	}
	cmd.Flags().StringVar(&domain, "domain", "", "trust domain (required)")
	cmd.Flags().StringVar(&outcome, "outcome", "", "clean or failure (required)")
	cmd.Flags().StringVar(&ref, "ref", "", "optional reference (e.g. PR #42) for deduplication")
	cmd.Flags().StringVar(&reason, "reason", "", "optional reason (typically set on failures)")
	return cmd
}

func trustOverrideCmd() *cobra.Command {
	var (
		domain string
		reason string
	)
	cmd := &cobra.Command{
		Use:   "override",
		Short: "Record a human override for a domain",
		Long:  "Record a human override for a domain. Writes a correction memory and appends an override event to the trust history." + trustLongHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			if domain == "" {
				return &ExitError{Code: 3, Err: fmt.Errorf("--domain is required")}
			}
			if reason == "" {
				return &ExitError{Code: 3, Err: fmt.Errorf("--reason is required")}
			}
			teng := trustEngineFrom(cmd)
			dec, err := teng.Override(cmd.Context(), domain, reason)
			if err != nil {
				return mapTrustErr(err)
			}

			printResult(cmd, trust.DecisionJSON(dec, false), func() string {
				return fmt.Sprintf("Override recorded for %s\nLevel: %s\nRecommendation: %s\n", dec.Domain, dec.Level, dec.Recommendation)
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&domain, "domain", "", "trust domain (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why the human overrode (required)")
	return cmd
}

func trustListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every domain with its trust level",
		Long:  "List every domain with its trust level." + trustLongHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			teng := trustEngineFrom(cmd)
			list, err := teng.List(cmd.Context())
			if err != nil {
				return mapTrustErr(err)
			}
			items := make([]map[string]any, 0, len(list))
			for _, d := range list {
				items = append(items, trust.DecisionJSON(d, false))
			}
			printResult(cmd, map[string]any{"domains": items, "count": len(list)}, func() string {
				var sb strings.Builder
				for _, d := range list {
					fmt.Fprintf(&sb, "%-14s %-10s clean_ships=%-4d", d.Domain, d.Level, d.CleanShips)
					if d.LastFailure != nil {
						fmt.Fprintf(&sb, " last_failure=%s", d.LastFailure.Format("2006-01-02"))
					}
					sb.WriteString("\n")
				}
				fmt.Fprintf(&sb, "\n%d domains\n", len(list))
				return sb.String()
			})
			return nil
		},
	}
	return cmd
}

func trustRepairCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Repair corrupted trust.yml by restoring from the backup",
		Long: `Repair corrupted trust.yml by restoring from a crash-left sibling.

If trust.yml is valid, repair does nothing but sweeps stale .tmp files.
If the live file is missing, repair promotes trust.yml.tmp (the latest
write-in-progress, if present) or falls back to trust.yml.bak (the prior
committed state). Schema-version mismatches block auto-promote — the
operator must migrate by hand.` + trustLongHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, ok := cmd.Context().Value(brainDirKey).(string)
			if !ok || dir == "" {
				return fmt.Errorf("no .brain/ directory resolved")
			}
			source, err := trust.Repair(cmd.Context(), filepath.Join(dir, "trust"))
			if err != nil {
				return mapTrustErr(err)
			}

			// Report exactly what Repair did — operators at 3am need to
			// know whether state came from .tmp (mid-rename crash) or
			// .bak (ordinary corruption) so they can diagnose the root
			// cause, not just observe "it works now."
			var msg string
			switch source {
			case trust.RepairAlreadyValid:
				msg = "trust.yml is valid; no repair needed.\n"
			case trust.RepairFromTmp:
				msg = "Restored trust.yml from trust.yml.tmp (recovered from mid-write crash).\n"
			case trust.RepairFromBak:
				msg = "Restored trust.yml from trust.yml.bak (previous good state).\n"
			default:
				msg = fmt.Sprintf("Repair returned unexpected source %q\n", source)
			}
			payload := map[string]any{
				"source":        string(source),
				"already_valid": source == trust.RepairAlreadyValid,
			}
			printResult(cmd, payload, func() string { return msg })
			return nil
		},
	}
	return cmd
}

func formatCheck(d trust.Decision, verbose bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Domain: %s\n", d.Domain)
	fmt.Fprintf(&sb, "Level: %s\n", d.Level)
	fmt.Fprintf(&sb, "Clean ships: %d\n", d.CleanShips)
	if d.LastFailure != nil {
		fmt.Fprintf(&sb, "Last failure: %s\n", d.LastFailure.Format("2006-01-02"))
	}
	if d.LastPromotion != nil {
		fmt.Fprintf(&sb, "Last promotion: %s\n", d.LastPromotion.Format("2006-01-02"))
	}
	if d.Hotfix {
		sb.WriteString("Urgency: hotfix\n")
	}
	fmt.Fprintf(&sb, "Recommendation: %s\n", d.Recommendation)
	if verbose && len(d.History) > 0 {
		sb.WriteString("History:\n")
		for _, e := range d.History {
			fmt.Fprintf(&sb, "  %s  %s", e.At.Format("2006-01-02"), e.Kind)
			if e.Outcome != "" {
				fmt.Fprintf(&sb, "  outcome=%s", e.Outcome)
			}
			if e.From != "" || e.To != "" {
				fmt.Fprintf(&sb, "  %s→%s", e.From, e.To)
			}
			if e.Ref != "" {
				fmt.Fprintf(&sb, "  ref=%q", e.Ref)
			}
			if e.Reason != "" {
				fmt.Fprintf(&sb, "  reason=%q", e.Reason)
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func formatRecord(r trust.RecordResult) string {
	var sb strings.Builder
	if r.Deduplicated {
		sb.WriteString("Duplicate ref — outcome not recorded.\n")
	}
	fmt.Fprintf(&sb, "Domain: %s\n", r.Decision.Domain)
	fmt.Fprintf(&sb, "Level: %s\n", r.Decision.Level)
	fmt.Fprintf(&sb, "Clean ships: %d\n", r.Decision.CleanShips)
	if r.Promoted {
		fmt.Fprintf(&sb, "Promoted to %s.\n", r.Decision.Level)
	}
	if r.Demoted {
		sb.WriteString("Demoted to ask after failure.\n")
	}
	if r.LessonsTouched > 0 {
		fmt.Fprintf(&sb, "Lessons ticked: %d", r.LessonsTouched)
		if r.LessonsRetired > 0 {
			fmt.Fprintf(&sb, " (retired: %d)", r.LessonsRetired)
		}
		sb.WriteString("\n")
	}
	if r.EvictedRefs > 0 {
		fmt.Fprintf(&sb, "Seen-refs evicted (FIFO): %d\n", r.EvictedRefs)
	}
	fmt.Fprintf(&sb, "Recommendation: %s\n", r.Decision.Recommendation)
	return sb.String()
}

// mapTrustErr translates engine errors into CLI ExitErrors. Lock timeouts
// surface as exit code 4 (conflict).
func mapTrustErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrConflict) {
		return &ExitError{Code: 4, Err: err}
	}
	return err
}

func jsonRecord(r trust.RecordResult) any {
	out := map[string]any{
		"decision":     trust.DecisionJSON(r.Decision, false),
		"promoted":     r.Promoted,
		"demoted":      r.Demoted,
		"deduplicated": r.Deduplicated,
	}
	if r.LessonsTouched > 0 {
		out["lessons_touched"] = r.LessonsTouched
	}
	if r.LessonsRetired > 0 {
		out["lessons_retired"] = r.LessonsRetired
	}
	if r.EvictedRefs > 0 {
		out["evicted_refs"] = r.EvictedRefs
	}
	return out
}

