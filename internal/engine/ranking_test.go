package engine

import (
	"testing"
	"time"

	"github.com/luuuc/brain/internal/memory"
)

func TestRank(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	past := func(days int) time.Time { return now.AddDate(0, 0, -days) }
	pastPtr := func(days int) *time.Time { t := past(days); return &t }

	tests := []struct {
		name     string
		memories []memory.Memory
		opts     RankOptions
		want     []string // expected Paths in order
	}{
		{
			name:     "empty slice",
			memories: nil,
			opts:     RankOptions{Now: now, Limit: 5},
			want:     nil,
		},
		{
			name: "single memory",
			memories: []memory.Memory{
				{Path: "facts/a.md", Layer: memory.LayerFact, Created: past(1)},
			},
			opts: RankOptions{Now: now, Limit: 5},
			want: []string{"facts/a.md"},
		},
		{
			name: "authority ordering: correction > decision > lesson > fact",
			memories: []memory.Memory{
				{Path: "facts/a.md", Layer: memory.LayerFact, Created: past(1)},
				{Path: "lessons/b.md", Layer: memory.LayerLesson, Created: past(1)},
				{Path: "corrections/c.md", Layer: memory.LayerCorrection, Created: past(1)},
				{Path: "decisions/d.md", Layer: memory.LayerDecision, Created: past(1)},
			},
			opts: RankOptions{Now: now, Limit: 10},
			want: []string{"corrections/c.md", "decisions/d.md", "lessons/b.md", "facts/a.md"},
		},
		{
			name: "recency within same layer",
			memories: []memory.Memory{
				{Path: "facts/old.md", Layer: memory.LayerFact, Created: past(10)},
				{Path: "facts/new.md", Layer: memory.LayerFact, Created: past(1)},
				{Path: "facts/mid.md", Layer: memory.LayerFact, Created: past(5)},
			},
			opts: RankOptions{Now: now, Limit: 10},
			want: []string{"facts/new.md", "facts/mid.md", "facts/old.md"},
		},
		{
			name: "updated time used for recency",
			memories: []memory.Memory{
				{Path: "facts/a.md", Layer: memory.LayerFact, Created: past(10), Updated: pastPtr(1)},
				{Path: "facts/b.md", Layer: memory.LayerFact, Created: past(2)},
			},
			opts: RankOptions{Now: now, Limit: 10},
			want: []string{"facts/a.md", "facts/b.md"},
		},
		{
			name: "stale facts deprioritized within fact layer",
			memories: []memory.Memory{
				{Path: "facts/stale.md", Layer: memory.LayerFact, Created: past(40), StaleAfter: pastPtr(10)},
				{Path: "facts/fresh.md", Layer: memory.LayerFact, Created: past(5)},
			},
			opts: RankOptions{Now: now, Limit: 10},
			want: []string{"facts/fresh.md", "facts/stale.md"},
		},
		{
			name: "stale facts still returned within limit",
			memories: []memory.Memory{
				{Path: "facts/stale.md", Layer: memory.LayerFact, Created: past(40), StaleAfter: pastPtr(10)},
			},
			opts: RankOptions{Now: now, Limit: 5},
			want: []string{"facts/stale.md"},
		},
		{
			name: "all stale",
			memories: []memory.Memory{
				{Path: "facts/a.md", Layer: memory.LayerFact, Created: past(50), StaleAfter: pastPtr(20)},
				{Path: "facts/b.md", Layer: memory.LayerFact, Created: past(40), StaleAfter: pastPtr(10)},
			},
			opts: RankOptions{Now: now, Limit: 10},
			want: []string{"facts/b.md", "facts/a.md"}, // still sorted by recency
		},
		{
			name: "retired lessons excluded by default",
			memories: []memory.Memory{
				{Path: "lessons/active.md", Layer: memory.LayerLesson, Created: past(1)},
				{Path: "lessons/retired.md", Layer: memory.LayerLesson, Created: past(1), Retired: true},
			},
			opts: RankOptions{Now: now, Limit: 10},
			want: []string{"lessons/active.md"},
		},
		{
			name: "retired lessons included when IncludeRetired",
			memories: []memory.Memory{
				{Path: "lessons/active.md", Layer: memory.LayerLesson, Created: past(1)},
				{Path: "lessons/retired.md", Layer: memory.LayerLesson, Created: past(2), Retired: true},
			},
			opts: RankOptions{Now: now, Limit: 10, IncludeRetired: true},
			want: []string{"lessons/active.md", "lessons/retired.md"},
		},
		{
			name: "limit applied after ranking",
			memories: []memory.Memory{
				{Path: "corrections/a.md", Layer: memory.LayerCorrection, Created: past(1)},
				{Path: "decisions/b.md", Layer: memory.LayerDecision, Created: past(1)},
				{Path: "lessons/c.md", Layer: memory.LayerLesson, Created: past(1)},
				{Path: "facts/d.md", Layer: memory.LayerFact, Created: past(1)},
			},
			opts: RankOptions{Now: now, Limit: 2},
			want: []string{"corrections/a.md", "decisions/b.md"},
		},
		{
			name: "limit zero means no limit",
			memories: []memory.Memory{
				{Path: "facts/a.md", Layer: memory.LayerFact, Created: past(1)},
				{Path: "facts/b.md", Layer: memory.LayerFact, Created: past(2)},
				{Path: "facts/c.md", Layer: memory.LayerFact, Created: past(3)},
			},
			opts: RankOptions{Now: now, Limit: 0},
			want: []string{"facts/a.md", "facts/b.md", "facts/c.md"},
		},
		{
			name: "staleness only affects facts",
			memories: []memory.Memory{
				// A lesson with a StaleAfter set (shouldn't happen, but shouldn't break).
				{Path: "lessons/a.md", Layer: memory.LayerLesson, Created: past(40), StaleAfter: pastPtr(10)},
				{Path: "lessons/b.md", Layer: memory.LayerLesson, Created: past(1)},
			},
			opts: RankOptions{Now: now, Limit: 10},
			want: []string{"lessons/b.md", "lessons/a.md"}, // sorted by recency, not staleness
		},
		{
			name: "mixed authority with stale and retired",
			memories: []memory.Memory{
				{Path: "facts/stale.md", Layer: memory.LayerFact, Created: past(40), StaleAfter: pastPtr(10)},
				{Path: "lessons/retired.md", Layer: memory.LayerLesson, Created: past(1), Retired: true},
				{Path: "corrections/c.md", Layer: memory.LayerCorrection, Created: past(5)},
				{Path: "facts/fresh.md", Layer: memory.LayerFact, Created: past(2)},
			},
			opts: RankOptions{Now: now, Limit: 10},
			want: []string{"corrections/c.md", "facts/fresh.md", "facts/stale.md"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Rank(tt.memories, tt.opts)

			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i, m := range got {
				if m.Path != tt.want[i] {
					t.Errorf("got[%d].Path = %q, want %q", i, m.Path, tt.want[i])
				}
			}
		})
	}
}

