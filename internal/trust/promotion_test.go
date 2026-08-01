package trust

import (
	"context"
	"testing"
	"time"

	"github.com/luuuc/brain/internal/config"
)

// TestPromotion_honorsConfiguredThresholds is the point of making thresholds
// configurable: a shorter ladder must actually promote sooner.
func TestPromotion_honorsConfiguredThresholds(t *testing.T) {
	custom := config.Trust{PromoteToNotify: 2, PromoteToAutoShip: 3, PromoteToFullAuto: 4}
	eng, _, _ := newTestEngineWithOpts(t, WithThresholds(custom))
	ctx := context.Background()

	// want[i] is the level expected after i clean outcomes. The counter resets
	// at each promotion, so the rungs cost 2, then 3, then 4 — cumulatively
	// promoting at outcome 2, 5 and 9.
	want := []Level{
		LevelAsk, LevelAsk,
		LevelNotify, LevelNotify, LevelNotify,
		LevelAutoShip, LevelAutoShip, LevelAutoShip, LevelAutoShip,
		LevelFullAuto,
	}
	for i, wantLevel := range want {
		if i > 0 {
			if _, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{}); err != nil {
				t.Fatalf("record %d: %v", i, err)
			}
		}
		d, err := eng.Check(ctx, "code", CheckOptions{})
		if err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if d.Level != wantLevel {
			t.Errorf("after %d clean outcomes: level = %q, want %q", i, d.Level, wantLevel)
		}
	}
}

// A raised threshold must not demote a domain that is already past it —
// promotion is only ever assessed on Record, never re-evaluated on load.
func TestPromotion_raisingThresholdDoesNotDemote(t *testing.T) {
	ctx := context.Background()

	climb, md, trustDir := newTestEngineWithOpts(t, WithThresholds(
		config.Trust{PromoteToNotify: 2, PromoteToAutoShip: 3, PromoteToFullAuto: 4}))
	for i := 0; i < 2; i++ {
		if _, err := climb.Record(ctx, "code", OutcomeClean, RecordOptions{}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	if d, _ := climb.Check(ctx, "code", CheckOptions{}); d.Level != LevelNotify {
		t.Fatalf("setup: level = %q, want %q", d.Level, LevelNotify)
	}

	// Same on-disk state, far stricter thresholds.
	strict, err := NewEngine(ctx, trustDir, md,
		WithLockTimeout(500*time.Millisecond),
		WithThresholds(config.Trust{PromoteToNotify: 1000, PromoteToAutoShip: 1000, PromoteToFullAuto: 1000}))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	strict.syncWrites = false

	d, err := strict.Check(ctx, "code", CheckOptions{})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if d.Level != LevelNotify {
		t.Errorf("level = %q, want %q — raising a threshold must not demote", d.Level, LevelNotify)
	}
}

// A non-positive threshold would freeze the ladder rather than loosen it, so
// the option refuses it and keeps the defaults.
func TestWithThresholds_rejectsNonPositive(t *testing.T) {
	bad := []config.Trust{
		{PromoteToNotify: 0, PromoteToAutoShip: 3, PromoteToFullAuto: 4},
		{PromoteToNotify: 2, PromoteToAutoShip: -1, PromoteToFullAuto: 4},
		{PromoteToNotify: 2, PromoteToAutoShip: 3, PromoteToFullAuto: 0},
	}
	for _, tc := range bad {
		eng, _, _ := newTestEngineWithOpts(t, WithThresholds(tc))
		if eng.thresholds != defaultThresholds {
			t.Errorf("WithThresholds(%+v) applied; want defaults kept", tc)
		}
	}
}

// TestPromotion_transitions drives every promotion through Record (not
// through unexported seeding). Each case records enough clean outcomes to
// land exactly at or just before the next threshold, so the final Record
// observes the promotion.
func TestPromotion_transitions(t *testing.T) {
	cases := []struct {
		name         string
		outcomes     int
		wantLevel    Level
		wantShips    int
		wantPromotes int
	}{
		{"below_first_threshold", defaultThresholds.PromoteToNotify - 1, LevelAsk, defaultThresholds.PromoteToNotify - 1, 0},
		{"ask_to_notify", defaultThresholds.PromoteToNotify, LevelNotify, 0, 1},
		{"notify_to_auto_ship", defaultThresholds.PromoteToNotify + defaultThresholds.PromoteToAutoShip, LevelAutoShip, 0, 2},
		{"auto_ship_to_full_auto", defaultThresholds.PromoteToNotify + defaultThresholds.PromoteToAutoShip + defaultThresholds.PromoteToFullAuto, LevelFullAuto, 0, 3},
		{"full_auto_extras", defaultThresholds.PromoteToNotify + defaultThresholds.PromoteToAutoShip + defaultThresholds.PromoteToFullAuto + 5, LevelFullAuto, 5, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newTestEngine(t)
			ctx := context.Background()
			promotions := 0
			for i := 0; i < tc.outcomes; i++ {
				r, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{})
				if err != nil {
					t.Fatalf("record %d: %v", i, err)
				}
				if r.Promoted {
					promotions++
				}
			}
			dec, err := eng.Check(ctx, "code", CheckOptions{})
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if dec.Level != tc.wantLevel {
				t.Fatalf("level = %q, want %q", dec.Level, tc.wantLevel)
			}
			if dec.CleanShips != tc.wantShips {
				t.Fatalf("clean_ships = %d, want %d", dec.CleanShips, tc.wantShips)
			}
			if promotions != tc.wantPromotes {
				t.Fatalf("promotions = %d, want %d", promotions, tc.wantPromotes)
			}
		})
	}
}

func TestPromotion_lastPromotionSet(t *testing.T) {
	eng := newTestEngine(t)
	ctx := context.Background()
	for i := 0; i < defaultThresholds.PromoteToNotify; i++ {
		if _, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	dec, _ := eng.Check(ctx, "code", CheckOptions{})
	if dec.LastPromotion == nil {
		t.Fatal("expected LastPromotion set after promotion")
	}
}
