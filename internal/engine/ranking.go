package engine

import (
	"sort"
	"time"

	"github.com/luuuc/brain/internal/memory"
)

// RankOptions controls how Rank sorts and filters memories.
type RankOptions struct {
	Now                 time.Time              // current time for staleness checks
	Limit               int                    // max results; 0 means no limit (contrast with RecallOptions.Limit where 0 means default 5)
	IncludeRetired      bool                   // include retired lessons
	EffectivenessScores map[string]float64     // placeholder for pitch 01-06
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
	// Filter retired lessons unless IncludeRetired.
	var filtered []memory.Memory
	for _, m := range memories {
		if m.Retired && !opts.IncludeRetired {
			continue
		}
		filtered = append(filtered, m)
	}

	now := opts.Now

	// Sort by: authority (layer), then stale-last within same authority, then recency.
	sort.SliceStable(filtered, func(i, j int) bool {
		ai, aj := layerAuthority(filtered[i].Layer), layerAuthority(filtered[j].Layer)
		if ai != aj {
			return ai < aj
		}

		// Within same layer, non-stale before stale.
		si, sj := isStale(filtered[i], now), isStale(filtered[j], now)
		if si != sj {
			return !si // non-stale first
		}

		// Within same staleness, most recent first.
		ti, tj := effectiveTime(filtered[i]), effectiveTime(filtered[j])
		return ti.After(tj)
	})

	// Apply limit.
	if opts.Limit > 0 && len(filtered) > opts.Limit {
		filtered = filtered[:opts.Limit]
	}

	return filtered
}

// effectiveTime returns the most recent timestamp for sorting.
func effectiveTime(m memory.Memory) time.Time {
	if m.Updated != nil {
		return *m.Updated
	}
	return m.Created
}
