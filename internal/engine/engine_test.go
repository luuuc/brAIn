package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/markdown"
	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
)

func setup(t *testing.T) (*engine.Engine, context.Context) {
	t.Helper()
	dir := t.TempDir()
	s := markdown.New(dir)
	ctx := context.Background()
	e, err := engine.NewEngine(ctx, s, engine.WithLockDir(dir))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e, ctx
}

// setupAt is like setup but pins the engine clock to a fixed time.
func setupAt(t *testing.T, now time.Time) (*engine.Engine, context.Context) {
	t.Helper()
	dir := t.TempDir()
	s := markdown.New(dir)
	ctx := context.Background()
	e, err := engine.NewEngine(ctx, s,
		engine.WithLockDir(dir),
		engine.WithClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e, ctx
}

func mustRemember(t *testing.T, e *engine.Engine, ctx context.Context, m memory.Memory) string {
	t.Helper()
	res, err := e.Remember(ctx, m)
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	return res.Path
}

func TestNewEngine_NilStore(t *testing.T) {
	_, err := engine.NewEngine(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestRememberRecall_RoundTrip(t *testing.T) {
	e, ctx := setup(t)

	now := time.Now()
	m := memory.Memory{
		Layer:   memory.LayerFact,
		Domain:  "database",
		Created: now,
		Body:    "# Users table\n\nThe users table has 12 columns.",
	}

	res, err := e.Remember(ctx, m)
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if res.Path == "" {
		t.Fatal("Remember returned empty path")
	}
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", res.Warnings)
	}

	results, err := e.Recall(ctx, engine.RecallOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Recall got %d results, want 1", len(results))
	}
	if results[0].Domain != "database" {
		t.Errorf("Domain = %q, want %q", results[0].Domain, "database")
	}
}

func TestRemember_Validation(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	// Missing domain.
	_, err := e.Remember(ctx, memory.Memory{Layer: memory.LayerFact, Created: now, Body: "# No domain"})
	if err == nil {
		t.Fatal("expected error for empty domain")
	}

	// Zero created time.
	_, err = e.Remember(ctx, memory.Memory{Layer: memory.LayerFact, Domain: "db", Body: "# No created"})
	if err == nil {
		t.Fatal("expected error for zero created time")
	}
}

func TestRecall_DomainFiltering(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "database", Created: now, Body: "# DB fact"})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "api", Created: now, Body: "# API fact"})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "database", Created: now, Body: "# Another DB fact"})

	results, err := e.Recall(ctx, engine.RecallOptions{Domain: "database", Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for _, r := range results {
		if r.Domain != "database" {
			t.Errorf("unexpected domain %q", r.Domain)
		}
	}
}

func TestRecall_QueryMatching(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "db", Created: now, Body: "# Users table schema"})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "db", Created: now, Body: "# Orders table schema"})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "api", Created: now, Body: "# Endpoint list", Tags: []string{"users", "rest"}})

	// Query matches title.
	results, err := e.Recall(ctx, engine.RecallOptions{Query: "users", Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (title match + tag match)", len(results))
	}

	// Query matches tag.
	results, err = e.Recall(ctx, engine.RecallOptions{Query: "rest", Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}

	// No matches.
	results, err = e.Recall(ctx, engine.RecallOptions{Query: "nonexistent", Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("got %d results, want 0", len(results))
	}
}

func TestRecall_LayerFiltering(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "db", Created: now, Body: "# A fact"})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerDecision, Domain: "db", Created: now, Body: "# A decision"})

	layer := memory.LayerFact
	results, err := e.Recall(ctx, engine.RecallOptions{Layer: &layer, Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Layer != memory.LayerFact {
		t.Errorf("Layer = %q, want %q", results[0].Layer, memory.LayerFact)
	}
}

func TestRecall_ZeroLimitReturnsAll(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	for i := 0; i < 10; i++ {
		mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "db", Created: now.Add(time.Duration(i) * time.Second), Body: "# Fact"})
	}

	results, err := e.Recall(ctx, engine.RecallOptions{})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 10 {
		t.Fatalf("zero limit: got %d results, want 10 (all)", len(results))
	}
}

func TestRecall_ExplicitLimit(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	for i := 0; i < 10; i++ {
		mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "db", Created: now.Add(time.Duration(i) * time.Second), Body: "# Fact"})
	}

	results, err := e.Recall(ctx, engine.RecallOptions{Limit: 5})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("explicit limit 5: got %d results, want 5", len(results))
	}
}

