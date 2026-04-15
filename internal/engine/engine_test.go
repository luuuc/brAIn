package engine_test

import (
	"context"
	"errors"
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
	e, err := engine.NewEngine(ctx, s)
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
