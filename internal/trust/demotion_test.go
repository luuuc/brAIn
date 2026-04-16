package trust

import (
	"context"
	"testing"
)

func TestDemotion_fromEveryLevel(t *testing.T) {
	cases := []struct {
		name        string
		from        Level
		wantDemoted bool
	}{
		{"from_ask_stays_ask", LevelAsk, false},
		{"from_notify_to_ask", LevelNotify, true},
		{"from_auto_ship_to_ask", LevelAutoShip, true},
		{"from_full_auto_to_ask", LevelFullAuto, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newTestEngine(t)
			ctx := context.Background()
			climbTo(t, eng, "database", tc.from)

			r, err := eng.Record(ctx, "database", OutcomeFailure, RecordOptions{Reason: "broke staging"})
			if err != nil {
				t.Fatalf("Record failure: %v", err)
			}
			if r.Demoted != tc.wantDemoted {
				t.Fatalf("demoted = %v, want %v", r.Demoted, tc.wantDemoted)
			}
			if r.Decision.Level != LevelAsk {
				t.Fatalf("level after failure = %q, want ask", r.Decision.Level)
			}
			if r.Decision.CleanShips != 0 {
				t.Fatalf("clean_ships = %d, want 0", r.Decision.CleanShips)
			}
			if r.Decision.LastFailure == nil {
				t.Fatal("expected LastFailure set after failure")
			}
		})
	}
}

// TestDemotion_reasonPreserved makes a single focused assertion: the
// operator's reason string lands somewhere the user can retrieve it. We
// assert on the history event only because it's the only place the reason
// goes — if someone adds a LastFailureReason field later, adjust this.
func TestDemotion_reasonPreserved(t *testing.T) {
	eng := newTestEngine(t)
	ctx := context.Background()
	climbTo(t, eng, "database", LevelAutoShip)

	if _, err := eng.Record(ctx, "database", OutcomeFailure, RecordOptions{Reason: "migration broke prod"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	dec, _ := eng.Check(ctx, "database", CheckOptions{})
	found := false
	for _, e := range dec.History {
		if e.Kind == EventDemotion && e.Reason == "migration broke prod" {
			found = true
		}
	}
	if !found {
		t.Fatal("demotion reason missing from history")
	}
}