func TestRecall_AuthorityOrdering(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "db", Created: now, Body: "# A fact"})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerCorrection, Domain: "db", Created: now, Body: "# Stop doing this"})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerDecision, Domain: "db", Created: now, Body: "# We decided on X"})

	results, err := e.Recall(ctx, engine.RecallOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if results[0].Layer != memory.LayerCorrection {
		t.Errorf("results[0].Layer = %q, want correction", results[0].Layer)
	}
	if results[1].Layer != memory.LayerDecision {
		t.Errorf("results[1].Layer = %q, want decision", results[1].Layer)
	}
	if results[2].Layer != memory.LayerFact {
		t.Errorf("results[2].Layer = %q, want fact", results[2].Layer)
	}
}

func TestSupersession(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	// Write original memory.
	origPath := mustRemember(t, e, ctx, memory.Memory{
		Layer:   memory.LayerFact,
		Domain:  "db",
		Created: now,
		Body:    "# Original fact",
	})

	// Write superseding memory.
	res, err := e.Remember(ctx, memory.Memory{
		Layer:      memory.LayerFact,
		Domain:     "db",
		Created:    now.Add(time.Second),
		Body:       "# Updated fact",
		Supersedes: origPath,
	})
	if err != nil {
		t.Fatalf("Remember superseding: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", res.Warnings)
	}

	// Recall should only return the new one (original is retired).
	results, err := e.Recall(ctx, engine.RecallOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Body == "" {
		t.Error("expected non-empty body")
	}
}

func TestSupersession_MissingTarget(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	// Supersede a non-existent file — should succeed with a warning.
	res, err := e.Remember(ctx, memory.Memory{
		Layer:      memory.LayerFact,
		Domain:     "db",
		Created:    now,
		Body:       "# New fact",
		Supersedes: "facts/nonexistent.md",
	})
	if err != nil {
		t.Fatalf("Remember should succeed even when superseded target is missing: %v", err)
	}
	if res.Path == "" {
		t.Fatal("expected non-empty path")
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(res.Warnings))
	}
}

