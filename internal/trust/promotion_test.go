package trust

import (
	"context"
	"testing"
)

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
		{"below_first_threshold", PromoteAskToNotify - 1, LevelAsk, PromoteAskToNotify - 1, 0},
		{"ask_to_notify", PromoteAskToNotify, LevelNotify, 0, 1},
		{"notify_to_auto_ship", PromoteAskToNotify + PromoteNotifyToAutoShip, LevelAutoShip, 0, 2},
		{"auto_ship_to_full_auto", PromoteAskToNotify + PromoteNotifyToAutoShip + PromoteAutoShipToFullAuto, LevelFullAuto, 0, 3},
		{"full_auto_extras", PromoteAskToNotify + PromoteNotifyToAutoShip + PromoteAutoShipToFullAuto + 5, LevelFullAuto, 5, 3},
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
	for i := 0; i < PromoteAskToNotify; i++ {
		if _, err := eng.Record(ctx, "code", OutcomeClean, RecordOptions{}); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	dec, _ := eng.Check(ctx, "code", CheckOptions{})
	if dec.LastPromotion == nil {
		t.Fatal("expected LastPromotion set after promotion")
	}
}
