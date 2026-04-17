package engine

import (
	"sort"
	"time"

	"github.com/luuuc/brain/internal/memory"
)

// RankOptions controls how Rank sorts and filters memories.
type RankOptions struct {
	Now            time.Time // current time for staleness checks
	Limit          int       // max results; 0 means no limit (contrast with RecallOptions.Limit where 0 means default 5)
	IncludeRetired bool      // include retired lessons
	// EffectivenessScores maps persona → acceptance rate (0..1). When
	// set, memories whose Persona field appears in the map rank above
	// those with lower scores within the same layer. Memories without a
	// persona (or without an entry in the map) are treated as score 0,
	// which puts them at the tail of the layer and lets recency break
	// the final tie.
	EffectivenessScores map[string]float64
}

// layerAuthority returns the authority rank for a layer. Lower = higher authority.
func layerAuthority(l memory.Layer) int {
	switch l {
	case memory.LayerCorrection:
		return 0
	case memory.LayerDecision:
		return 1
	case memory.LayerLesson:
		return 2
	case memory.LayerFact:
		return 3
	case memory.LayerEffectiveness:
		return 4
	default:
		return 5
	}
}

// isStale returns true if a fact has passed its stale_after date.
func isStale(m memory.Memory, now time.Time) bool {
	if m.Layer != memory.LayerFact {
		return false
	}
	if m.StaleAfter == nil {
		return false
	}
	return now.After(*m.StaleAfter)
}

// Rank sorts memories by authority hierarchy and recency, excludes retired
// lessons (unless IncludeRetired), deprioritizes stale facts, and applies
// the limit.
func Rank(memories []memory.Memory, opts RankOptions) []memory.Memory {
	var filtered []memory.Memory
	for _, m := range memories {
		if m.Retired && !opts.IncludeRetired {
			continue
		}
		filtered = append(filtered, m)
	}

	now := opts.Now
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]

		if ra, rb := layerAuthority(a.Layer), layerAuthority(b.Layer); ra != rb {
			return ra < rb
		}
		if sa, sb := isStale(a, now), isStale(b, now); sa != sb {
			return !sa // non-stale first
		}
		if less, decided := compareEffectiveness(a, b, opts.EffectivenessScores); decided {
			return less
		}
		return effectiveTime(a).After(effectiveTime(b))
	})

	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[:opts.Limit]
	}

	return filtered
}

// compareEffectiveness returns (less, decided). When decided is false, the
// two memories tie on effectiveness and the caller should fall through to
// the next tie-break. Missing or unknown personas are treated as score 0.
func compareEffectiveness(a, b memory.Memory, scores map[string]float64) (less, decided bool) {
	if len(scores) == 0 {
		return false, false
	}
	scoreA := scores[a.Persona]
	scoreB := scores[b.Persona]
	if scoreA == scoreB {
		return false, false
	}
	return scoreA > scoreB, true
}

// effectiveTime returns the most recent timestamp for sorting.
func effectiveTime(m memory.Memory) time.Time {
	if m.Updated != nil {
		return *m.Updated
	}
	return m.Created
}
