package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luuuc/brain/internal/markdown"
	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
)

func TestOutcome_Valid(t *testing.T) {
	tests := []struct {
		o    Outcome
		want bool
	}{
		{OutcomeAccepted, true},
		{OutcomeOverridden, true},
		{Outcome(""), false},
		{Outcome("accepted "), false},
		{Outcome("ACCEPTED"), false},
		{Outcome("unknown"), false},
	}
	for _, tt := range tests {
		if got := tt.o.Valid(); got != tt.want {
			t.Errorf("Outcome(%q).Valid() = %v, want %v", tt.o, got, tt.want)
		}
	}
}

func TestLoadOutcomes_ParsesCanonical(t *testing.T) {
	body := `# Kent Beck effectiveness in testing

## Outcomes
- 2026-04-14: accepted — PR #52
- 2026-04-10: accepted
- 2026-04-03: overridden — false positive on STI tests
`
	got := loadOutcomes(body).entries()
	want := []outcomeEntry{
		{Date: mustDate(t, "2026-04-14"), Outcome: OutcomeAccepted, Reason: "PR #52"},
		{Date: mustDate(t, "2026-04-10"), Outcome: OutcomeAccepted},
		{Date: mustDate(t, "2026-04-03"), Outcome: OutcomeOverridden, Reason: "false positive on STI tests"},
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].Date.Equal(want[i].Date) {
			t.Errorf("[%d].Date = %v, want %v", i, got[i].Date, want[i].Date)
		}
		if got[i].Outcome != want[i].Outcome {
			t.Errorf("[%d].Outcome = %q, want %q", i, got[i].Outcome, want[i].Outcome)
		}
		if got[i].Reason != want[i].Reason {
			t.Errorf("[%d].Reason = %q, want %q", i, got[i].Reason, want[i].Reason)
		}
	}
}

func TestLoadOutcomes_PreservesUnknownLinesForRewrite(t *testing.T) {
	// Unknown lines (human notes, typos, future schema) must survive a
	// Track rewrite — parseOutcomes silently skipping is OK only because
	// loadOutcomes keeps every line for re-rendering.
	body := `## Outcomes
- 2026-04-14: accepted — PR #52
- TODO: check the hickey review too
- 2026-04-09: accepted
`
	sec := loadOutcomes(body)
	if len(sec.lines) != 3 {
		t.Fatalf("lines preserved: got %d, want 3", len(sec.lines))
	}
	// Entries parse two; middle line is kept as-is but not parsed.
	if entries := sec.entries(); len(entries) != 2 {
		t.Errorf("entries parsed: got %d, want 2 (TODO line is ignored for stats)", len(entries))
	}
	rendered := sec.render()
	if !strings.Contains(rendered, "TODO: check the hickey review too") {
		t.Errorf("unknown line lost on render:\n%s", rendered)
	}
}

func TestLoadOutcomes_IgnoresNonSection(t *testing.T) {
	body := `# Title

## Overview
- 2026-04-14: accepted (belongs to a different section)

## Outcomes
- 2026-04-09: accepted

## Notes
- 2026-04-08: accepted (also a different section)
`
	entries := loadOutcomes(body).entries()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 — only the Outcomes section should count", len(entries))
	}
	if !entries[0].Date.Equal(mustDate(t, "2026-04-09")) {
		t.Errorf("picked up wrong entry: %+v", entries[0])
	}
}

func TestLoadOutcomes_EmptyBody(t *testing.T) {
	if got := loadOutcomes("").entries(); len(got) != 0 {
		t.Errorf("empty body: got %d entries, want 0", len(got))
	}
	if got := loadOutcomes("# title only\n").entries(); len(got) != 0 {
		t.Errorf("no outcomes section: got %d entries, want 0", len(got))
	}
}

