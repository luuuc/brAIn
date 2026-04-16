package trust

import (
	"context"
	"errors"
	"testing"

	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
)

// newTestEngine returns just the engine for tests that don't need the
// backing store or trust dir.
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	eng, _, _ := newTestEngineWithStore(t)
	return eng
}

// newTestEngineWithStore returns the engine, its backing store, and the
// trust dir. Tests that also need to seed lessons or inspect corrections
// reach for this variant.
func newTestEngineWithStore(t *testing.T) (*Engine, store.Store, string) {
	t.Helper()
	return newTestEngineWithOpts(t)
}

func TestCheck_unknownDomainReturnsAsk(t *testing.T) {
	eng := newTestEngine(t)
	got, err := eng.Check(context.Background(), "code", CheckOptions{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got.Level != LevelAsk {
		t.Fatalf("level = %q, want ask", got.Level)
	}
	if got.Recommendation != RecommendationEscalate {
		t.Fatalf("recommendation = %q, want escalate", got.Recommendation)
	}
}

func TestCheck_emptyDomainFails(t *testing.T) {
	eng := newTestEngine(t)
	if _, err := eng.Check(context.Background(), "", CheckOptions{}); err == nil {
		t.Fatal("expected error on empty domain")
	}
}

func TestCheck_respectsHotfix(t *testing.T) {
	eng := newTestEngine(t)
	dec, err := eng.Check(context.Background(), "code", CheckOptions{Hotfix: true})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if dec.Level != LevelAsk {
		t.Fatalf("level should remain ask, got %q", dec.Level)
	}
	if dec.Recommendation != RecommendationShipNotify {
		t.Fatalf("hotfix did not raise floor: got %q", dec.Recommendation)
	}
	if !dec.Hotfix {
		t.Fatal("Hotfix flag should be set on Decision")
	}
}

func TestCheck_afterPromotion(t *testing.T) {
	eng := newTestEngine(t)
	ctx := context.Background()
	for i := 0; i < PromoteAskToNotify; i++ {
		if _, err := eng.Record(ctx, "frontend", OutcomeClean, RecordOptions{}); err != nil {
			t.Fatalf("Record #%d: %v", i, err)
		}
	}
	dec, err := eng.Check(ctx, "frontend", CheckOptions{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if dec.Level != LevelNotify {
		t.Fatalf("level = %q, want notify", dec.Level)
	}
	if dec.Recommendation != RecommendationShipNotify {
		t.Fatalf("recommendation = %q, want ship_notify", dec.Recommendation)
	}
	if dec.CleanShips != 0 {
		t.Fatalf("clean_ships should reset on promotion, got %d", dec.CleanShips)
	}
}

func TestList_returnsAllDomainsSorted(t *testing.T) {
	eng := newTestEngine(t)
	ctx := context.Background()
	for _, dom := range []string{"frontend", "database", "testing"} {
		if _, err := eng.Record(ctx, dom, OutcomeClean, RecordOptions{}); err != nil {
			t.Fatalf("Record %s: %v", dom, err)
		}
	}
	list, err := eng.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("list size = %d, want 3", len(list))
	}
	want := []string{"database", "frontend", "testing"}
	for i, d := range list {
		if d.Domain != want[i] {
			t.Fatalf("position %d = %q, want %q", i, d.Domain, want[i])
		}
	}
}

func TestOverride_writesCorrectionMemory(t *testing.T) {
	eng, md, _ := newTestEngineWithStore(t)
	ctx := context.Background()
	dec, err := eng.Override(ctx, "database", "migration behaved unexpectedly")
	if err != nil {
		t.Fatalf("Override: %v", err)
	}
	if dec.Domain != "database" {
		t.Fatalf("domain = %q, want database", dec.Domain)
	}
	if len(dec.History) != 1 || dec.History[0].Kind != EventOverride {
		t.Fatalf("expected override event in history, got %+v", dec.History)
	}

	layer := memory.LayerCorrection
	domain := "database"
	mems, err := md.List(ctx, store.Filter{Layer: &layer, Domain: &domain})
	if err != nil {
		t.Fatalf("List corrections: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("expected 1 correction, got %d", len(mems))
	}
	if !mems[0].Immutable {
		t.Fatal("correction should be immutable")
	}
}

func TestOverride_emptyReasonFails(t *testing.T) {
	eng := newTestEngine(t)
	if _, err := eng.Override(context.Background(), "database", ""); err == nil {
		t.Fatal("expected error on empty reason")
	}
}

func TestRecord_contextCancellationErrors(t *testing.T) {
	eng := newTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
