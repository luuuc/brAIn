package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// (a) brain track with --outcome records the outcome and returns stats.
func TestTrackIntegration_RecordAccepted(t *testing.T) {
	dir := setupBrainDir(t)

	code, out := run(t, dir, "--json", "track",
		"--persona", "kent-beck",
		"--domain", "testing",
		"--outcome", "accepted",
		"--reason", "PR #52",
	)
	if code != 0 {
		t.Fatalf("track: exit %d, out=%s", code, out)
	}

	var r TrackResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !r.Recorded || r.Outcome != "accepted" {
		t.Errorf("recorded=%v outcome=%q, want true/accepted", r.Recorded, r.Outcome)
	}
	if r.Persona != "kent-beck" || r.Domain != "testing" {
		t.Errorf("persona/domain = %q/%q", r.Persona, r.Domain)
	}
	if r.Accepted != 1 || r.Overridden != 0 || r.Total != 1 {
		t.Errorf("counts: %+v, want accepted=1 total=1", r)
	}
	if r.AcceptanceRate == nil || *r.AcceptanceRate != 1.0 {
		t.Errorf("AcceptanceRate = %v, want 1.0", r.AcceptanceRate)
	}
}

// (b) brain track without --outcome prints stats.
func TestTrackIntegration_ViewStats(t *testing.T) {
	dir := setupBrainDir(t)

	// Seed two outcomes first.
	run(t, dir, "track", "--persona", "kent-beck", "--domain", "testing", "--outcome", "accepted")
	run(t, dir, "track", "--persona", "kent-beck", "--domain", "testing", "--outcome", "overridden")

	code, out := run(t, dir, "--json", "track", "--persona", "kent-beck", "--domain", "testing")
	if code != 0 {
		t.Fatalf("view stats: exit %d, out=%s", code, out)
	}
	var r TrackResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Recorded {
		t.Errorf("expected recorded=false for view-only call")
	}
	if r.Outcome != "" {
		t.Errorf("expected no outcome for view-only call, got %q", r.Outcome)
	}
	if r.Total != 2 || r.Accepted != 1 || r.Overridden != 1 {
		t.Errorf("counts = %+v, want total=2 accepted=1 overridden=1", r)
	}
	if r.AcceptanceRate == nil || *r.AcceptanceRate != 0.5 {
		t.Errorf("rate = %v, want 0.5", r.AcceptanceRate)
	}
}

// A 0% acceptance rate is a real signal (all overrides) and must be
// emitted on the wire, not omitted as "no data." The no-data half of
// this invariant lives in the MCP handler test where it's reachable.
func TestTrackIntegration_ZeroRateKeyStaysPresent(t *testing.T) {
	dir := setupBrainDir(t)

	_, out := run(t, dir, "--json", "track",
		"--persona", "kent-beck", "--domain", "testing",
		"--outcome", "overridden")
	var r TrackResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.AcceptanceRate == nil || *r.AcceptanceRate != 0.0 {
		t.Errorf("overridden: AcceptanceRate = %v, want 0.0 present", r.AcceptanceRate)
	}
	if !strings.Contains(out, `"acceptance_rate"`) {
		t.Errorf("acceptance_rate key must be present when total > 0 (even at 0%%):\n%s", out)
	}
}

// (c) view stats for an unknown persona-domain exits 2.
func TestTrackIntegration_ViewUnknownExits2(t *testing.T) {
	dir := setupBrainDir(t)

	code, _ := run(t, dir, "track", "--persona", "ghost", "--domain", "testing")
	if code != 2 {
		t.Errorf("unknown: exit %d, want 2", code)
	}
}

// (d) invalid inputs exit 3.
func TestTrackIntegration_InvalidInputs(t *testing.T) {
	dir := setupBrainDir(t)

	t.Run("missing persona", func(t *testing.T) {
		code, _ := run(t, dir, "track", "--domain", "testing", "--outcome", "accepted")
		if code != 3 {
			t.Errorf("exit %d, want 3", code)
		}
	})
	t.Run("missing domain", func(t *testing.T) {
		code, _ := run(t, dir, "track", "--persona", "kent-beck", "--outcome", "accepted")
		if code != 3 {
			t.Errorf("exit %d, want 3", code)
		}
	})
	t.Run("bad outcome", func(t *testing.T) {
		code, _ := run(t, dir, "track",
			"--persona", "kent-beck", "--domain", "testing",
			"--outcome", "loved")
		if code != 3 {
			t.Errorf("exit %d, want 3", code)
		}
	})
	t.Run("path-escape persona", func(t *testing.T) {
		code, _ := run(t, dir, "track",
			"--persona", "../etc/passwd", "--domain", "testing",
			"--outcome", "accepted")
		if code != 3 {
			t.Errorf("exit %d, want 3", code)
		}
	})
}

