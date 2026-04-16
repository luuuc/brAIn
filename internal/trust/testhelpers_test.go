package trust

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/brain/internal/markdown"
	"github.com/luuuc/brain/internal/store"
)

// newTestEngineWithOpts is the canonical test constructor. It builds an
// engine on a scratch .brain/ tree, applies a standard set of test-only
// defaults (short lock timeout, fsync off, pinned clock), then applies
// any extra options the caller passes. Tests that need custom options
// (e.g. WithSeenRefsCap) call this directly; the simpler helpers above
// route through it so eng.syncWrites = false lives in exactly one place.
func newTestEngineWithOpts(t *testing.T, opts ...Option) (*Engine, store.Store, string) {
	t.Helper()
	root := t.TempDir()
	brainDir := filepath.Join(root, ".brain")
	trustDir := filepath.Join(brainDir, "trust")
	md := markdown.New(brainDir)

	baseOpts := []Option{WithLockTimeout(500 * time.Millisecond)}
	baseOpts = append(baseOpts, opts...)

	eng, err := NewEngine(context.Background(), trustDir, md, baseOpts...)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	// Disable fsync — tests care about correctness, not crash durability.
	// syncWrites is an unexported field, so same-package tests can set it
	// directly without exposing a production knob.
	eng.syncWrites = false
	eng.now = func() time.Time { return time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC) }
	return eng, md, trustDir
}

// cumulativeCleanShipsTo returns the number of clean outcomes needed to
// reach target starting from the ask-level default. Shared by tests that
// need to climb a domain to a given level via Record (never via direct
// state seeding).
func cumulativeCleanShipsTo(t *testing.T, target Level) int {
	t.Helper()
	switch target {
	case LevelAsk:
		return 0
	case LevelNotify:
		return PromoteAskToNotify
	case LevelAutoShip:
		return PromoteAskToNotify + PromoteNotifyToAutoShip
	case LevelFullAuto:
		return PromoteAskToNotify + PromoteNotifyToAutoShip + PromoteAutoShipToFullAuto
	}
	t.Fatalf("unknown target level %q", target)
	return 0
}

// climbTo drives the engine through enough Record calls to land domain
// at target level. Used by tests that care about higher-level behavior
// (demotion, override after promotion, etc.) without hand-seeding state.
func climbTo(t *testing.T, eng *Engine, domain string, target Level) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < cumulativeCleanShipsTo(t, target); i++ {
		if _, err := eng.Record(ctx, domain, OutcomeClean, RecordOptions{}); err != nil {
			t.Fatalf("climbTo record %d: %v", i, err)
		}
	}
}
