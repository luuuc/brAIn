package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/luuuc/brain/internal/config"
	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/markdown"
	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
)

var testCreated = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

// setupWithFacts builds an engine with the given fact configuration and
// returns it alongside the store, so tests can read back what was written.
func setupWithFacts(t *testing.T, f config.Facts) (*engine.Engine, store.Store, context.Context) {
	t.Helper()
	dir := t.TempDir()
	s := markdown.New(dir)
	ctx := context.Background()
	e, err := engine.NewEngine(ctx, s, engine.WithLockDir(dir), engine.WithFacts(f))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e, s, ctx
}

// readBack fetches the memory written at path.
func readBack(t *testing.T, s store.Store, ctx context.Context, path string) memory.Memory {
	t.Helper()
	m, err := s.Read(ctx, path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return m
}

// The feature the pitch exists for: a fact written without an explicit
// expiry gets one, so facts actually go stale.
func TestRemember_stampsStaleAfterOnFacts(t *testing.T) {
	e, s, ctx := setupWithFacts(t, config.Default().Facts)

	res, err := e.Remember(ctx, memory.Memory{
		Layer: memory.LayerFact, Domain: "database",
		Body: "Users table has 12M rows", Created: testCreated,
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}

	got := readBack(t, s, ctx, res.Path)
	if got.StaleAfter == nil {
		t.Fatal("StaleAfter is nil — the fact will never go stale")
	}
	want := testCreated.AddDate(0, 0, 30)
	if !got.StaleAfter.Equal(want) {
		t.Errorf("StaleAfter = %v, want %v", got.StaleAfter, want)
	}
}

func TestRemember_staleAfterUsesConfiguredWindow(t *testing.T) {
	e, s, ctx := setupWithFacts(t, config.Facts{StaleAfterDays: 7})

	res, err := e.Remember(ctx, memory.Memory{
		Layer: memory.LayerFact, Domain: "database",
		Body: "Users table has 12M rows", Created: testCreated,
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}

	got := readBack(t, s, ctx, res.Path)
	if want := testCreated.AddDate(0, 0, 7); got.StaleAfter == nil || !got.StaleAfter.Equal(want) {
		t.Errorf("StaleAfter = %v, want %v", got.StaleAfter, want)
	}
}

// An explicit expiry from the caller is never overwritten, including one
// already in the past.
func TestRemember_explicitStaleAfterWins(t *testing.T) {
	e, s, ctx := setupWithFacts(t, config.Default().Facts)

	explicit := testCreated.AddDate(0, 0, -1)
	res, err := e.Remember(ctx, memory.Memory{
		Layer: memory.LayerFact, Domain: "database",
		Body: "already expired", Created: testCreated, StaleAfter: &explicit,
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}

	got := readBack(t, s, ctx, res.Path)
	if got.StaleAfter == nil || !got.StaleAfter.Equal(explicit) {
		t.Errorf("StaleAfter = %v, want the caller's %v", got.StaleAfter, explicit)
	}
}

// Only facts expire. Stamping a decision or a correction would quietly put a
// shelf life on the layers that are meant to be permanent.
func TestRemember_onlyFactsAreStamped(t *testing.T) {
	layers := []memory.Layer{
		memory.LayerLesson, memory.LayerDecision,
		memory.LayerEffectiveness, memory.LayerCorrection,
	}
	for _, layer := range layers {
		t.Run(string(layer), func(t *testing.T) {
			e, s, ctx := setupWithFacts(t, config.Default().Facts)
			res, err := e.Remember(ctx, memory.Memory{
				Layer: layer, Domain: "database",
				Body: "body text", Created: testCreated, Persona: "kent-beck",
			})
			if err != nil {
				t.Fatalf("Remember: %v", err)
			}
			if got := readBack(t, s, ctx, res.Path); got.StaleAfter != nil {
				t.Errorf("%s got StaleAfter = %v, want nil", layer, got.StaleAfter)
			}
		})
	}
}

// A fact that reached the fact layer by classification rather than by an
// explicit Layer must be stamped too.
func TestRemember_autoClassifiedFactIsStamped(t *testing.T) {
	e, s, ctx := setupWithFacts(t, config.Default().Facts)

	res, err := e.Remember(ctx, memory.Memory{
		Domain: "database", Body: "Users table has 12M rows", Created: testCreated,
	})
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	// Asserted, not skipped: if the classifier stops seeing this as a fact,
	// that is a failure to look at, not a reason to disable the test.
	if res.Layer != memory.LayerFact {
		t.Fatalf("classifier chose %q, want %q", res.Layer, memory.LayerFact)
	}
	if got := readBack(t, s, ctx, res.Path); got.StaleAfter == nil {
		t.Error("auto-classified fact was not stamped")
	}
}

// Stamping is worthless if ranking ignores it: a stale fact must sort below a
// fresh one in the same layer.
func TestRecall_staleFactRanksBelowFresh(t *testing.T) {
	dir := t.TempDir()
	s := markdown.New(dir)
	ctx := context.Background()
	now := testCreated.AddDate(0, 0, 40)

	e, err := engine.NewEngine(ctx, s,
		engine.WithLockDir(dir),
		engine.WithFacts(config.Facts{StaleAfterDays: 30}),
		engine.WithClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Written 40 days ago with a 30-day window: stale as of now.
	if _, err := e.Remember(ctx, memory.Memory{
		Layer: memory.LayerFact, Domain: "database",
		Body: "stale fact", Created: testCreated,
	}); err != nil {
		t.Fatalf("Remember stale: %v", err)
	}
	// Written today: fresh.
	if _, err := e.Remember(ctx, memory.Memory{
		Layer: memory.LayerFact, Domain: "database",
		Body: "fresh fact", Created: now,
	}); err != nil {
		t.Fatalf("Remember fresh: %v", err)
	}

	layer := memory.LayerFact
	got, err := e.Recall(ctx, engine.RecallOptions{Domain: "database", Layer: &layer})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d memories, want 2", len(got))
	}
	if got[0].StaleAfter == nil || !got[0].StaleAfter.After(now) {
		t.Errorf("first result is not the fresh fact: stale_after = %v, now = %v",
			got[0].StaleAfter, now)
	}
}

// A non-positive window would expire every fact on write, so the option
// refuses it and keeps the default.
func TestWithFacts_rejectsNonPositive(t *testing.T) {
	for _, days := range []int{0, -1} {
		e, s, ctx := setupWithFacts(t, config.Facts{StaleAfterDays: days})
		res, err := e.Remember(ctx, memory.Memory{
			Layer: memory.LayerFact, Domain: "database",
			Body: "fact", Created: testCreated,
		})
		if err != nil {
			t.Fatalf("Remember: %v", err)
		}
		got := readBack(t, s, ctx, res.Path)
		if want := testCreated.AddDate(0, 0, 30); got.StaleAfter == nil || !got.StaleAfter.Equal(want) {
			t.Errorf("stale_after_days=%d: StaleAfter = %v, want the default %v",
				days, got.StaleAfter, want)
		}
	}
}