func TestSupersession_SelfReference(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	// Write a memory.
	path := mustRemember(t, e, ctx, memory.Memory{
		Layer:   memory.LayerFact,
		Domain:  "db",
		Created: now,
		Body:    "# Self ref fact",
	})

	// Supersede itself — should retire the original.
	mustRemember(t, e, ctx, memory.Memory{
		Layer:      memory.LayerFact,
		Domain:     "db",
		Created:    now.Add(time.Second),
		Body:       "# Replacement",
		Supersedes: path,
	})

	results, err := e.Recall(ctx, engine.RecallOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

func TestSupersession_Duplicate(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	// Write original.
	origPath := mustRemember(t, e, ctx, memory.Memory{
		Layer:   memory.LayerFact,
		Domain:  "db",
		Created: now,
		Body:    "# Original",
	})

	// Two memories supersede the same target — both should succeed.
	mustRemember(t, e, ctx, memory.Memory{
		Layer:      memory.LayerFact,
		Domain:     "db",
		Created:    now.Add(time.Second),
		Body:       "# Replacement A",
		Supersedes: origPath,
	})
	// Second supersession retires an already-retired memory — idempotent.
	res, err := e.Remember(ctx, memory.Memory{
		Layer:      memory.LayerFact,
		Domain:     "db",
		Created:    now.Add(2 * time.Second),
		Body:       "# Replacement B",
		Supersedes: origPath,
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("duplicate supersession should not warn: %v", res.Warnings)
	}

	// Original retired, two replacements active.
	results, err := e.Recall(ctx, engine.RecallOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func TestForget(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	path := mustRemember(t, e, ctx, memory.Memory{
		Layer:   memory.LayerLesson,
		Domain:  "api",
		Created: now,
		Body:    "# Lesson to forget",
	})

	// Forget it.
	if err := e.Forget(ctx, path, "no longer relevant"); err != nil {
		t.Fatalf("Forget: %v", err)
	}

	// Recall should return nothing (retired).
	results, err := e.Recall(ctx, engine.RecallOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("got %d results, want 0 after forget", len(results))
	}

	// With IncludeRetired, should still be there.
	results, err = e.Recall(ctx, engine.RecallOptions{Limit: 10, IncludeRetired: true})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 with IncludeRetired", len(results))
	}
	if results[0].RetiredReason != "no longer relevant" {
		t.Errorf("RetiredReason = %q, want %q", results[0].RetiredReason, "no longer relevant")
	}
}

func TestForget_NonexistentPath(t *testing.T) {
	e, ctx := setup(t)

	err := e.Forget(ctx, "lessons/nonexistent.md", "")
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestRemember_ClassifiesLayer(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	// No explicit layer — should be classified from content.
	res, err := e.Remember(ctx, memory.Memory{
		Domain:  "db",
		Created: now,
		Body:    "We decided to use PostgreSQL",
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if res.Path == "" {
		t.Fatal("expected non-empty path")
	}

	results, err := e.Recall(ctx, engine.RecallOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Layer != memory.LayerDecision {
		t.Errorf("Layer = %q, want %q", results[0].Layer, memory.LayerDecision)
	}
}

func TestRemember_ExplicitLayerOverridesClassification(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()

	// Content says "we decided" but layer is explicitly fact.
	mustRemember(t, e, ctx, memory.Memory{
		Layer:   memory.LayerFact,
		Domain:  "db",
		Created: now,
		Body:    "We decided to use PostgreSQL",
	})

	results, err := e.Recall(ctx, engine.RecallOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if results[0].Layer != memory.LayerFact {
		t.Errorf("explicit layer should win: got %q, want %q", results[0].Layer, memory.LayerFact)
	}
}

func TestTrack_NewFileCreatesEffectivenessMemory(t *testing.T) {
	e, ctx := setup(t)

	stats, err := e.Track(ctx, "kent-beck", "testing", engine.OutcomeAccepted, "PR #52")
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	if stats.Accepted != 1 || stats.Overridden != 0 || stats.Total != 1 {
		t.Errorf("stats = %+v, want accepted=1 overridden=0 total=1", stats)
	}
	if stats.AcceptanceRate != 1.0 {
		t.Errorf("AcceptanceRate = %v, want 1.0", stats.AcceptanceRate)
	}
	if stats.Persona != "kent-beck" || stats.Domain != "testing" {
		t.Errorf("persona/domain on stats = %q/%q", stats.Persona, stats.Domain)
	}

	// The file is listable via the effectiveness layer.
	layer := memory.LayerEffectiveness
	results, err := e.Recall(ctx, engine.RecallOptions{Layer: &layer, Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("recall effectiveness: got %d results, want 1", len(results))
	}
	if results[0].Persona != "kent-beck" {
		t.Errorf("Persona = %q, want kent-beck", results[0].Persona)
	}
	if results[0].Path != "effectiveness/kent-beck-testing.md" {
		t.Errorf("Path = %q, want effectiveness/kent-beck-testing.md", results[0].Path)
	}
}

func TestTrack_AppendsToExistingFile(t *testing.T) {
	e, ctx := setup(t)

	if _, err := e.Track(ctx, "kent-beck", "testing", engine.OutcomeAccepted, "PR #50"); err != nil {
		t.Fatalf("Track #1: %v", err)
	}
	stats, err := e.Track(ctx, "kent-beck", "testing", engine.OutcomeOverridden, "false positive")
	if err != nil {
		t.Fatalf("Track #2: %v", err)
	}
	if stats.Total != 2 || stats.Accepted != 1 || stats.Overridden != 1 {
		t.Errorf("stats = %+v, want total=2 accepted=1 overridden=1", stats)
	}
	if stats.AcceptanceRate != 0.5 {
		t.Errorf("AcceptanceRate = %v, want 0.5", stats.AcceptanceRate)
	}

	// Read back the effectiveness file — both outcomes must be present.
	layer := memory.LayerEffectiveness
	results, err := e.Recall(ctx, engine.RecallOptions{Layer: &layer, Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d effectiveness files, want 1 (same persona+domain reuses file)", len(results))
	}
	body := results[0].Body
	for _, want := range []string{"accepted — PR #50", "overridden — false positive"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestTrack_IsolatesPersonaDomainCombinations(t *testing.T) {
	e, ctx := setup(t)

	_, err := e.Track(ctx, "kent-beck", "testing", engine.OutcomeAccepted, "")
	if err != nil {
		t.Fatalf("Track beck/testing: %v", err)
	}
	_, err = e.Track(ctx, "kent-beck", "api", engine.OutcomeOverridden, "")
	if err != nil {
		t.Fatalf("Track beck/api: %v", err)
	}
	_, err = e.Track(ctx, "rich-hickey", "testing", engine.OutcomeAccepted, "")
	if err != nil {
		t.Fatalf("Track hickey/testing: %v", err)
	}

	layer := memory.LayerEffectiveness
	results, err := e.Recall(ctx, engine.RecallOptions{Layer: &layer, Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d files, want 3 (one per persona-domain combo)", len(results))
	}
}

func TestTrack_ValidationErrors(t *testing.T) {
	e, ctx := setup(t)

	cases := []struct {
		name    string
		persona string
		domain  string
		outcome engine.Outcome
	}{
		{"empty persona", "", "testing", engine.OutcomeAccepted},
		{"empty domain", "kent-beck", "", engine.OutcomeAccepted},
		{"persona with slash", "../etc/passwd", "testing", engine.OutcomeAccepted},
		{"domain with slash", "kent-beck", "../etc", engine.OutcomeAccepted},
		{"persona with space", "kent beck", "testing", engine.OutcomeAccepted},
		{"invalid outcome", "kent-beck", "testing", engine.Outcome("unknown")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.Track(ctx, tc.persona, tc.domain, tc.outcome, "")
			if err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestTrack_TrimsAndLowercasesInputs(t *testing.T) {
	e, ctx := setup(t)

	stats, err := e.Track(ctx, "  Kent-Beck  ", "  Testing  ", engine.OutcomeAccepted, "")
	if err != nil {
		t.Fatalf("Track: %v", err)
	}
	if stats.Persona != "kent-beck" || stats.Domain != "testing" {
		t.Errorf("normalized inputs: persona=%q domain=%q", stats.Persona, stats.Domain)
	}
}

func TestTrack_ReasonFormatting(t *testing.T) {
	e, ctx := setup(t)

	if _, err := e.Track(ctx, "kent-beck", "testing", engine.OutcomeAccepted, "PR #52"); err != nil {
		t.Fatalf("Track with reason: %v", err)
	}
	if _, err := e.Track(ctx, "kent-beck", "testing", engine.OutcomeAccepted, ""); err != nil {
		t.Fatalf("Track without reason: %v", err)
	}
	if _, err := e.Track(ctx, "kent-beck", "testing", engine.OutcomeOverridden, "  whitespace padded  "); err != nil {
		t.Fatalf("Track padded reason: %v", err)
	}

	layer := memory.LayerEffectiveness
	results, err := e.Recall(ctx, engine.RecallOptions{Layer: &layer, Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	body := results[0].Body
	// Canonical reason uses em-dash.
	if !strings.Contains(body, ": accepted — PR #52") {
		t.Errorf("expected canonical em-dash separator for reason:\n%s", body)
	}
	// Empty reason → bare outcome (no trailing separator).
	if !strings.Contains(body, ": accepted\n") {
		t.Errorf("expected bare outcome line for empty reason:\n%s", body)
	}
	// Padded reason trimmed.
	if !strings.Contains(body, ": overridden — whitespace padded") {
		t.Errorf("expected trimmed reason:\n%s", body)
	}
	if strings.Contains(body, ": overridden —  whitespace") {
		t.Errorf("reason was not trimmed:\n%s", body)
	}
}

func TestEffectivenessStatsFor_NoFile(t *testing.T) {
	e, ctx := setup(t)

	_, err := e.EffectivenessStatsFor(ctx, "kent-beck", "testing")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRecall_EffectivenessAdjustsWithinLayer(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	e, ctx := setupAt(t, now)

	// Two lessons in the same domain, each associated with a persona.
	// kent-beck will have a 100% acceptance rate; rich-hickey will have 0%.
	// The rich-hickey lesson is newer, so without effectiveness it would
	// rank first by recency.
	mustRemember(t, e, ctx, memory.Memory{
		Layer:   memory.LayerLesson,
		Domain:  "testing",
		Persona: "kent-beck",
		Created: now.Add(-2 * time.Hour),
		Body:    "# beck lesson",
	})
	mustRemember(t, e, ctx, memory.Memory{
		Layer:   memory.LayerLesson,
		Domain:  "testing",
		Persona: "rich-hickey",
		Created: now,
		Body:    "# hickey lesson",
	})

	// Record outcomes so kent-beck scores high and rich-hickey scores low.
	for i := 0; i < 5; i++ {
		if _, err := e.Track(ctx, "kent-beck", "testing", engine.OutcomeAccepted, ""); err != nil {
			t.Fatalf("Track beck: %v", err)
		}
	}
	for i := 0; i < 5; i++ {
		if _, err := e.Track(ctx, "rich-hickey", "testing", engine.OutcomeOverridden, ""); err != nil {
			t.Fatalf("Track hickey: %v", err)
		}
	}

	layer := memory.LayerLesson
	// Without the flag: recency wins → hickey first.
	plain, err := e.Recall(ctx, engine.RecallOptions{Domain: "testing", Layer: &layer, Limit: 10})
	if err != nil {
		t.Fatalf("Recall plain: %v", err)
	}
	if len(plain) != 2 {
		t.Fatalf("plain got %d lessons, want 2", len(plain))
	}
	if plain[0].Persona != "rich-hickey" {
		t.Errorf("plain recall: first lesson = %q, want rich-hickey (newer)", plain[0].Persona)
	}

	// With the flag: beck's higher acceptance rate overrides recency.
	adj, err := e.Recall(ctx, engine.RecallOptions{
		Domain: "testing", Layer: &layer, Limit: 10, UseEffectiveness: true,
	})
	if err != nil {
		t.Fatalf("Recall adjusted: %v", err)
	}
	if adj[0].Persona != "kent-beck" {
		t.Errorf("adjusted recall: first lesson = %q, want kent-beck (higher rate)", adj[0].Persona)
	}
}

func TestRecall_EffectivenessNoOpWithoutData(t *testing.T) {
	e, ctx := setup(t)

	// Memory exists but no Track calls → no effectiveness data for the
	// domain. The flag must be a no-op, not an error.
	mustRemember(t, e, ctx, memory.Memory{
		Layer:   memory.LayerLesson,
		Domain:  "frontend",
		Persona: "kent-beck",
		Created: time.Now(),
		Body:    "# lonely lesson",
	})

	results, err := e.Recall(ctx, engine.RecallOptions{
		Domain: "frontend", UseEffectiveness: true, Limit: 10,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

func TestEffectivenessStatsFor_ReturnsCurrentRate(t *testing.T) {
	e, ctx := setup(t)

	_, _ = e.Track(ctx, "kent-beck", "testing", engine.OutcomeAccepted, "")
	_, _ = e.Track(ctx, "kent-beck", "testing", engine.OutcomeAccepted, "")
	_, _ = e.Track(ctx, "kent-beck", "testing", engine.OutcomeOverridden, "")

	stats, err := e.EffectivenessStatsFor(ctx, "kent-beck", "testing")
	if err != nil {
		t.Fatalf("EffectivenessStatsFor: %v", err)
	}
	if stats.Total != 3 || stats.Accepted != 2 || stats.Overridden != 1 {
		t.Errorf("stats = %+v, want total=3 accepted=2 overridden=1", stats)
	}
}

func TestRecall_FullPipeline(t *testing.T) {
	e, ctx := setup(t)
	now := time.Now()
	stale := now.AddDate(0, 0, -40)
	staleAfter := now.AddDate(0, 0, -10)

	// Create a mix of memories.
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerCorrection, Domain: "api", Created: now, Body: "# Never use XML"})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerDecision, Domain: "api", Created: now, Body: "# We decided on REST"})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerLesson, Domain: "api", Created: now, Body: "# Learned rate limiting matters"})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerLesson, Domain: "api", Created: now, Body: "# Old lesson", Retired: true})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "api", Created: now, Body: "# Fresh fact"})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "api", Created: stale, StaleAfter: &staleAfter, Body: "# Stale fact"})
	mustRemember(t, e, ctx, memory.Memory{Layer: memory.LayerFact, Domain: "db", Created: now, Body: "# DB fact — different domain"})

	// Recall for api domain, limit 10.
	results, err := e.Recall(ctx, engine.RecallOptions{Domain: "api", Limit: 10})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	// Should have 5: correction, decision, lesson (active), fresh fact, stale fact.
	// Retired lesson excluded. DB fact excluded by domain.
	if len(results) != 5 {
		t.Fatalf("got %d results, want 5", len(results))
	}

	// Verify ordering: correction > decision > lesson > fresh fact > stale fact.
	expectedLayers := []memory.Layer{
		memory.LayerCorrection,
		memory.LayerDecision,
		memory.LayerLesson,
		memory.LayerFact, // fresh
		memory.LayerFact, // stale
	}
	for i, want := range expectedLayers {
		if results[i].Layer != want {
			t.Errorf("results[%d].Layer = %q, want %q", i, results[i].Layer, want)
		}
	}
}
