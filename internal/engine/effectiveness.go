package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Outcome is the result of a single persona review.
type Outcome string

const (
	OutcomeAccepted   Outcome = "accepted"
	OutcomeOverridden Outcome = "overridden"
)

// Valid reports whether o is a known outcome.
func (o Outcome) Valid() bool {
	return o == OutcomeAccepted || o == OutcomeOverridden
}

// outcomeEntry is one parsed line under a file's `## Outcomes` section.
type outcomeEntry struct {
	Date    time.Time
	Outcome Outcome
	Reason  string
}

// EffectivenessStats holds derived stats for a persona in a domain over the
// current rolling window. Outcomes outside the window are kept in the file
// but excluded from the counts and rate.
type EffectivenessStats struct {
	Persona    string
	Domain     string
	Accepted   int
	Overridden int
	Total      int
	// AcceptanceRate is accepted / total. Only meaningful when Total > 0.
	// Use HasSamples to disambiguate an actual 0% rate from "no data."
	AcceptanceRate float64
	WindowDays     int
}

// HasSamples reports whether the window contained any outcomes. Callers
// formatting the rate for display should gate on this rather than
// inspecting AcceptanceRate directly — a zero rate with samples ("100%
// overridden") is a real signal, while a zero rate with no samples is
// the absence of one.
func (s EffectivenessStats) HasSamples() bool { return s.Total > 0 }

// effectivenessWindowDays is the rolling window used for rate computation.
// Pitch 01-06 fixes this at 90 days.
const effectivenessWindowDays = 90

// ErrInvalidArgs is the sentinel for input-validation failures from the
// effectiveness verbs (Track, EffectivenessStatsFor). Callers map it to
// their own "bad input" exit code / error shape.
var ErrInvalidArgs = errors.New("engine: invalid args")

// isSafeSlug reports whether s is a lower-kebab-case token safe to splice
// into a file path. Equivalent to `^[a-z0-9][a-z0-9-]*$` without the
// regex engine — ASCII only, cheap, obvious failure modes.
func isSafeSlug(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

// effectivenessPath returns the canonical effectiveness/<persona>-<domain>.md
// path used by Track. Callers must pre-validate the inputs with isSafeSlug.
func effectivenessPath(persona, domain string) string {
	return fmt.Sprintf("effectiveness/%s-%s.md", persona, domain)
}

// outcomesSection holds the raw contents of an effectiveness file's
// `## Outcomes` list, preserving every line verbatim. Parsing recovers
// entries for stats; rendering puts lines back exactly as they came in,
// with appended new entries tacked on the end. This is what keeps a
// human-edited note from being silently rewritten into oblivion.
type outcomesSection struct {
	lines []string // raw lines inside the ## Outcomes section (no blanks)
}

// loadOutcomes extracts the `## Outcomes` section from a markdown body.
// Blank lines inside the section are dropped; everything else is kept
// verbatim in its original order.
func loadOutcomes(body string) *outcomesSection {
	sec := &outcomesSection{}
	inSection := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			inSection = strings.EqualFold(strings.TrimPrefix(trimmed, "## "), "Outcomes")
			continue
		}
		if !inSection || trimmed == "" {
			continue
		}
		sec.lines = append(sec.lines, line)
	}
	return sec
}

// append adds a new outcome line to the end of the section.
func (s *outcomesSection) append(e outcomeEntry) {
	s.lines = append(s.lines, formatOutcomeLine(e))
}

// entries parses the known-shape lines into outcomeEntry values. Lines that
// don't match are skipped here but kept in `lines` for re-rendering, so
// parseOutcome-failures never cause data loss.
func (s *outcomesSection) entries() []outcomeEntry {
	out := make([]outcomeEntry, 0, len(s.lines))
	for _, line := range s.lines {
		if e, ok := parseOutcomeLine(line); ok {
			out = append(out, e)
		}
	}
	return out
}

// parseOutcomeLine parses one `- YYYY-MM-DD: <outcome>[ — reason]` line.
// Hand-rolled with strings.Cut; simpler and cheaper than a regex, and the
// failure modes are obvious on read.
func parseOutcomeLine(line string) (outcomeEntry, bool) {
	s, ok := strings.CutPrefix(strings.TrimSpace(line), "- ")
	if !ok {
		return outcomeEntry{}, false
	}
	dateStr, rest, ok := strings.Cut(s, ": ")
	if !ok {
		return outcomeEntry{}, false
	}
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return outcomeEntry{}, false
	}
	outcomeStr, reason, _ := strings.Cut(rest, " — ")
	o := Outcome(strings.TrimSpace(outcomeStr))
	if !o.Valid() {
		return outcomeEntry{}, false
	}
	return outcomeEntry{Date: date, Outcome: o, Reason: strings.TrimSpace(reason)}, true
}

// formatOutcomeLine is the inverse of parseOutcomeLine. It writes the one
// canonical separator (em-dash). There is only one writer; we don't chase
// every way a human might format a reason on the wire.
func formatOutcomeLine(e outcomeEntry) string {
	base := fmt.Sprintf("- %s: %s", e.Date.Format("2006-01-02"), e.Outcome)
	if e.Reason != "" {
		return base + " — " + e.Reason
	}
	return base
}

// computeStats derives counts and rate from entries, filtered to those within
// the last windowDays of now (inclusive at the boundary).
func computeStats(entries []outcomeEntry, now time.Time, windowDays int) EffectivenessStats {
	cutoff := now.AddDate(0, 0, -windowDays)
	var accepted, overridden int
	for _, e := range entries {
		if e.Date.Before(cutoff) {
			continue
		}
		switch e.Outcome {
		case OutcomeAccepted:
			accepted++
		case OutcomeOverridden:
			overridden++
		}
	}
	total := accepted + overridden
	var rate float64
	if total > 0 {
		rate = float64(accepted) / float64(total)
	}
	return EffectivenessStats{
		Accepted:       accepted,
		Overridden:     overridden,
		Total:          total,
		AcceptanceRate: rate,
		WindowDays:     windowDays,
	}
}

// render assembles the full markdown body for an effectiveness file
// from the raw outcomes section. No sorting or rewriting of existing
// lines — an operator who edits the file by hand sees their changes
// survive the next Track call.
func (s *outcomesSection) render() string {
	var sb strings.Builder
	sb.WriteString("## Outcomes\n")
	for _, line := range s.lines {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// validateTrackArgs checks persona/domain/outcome. Returns ErrInvalidArgs-wrapped
// errors so callers can distinguish input bugs from storage/lock failures.
func validateTrackArgs(persona, domain string, outcome Outcome) error {
	if !isSafeSlug(persona) {
		return fmt.Errorf("%w: invalid persona %q (expected lower-kebab-case)", ErrInvalidArgs, persona)
	}
	if !isSafeSlug(domain) {
		return fmt.Errorf("%w: invalid domain %q (expected lower-kebab-case)", ErrInvalidArgs, domain)
	}
	if !outcome.Valid() {
		return fmt.Errorf("%w: invalid outcome %q (valid: accepted, overridden)", ErrInvalidArgs, outcome)
	}
	return nil
}