func TestFormatOutcomeLine_RoundTrip(t *testing.T) {
	tests := []outcomeEntry{
		{Date: mustDate(t, "2026-04-14"), Outcome: OutcomeAccepted, Reason: "PR #52"},
		{Date: mustDate(t, "2026-04-10"), Outcome: OutcomeAccepted},
		{Date: mustDate(t, "2026-04-03"), Outcome: OutcomeOverridden, Reason: "false positive"},
	}
	for _, e := range tests {
		line := formatOutcomeLine(e)
		parsed, ok := parseOutcomeLine(line)
		if !ok {
			t.Fatalf("round-trip failed to parse %q", line)
		}
		if !parsed.Date.Equal(e.Date) || parsed.Outcome != e.Outcome || parsed.Reason != e.Reason {
			t.Errorf("round-trip mismatch: %+v → %q → %+v", e, line, parsed)
		}
	}
}

func TestOutcomesSection_RenderPreservesAppendOrder(t *testing.T) {
	// With the append-only representation, render emits exactly the
	// lines it was given, in the order they were recorded. No sorting.
	entries := []outcomeEntry{
		{Date: mustDate(t, "2026-04-03"), Outcome: OutcomeOverridden, Reason: "first recorded"},
		{Date: mustDate(t, "2026-04-10"), Outcome: OutcomeAccepted, Reason: "second"},
		{Date: mustDate(t, "2026-04-14"), Outcome: OutcomeAccepted, Reason: "third"},
	}
	var sec outcomesSection
	for _, e := range entries {
		sec.append(e)
	}
	body := sec.render()
	idx1 := strings.Index(body, "first recorded")
	idx2 := strings.Index(body, "second")
	idx3 := strings.Index(body, "third")
	if idx1 < 0 || idx2 < 0 || idx3 < 0 || idx1 >= idx2 || idx2 >= idx3 {
		t.Errorf("expected append-order in body:\n%s", body)
	}
}

