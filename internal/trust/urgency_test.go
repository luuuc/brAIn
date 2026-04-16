package trust

import "testing"

// TestRecommend_matrix covers level × hotfix → recommendation. Hotfix
// raises the floor at ask to ship_notify; higher levels are unaffected.
func TestRecommend_matrix(t *testing.T) {
	cases := []struct {
		level  Level
		hotfix bool
		want   Recommendation
	}{
		{LevelAsk, false, RecommendationEscalate},
		{LevelNotify, false, RecommendationShipNotify},
		{LevelAutoShip, false, RecommendationShip},
		{LevelFullAuto, false, RecommendationShip},
		{LevelAsk, true, RecommendationShipNotify},
		{LevelNotify, true, RecommendationShipNotify},
		{LevelAutoShip, true, RecommendationShip},
		{LevelFullAuto, true, RecommendationShip},
	}
	for _, tc := range cases {
		name := string(tc.level)
		if tc.hotfix {
			name += "_hotfix"
		}
		t.Run(name, func(t *testing.T) {
			if got := Recommend(tc.level, tc.hotfix); got != tc.want {
				t.Fatalf("Recommend(%q, %v) = %q, want %q", tc.level, tc.hotfix, got, tc.want)
			}
		})
	}
}
