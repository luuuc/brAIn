package trust

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/luuuc/brain/internal/store"
)

// (a) Duplicate ref silently ignored — no counter increment, no history entry.
func TestDedup_duplicateRefIgnored(t *testing.T) {
	eng := newTestEngine(t)
	ctx := context.Background()

	first, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{Ref: "PR #42"})
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	if first.Deduplicated {
		t.Fatal("first record should not be deduplicated")
	}
	if first.Decision.CleanShips != 1 {
		t.Fatalf("clean_ships = %d, want 1", first.Decision.CleanShips)
	}

	second, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{Ref: "PR #42"})
	if err != nil {
		t.Fatalf("second record: %v", err)
	}
	if !second.Deduplicated {
		t.Fatal("second record should be deduplicated")
	}
	if second.Decision.CleanShips != 1 {
		t.Fatalf("counter must not increment on dup, got %d", second.Decision.CleanShips)
	}

	// Only one outcome event in history.
	var outcomes int
	for _, e := range second.Decision.History {
		if e.Kind == EventOutcome {
			outcomes++
		}
	}
	if outcomes != 1 {
		t.Fatalf("expected 1 outcome event, got %d", outcomes)
	}
}

// (b) Same ref with conflicting outcome — second is rejected.
func TestDedup_conflictingOutcomeRejected(t *testing.T) {
	eng := newTestEngine(t)
	ctx := context.Background()
	if _, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{Ref: "PR #9"}); err != nil {
		t.Fatalf("first record: %v", err)
	}
	_, err := eng.Record(ctx, "code", OutcomeFailure, RecordOptions{Ref: "PR #9", Reason: "rollback"})
	if err == nil {
		t.Fatal("expected error on conflicting ref outcome")
	}
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}

	// State should not have been demoted.
	dec, _ := eng.Check(ctx, "code", CheckOptions{})
	if dec.Level != LevelAsk || dec.CleanShips != 1 {
		t.Fatalf("state changed on rejected dup: level=%q clean=%d", dec.Level, dec.CleanShips)
	}
}

// (c) Same ref in different domains counts separately.
func TestDedup_sameRefDifferentDomains(t *testing.T) {
	eng := newTestEngine(t)
	ctx := context.Background()
	if _, err := eng.Record(ctx, "database", OutcomeClean, RecordOptions{Ref: "PR #10"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := eng.Record(ctx, "frontend", OutcomeClean, RecordOptions{Ref: "PR #10"}); err != nil {
		t.Fatalf("second: %v", err)
	}

	for _, d := range []string{"database", "frontend"} {
		dec, err := eng.Check(ctx, d, CheckOptions{})
		if err != nil {
			t.Fatalf("check %s: %v", d, err)
		}
		if dec.CleanShips != 1 {
			t.Fatalf("%s clean_ships = %d, want 1", d, dec.CleanShips)
		}
	}
}

// Dedup state evicts oldest refs once the cap is reached, so old refs
// become addressable again. This keeps trust.yml bounded even under a
// long-running CI. Test uses a tight cap (via WithSeenRefsCap) so we
// don't pay fsync cost on hundreds of records.
func TestDedup_seenRefsFIFOEviction(t *testing.T) {
	eng, _, _ := newTestEngineWithOpts(t, WithSeenRefsCap(3))
	ctx := context.Background()

	// Fill the cap (3). No eviction yet.
	for i := 0; i < 3; i++ {
		ref := "PR#" + strconv.Itoa(i)
		r, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{Ref: ref})
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		if r.EvictedRefs != 0 {
			t.Fatalf("record %d: unexpected evicted=%d", i, r.EvictedRefs)
		}
	}
	// The fourth record should evict PR#0 and report it.
	r, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{Ref: "PR#3"})
	if err != nil {
		t.Fatalf("record 3: %v", err)
	}
	if r.EvictedRefs != 1 {
		t.Fatalf("record 3 EvictedRefs = %d, want 1", r.EvictedRefs)
	}

	// Evicted ref is re-recordable, not deduplicated.
	r2, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{Ref: "PR#0"})
	if err != nil {
		t.Fatalf("re-record of evicted ref: %v", err)
	}
	if r2.Deduplicated {
		t.Fatal("expected evicted ref to be re-recordable, got dedup")
	}
	// A ref still inside the window stays deduplicated.
	r3, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{Ref: "PR#3"})
	if err != nil {
		t.Fatalf("re-record of live ref: %v", err)
	}
	if !r3.Deduplicated {
		t.Fatal("expected live ref to be deduplicated")
	}
}

// Sanity: no ref means no dedup — both records count.
func TestDedup_noRefBothCount(t *testing.T) {
	eng := newTestEngine(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	dec, _ := eng.Check(ctx, "code", CheckOptions{})
	if dec.CleanShips != 2 {
		t.Fatalf("clean_ships = %d, want 2", dec.CleanShips)
	}
}