// (e) text output is readable.
func TestTrackIntegration_TextOutput(t *testing.T) {
	dir := setupBrainDir(t)

	_, out := run(t, dir, "track",
		"--persona", "kent-beck", "--domain", "testing",
		"--outcome", "accepted", "--reason", "PR #52",
	)
	for _, want := range []string{
		"Recorded accepted for kent-beck in testing",
		"PR #52",
		"Acceptance rate: 1.00",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

// (f) view-only text output shows "n/a" when total is zero.
// This is the "seeded then all aged out" case would show n/a, but we can't
// fake time here. Instead we assert the formatter handles total==0 —
// checked implicitly in TestTrackIntegration_ViewUnknownExits2 (exit 2)
// and in the unit-level tests. This test just confirms the happy-path
// text includes a numeric rate when total > 0.
func TestTrackIntegration_ViewTextHasRate(t *testing.T) {
	dir := setupBrainDir(t)
	run(t, dir, "track", "--persona", "kent-beck", "--domain", "api", "--outcome", "overridden")

	_, out := run(t, dir, "track", "--persona", "kent-beck", "--domain", "api")
	if !strings.Contains(out, "Acceptance rate: 0.00") {
		t.Errorf("view text missing rate:\n%s", out)
	}
}

// The loop 01-06 built, driven entirely through the CLI: record outcomes for
// two personas, attribute a memory to each, and watch recall rank the
// well-regarded persona's memory above the newer one.
//
// Before --persona existed this was provable only from Go, because nothing
// any user could run put a persona on a lesson. Effectiveness ranking was
// switched on and had nothing to score.
func TestRemember_personaMakesEffectivenessRankingReachable(t *testing.T) {
	target := setupBrainDir(t)

	// kent-beck is accepted every time; rich-hickey is overridden every time.
	for range 5 {
		if code, out := run(t, target, "track", "--domain", "testing",
			"--persona", "kent-beck", "--outcome", "accepted"); code != 0 {
			t.Fatalf("track beck: exit %d, out=%s", code, out)
		}
		if code, out := run(t, target, "track", "--domain", "testing",
			"--persona", "rich-hickey", "--outcome", "overridden"); code != 0 {
			t.Fatalf("track hickey: exit %d, out=%s", code, out)
		}
	}

	// Beck's lesson is written first, so hickey's is newer and would lead on
	// recency alone.
	if code, out := run(t, target, "remember", "Check for concurrent writes",
		"--domain", "testing", "--layer", "lesson", "--persona", "kent-beck"); code != 0 {
		t.Fatalf("remember beck: exit %d, out=%s", code, out)
	}
	if code, out := run(t, target, "remember", "Prefer values over places",
		"--domain", "testing", "--layer", "lesson", "--persona", "rich-hickey"); code != 0 {
		t.Fatalf("remember hickey: exit %d, out=%s", code, out)
	}

	code, out := run(t, target, "recall", "--domain", "testing", "--layer", "lesson")
	if code != 0 {
		t.Fatalf("recall: exit %d, out=%s", code, out)
	}
	beck := strings.Index(out, "Check for concurrent writes")
	hickey := strings.Index(out, "Prefer values over places")
	if beck < 0 || hickey < 0 {
		t.Fatalf("both lessons should be recalled:\n%s", out)
	}
	if beck > hickey {
		t.Errorf("the overridden persona's newer lesson outranked the accepted one:\n%s", out)
	}
}

// A persona that is not a slug would be stored and then never match a score,
// failing silently. It is rejected instead.
func TestRemember_rejectsAPersonaThatCouldNeverScore(t *testing.T) {
	target := setupBrainDir(t)

	code, _ := run(t, target, "remember", "A lesson", "--domain", "testing",
		"--layer", "lesson", "--persona", "Kent Beck")
	if code != 3 {
		t.Errorf("exit %d, want 3 (invalid input)", code)
	}
}