func TestRank_EffectivenessAdjustment(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	past := func(days int) time.Time { return now.AddDate(0, 0, -days) }

	tests := []struct {
		name     string
		memories []memory.Memory
		scores   map[string]float64
		want     []string
	}{
		{
			name: "higher-scored persona ranks first within layer",
			memories: []memory.Memory{
				{Path: "lessons/a.md", Layer: memory.LayerLesson, Created: past(1), Persona: "kent-beck"},
				{Path: "lessons/b.md", Layer: memory.LayerLesson, Created: past(2), Persona: "rich-hickey"},
			},
			scores: map[string]float64{
				"kent-beck":   0.4,
				"rich-hickey": 0.9,
			},
			want: []string{"lessons/b.md", "lessons/a.md"},
		},
		{
			name: "memories without persona fall behind ranked ones",
			memories: []memory.Memory{
				{Path: "lessons/unranked.md", Layer: memory.LayerLesson, Created: past(1)},
				{Path: "lessons/ranked.md", Layer: memory.LayerLesson, Created: past(5), Persona: "kent-beck"},
			},
			scores: map[string]float64{"kent-beck": 0.1},
			want:   []string{"lessons/ranked.md", "lessons/unranked.md"},
		},
		{
			name: "tied scores fall through to recency",
			memories: []memory.Memory{
				{Path: "lessons/older.md", Layer: memory.LayerLesson, Created: past(5), Persona: "a"},
				{Path: "lessons/newer.md", Layer: memory.LayerLesson, Created: past(1), Persona: "b"},
			},
			scores: map[string]float64{"a": 0.5, "b": 0.5},
			want:   []string{"lessons/newer.md", "lessons/older.md"},
		},
		{
			name: "effectiveness never crosses layer boundaries",
			memories: []memory.Memory{
				{Path: "facts/hi-score.md", Layer: memory.LayerFact, Created: past(1), Persona: "high"},
				{Path: "lessons/low-score.md", Layer: memory.LayerLesson, Created: past(1), Persona: "low"},
			},
			scores: map[string]float64{"high": 1.0, "low": 0.0},
			want:   []string{"lessons/low-score.md", "facts/hi-score.md"},
		},
		{
			name: "empty score map leaves ranking unchanged",
			memories: []memory.Memory{
				{Path: "lessons/a.md", Layer: memory.LayerLesson, Created: past(5), Persona: "kent-beck"},
				{Path: "lessons/b.md", Layer: memory.LayerLesson, Created: past(1), Persona: "rich-hickey"},
			},
			scores: map[string]float64{},
			want:   []string{"lessons/b.md", "lessons/a.md"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Rank(tt.memories, RankOptions{Now: now, Limit: 10, EffectivenessScores: tt.scores})
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i, m := range got {
				if m.Path != tt.want[i] {
					t.Errorf("got[%d].Path = %q, want %q", i, m.Path, tt.want[i])
				}
			}
		})
	}
}