func TestTrack_OldEntriesPersistOutsideWindow(t *testing.T) {
	// Seed a persona-domain file with both in-window and aged-out
	// outcomes. Track must preserve all entries in the file but report
	// only in-window ones in the returned rate.
	dir := t.TempDir()
	s := markdown.New(dir)
	ctx := context.Background()

	fixedNow := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	e, err := NewEngine(ctx, s, WithLockDir(dir), WithClock(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	old := fixedNow.AddDate(0, 0, -200)
	recent := fixedNow.AddDate(0, 0, -5)
	var sec outcomesSection
	sec.append(outcomeEntry{Date: old, Outcome: OutcomeAccepted, Reason: "ancient"})
	sec.append(outcomeEntry{Date: recent, Outcome: OutcomeOverridden, Reason: "recent miss"})
	seed := memory.Memory{
		Path:    effectivenessPath("kent-beck", "testing"),
		Layer:   memory.LayerEffectiveness,
		Domain:  "testing",
		Persona: "kent-beck",
		Created: old,
		Body:    sec.render(),
	}
	if _, err := s.Write(ctx, seed); err != nil {
		t.Fatalf("seed Write: %v", err)
	}

	stats, err := e.Track(ctx, "kent-beck", "testing", OutcomeAccepted, "today")
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	if stats.Total != 2 || stats.Accepted != 1 || stats.Overridden != 1 {
		t.Errorf("stats = %+v, want total=2 accepted=1 overridden=1", stats)
	}
	if stats.AcceptanceRate != 0.5 {
		t.Errorf("AcceptanceRate = %v, want 0.5", stats.AcceptanceRate)
	}

	got, err := s.Read(ctx, effectivenessPath("kent-beck", "testing"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, want := range []string{"ancient", "recent miss", "today"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body missing %q — history was dropped:\n%s", want, got.Body)
		}
	}
}

func TestTrack_PreservesHumanEditedNotes(t *testing.T) {
	// The load→append→write cycle must not drop unparseable lines a
	// human added to the file by hand. Luc's blocker: silent rewrite
	// data loss.
	dir := t.TempDir()
	s := markdown.New(dir)
	ctx := context.Background()
	e, err := NewEngine(ctx, s, WithLockDir(dir), WithClock(func() time.Time {
		return time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	}))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Seed with a prior entry AND a human note that doesn't parse.
	seedBody := "## Outcomes\n" +
		"- 2026-04-10: accepted — PR #50\n" +
		"- TODO: double-check the STI edge case, not sure this rate is real\n"
	seed := memory.Memory{
		Path:    effectivenessPath("kent-beck", "testing"),
		Layer:   memory.LayerEffectiveness,
		Domain:  "testing",
		Persona: "kent-beck",
		Created: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
		Body:    seedBody,
	}
	if _, err := s.Write(ctx, seed); err != nil {
		t.Fatalf("seed Write: %v", err)
	}

	if _, err := e.Track(ctx, "kent-beck", "testing", OutcomeAccepted, ""); err != nil {
		t.Fatalf("Track: %v", err)
	}

	got, err := s.Read(ctx, effectivenessPath("kent-beck", "testing"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(got.Body, "TODO: double-check") {
		t.Errorf("human note was lost on rewrite:\n%s", got.Body)
	}
}

func TestTrack_ConcurrentWritesNoLostEntries(t *testing.T) {
	// The advisory lock must serialise concurrent Track goroutines so
	// no recorded outcome is lost. This is blocker #1 — previously two
	// readers would both read N, both append, and the second writer
	// would silently overwrite the first.
	dir := t.TempDir()
	s := markdown.New(dir)
	ctx := context.Background()
	e, err := NewEngine(ctx, s, WithLockDir(dir))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := e.Track(ctx, "kent-beck", "testing", OutcomeAccepted, ""); err != nil {
				t.Errorf("Track: %v", err)
			}
		}()
	}
	wg.Wait()

	stats, err := e.EffectivenessStatsFor(ctx, "kent-beck", "testing")
	if err != nil {
		t.Fatalf("EffectivenessStatsFor: %v", err)
	}
	if stats.Total != n {
		t.Errorf("concurrent writes: got total=%d, want %d (lock lost writes)", stats.Total, n)
	}
}

func TestTrack_UsesAppendFastPath(t *testing.T) {
	// The second Track for an existing persona+domain should reach the
	// store via AppendBody, not a full Write. countingStore spies on
	// which path was taken.
	ctx := context.Background()
	real := markdown.New(t.TempDir())
	cs := &countingStore{inner: real}
	e, err := NewEngine(ctx, cs,
		WithLockDir(t.TempDir()),
		WithClock(func() time.Time { return time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC) }),
	)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// First Track creates the file — must use Write.
	if _, err := e.Track(ctx, "kent-beck", "testing", OutcomeAccepted, ""); err != nil {
		t.Fatalf("Track #1: %v", err)
	}
	if cs.writes != 1 || cs.appends != 0 {
		t.Fatalf("after first Track: writes=%d appends=%d, want 1/0", cs.writes, cs.appends)
	}

	// Second Track hits the append path — no new Write.
	if _, err := e.Track(ctx, "kent-beck", "testing", OutcomeOverridden, "PR #7"); err != nil {
		t.Fatalf("Track #2: %v", err)
	}
	if cs.writes != 1 || cs.appends != 1 {
		t.Fatalf("after second Track: writes=%d appends=%d, want 1/1 (append fast path)", cs.writes, cs.appends)
	}

	// Body on disk must still contain both outcomes.
	m, err := real.Read(ctx, effectivenessPath("kent-beck", "testing"))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	for _, want := range []string{"accepted", "overridden", "PR #7"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body missing %q:\n%s", want, m.Body)
		}
	}
}

// countingStore wraps a store.Store and records which writes took the
// Write path vs the AppendBody fast path, so tests can prove the fast
// path actually fires.
type countingStore struct {
	inner   store.Store
	writes  int
	appends int
}

func (c *countingStore) Write(ctx context.Context, m memory.Memory) (string, error) {
	c.writes++
	return c.inner.Write(ctx, m)
}
func (c *countingStore) Read(ctx context.Context, path string) (memory.Memory, error) {
	return c.inner.Read(ctx, path)
}
func (c *countingStore) List(ctx context.Context, f store.Filter) ([]memory.Memory, error) {
	return c.inner.List(ctx, f)
}
func (c *countingStore) Delete(ctx context.Context, path string) error {
	return c.inner.Delete(ctx, path)
}
func (c *countingStore) AppendBody(ctx context.Context, path, content string) error {
	c.appends++
	return c.inner.(interface {
		AppendBody(context.Context, string, string) error
	}).AppendBody(ctx, path, content)
}

func TestEffectivenessPath(t *testing.T) {
	got := effectivenessPath("kent-beck", "testing")
	want := "effectiveness/kent-beck-testing.md"
	if got != want {
		t.Errorf("effectivenessPath = %q, want %q", got, want)
	}
}

func TestComputeStats_Basic(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	entries := []outcomeEntry{
		{Date: mustDate(t, "2026-04-14"), Outcome: OutcomeAccepted},
		{Date: mustDate(t, "2026-04-10"), Outcome: OutcomeAccepted},
		{Date: mustDate(t, "2026-04-03"), Outcome: OutcomeOverridden},
	}
	stats := computeStats(entries, now, effectivenessWindowDays)
	if stats.Accepted != 2 || stats.Overridden != 1 || stats.Total != 3 {
		t.Errorf("stats = %+v, want accepted=2 overridden=1 total=3", stats)
	}
	wantRate := 2.0 / 3.0
	if stats.AcceptanceRate < wantRate-1e-9 || stats.AcceptanceRate > wantRate+1e-9 {
		t.Errorf("AcceptanceRate = %v, want ≈ %v", stats.AcceptanceRate, wantRate)
	}
	if stats.WindowDays != effectivenessWindowDays {
		t.Errorf("WindowDays = %d, want %d", stats.WindowDays, effectivenessWindowDays)
	}
}

func TestComputeStats_EmptyAndEdgeCases(t *testing.T) {
	now := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	empty := computeStats(nil, now, effectivenessWindowDays)
	if empty.Total != 0 || empty.AcceptanceRate != 0 {
		t.Errorf("empty: got %+v, want zero stats", empty)
	}

	aged := []outcomeEntry{
		{Date: now.AddDate(0, 0, -200), Outcome: OutcomeAccepted},
	}
	agedStats := computeStats(aged, now, effectivenessWindowDays)
	if agedStats.Total != 0 || agedStats.AcceptanceRate != 0 {
		t.Errorf("aged: got %+v, want zero stats (all outside window)", agedStats)
	}

	onlyOver := []outcomeEntry{
		{Date: now.AddDate(0, 0, -1), Outcome: OutcomeOverridden},
		{Date: now.AddDate(0, 0, -2), Outcome: OutcomeOverridden},
	}
	overStats := computeStats(onlyOver, now, effectivenessWindowDays)
	if overStats.AcceptanceRate != 0 || overStats.Overridden != 2 {
		t.Errorf("only overrides: got %+v, want rate=0 overridden=2", overStats)
	}

	onlyAcc := []outcomeEntry{
		{Date: now.AddDate(0, 0, -1), Outcome: OutcomeAccepted},
		{Date: now.AddDate(0, 0, -2), Outcome: OutcomeAccepted},
	}
	accStats := computeStats(onlyAcc, now, effectivenessWindowDays)
	if accStats.AcceptanceRate != 1.0 || accStats.Accepted != 2 {
		t.Errorf("only accepts: got %+v, want rate=1 accepted=2", accStats)
	}
}

func TestComputeStats_WindowBoundaryInclusive(t *testing.T) {
	now := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)
	boundary := now.AddDate(0, 0, -effectivenessWindowDays)
	justOutside := now.AddDate(0, 0, -effectivenessWindowDays-1)

	entries := []outcomeEntry{
		{Date: boundary, Outcome: OutcomeAccepted},
		{Date: justOutside, Outcome: OutcomeAccepted},
	}
	stats := computeStats(entries, now, effectivenessWindowDays)
	if stats.Total != 1 {
		t.Errorf("Total = %d, want 1 (boundary inclusive, one day older excluded)", stats.Total)
	}
	if stats.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1", stats.Accepted)
	}
}

func TestValidateTrackArgs_ReturnsErrInvalidArgs(t *testing.T) {
	err := validateTrackArgs("kent-beck", "testing", Outcome("unknown"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInvalidArgs) {
		t.Errorf("err = %v, want ErrInvalidArgs wrapped", err)
	}
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return d
}
