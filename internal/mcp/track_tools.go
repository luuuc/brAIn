package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/store"
)

// handleTrack dispatches brain_track. With outcome, it records; without
// outcome, it returns the current rolling-window stats.
func (s *Server) handleTrack(ctx context.Context, args map[string]any) toolCallResult {
	persona, _ := args["persona"].(string)
	if persona == "" {
		return errorResult("persona is required")
	}
	domain, _ := args["domain"].(string)
	if domain == "" {
		return errorResult("domain is required")
	}
	reason, _ := args["reason"].(string)

	outcomeStr, hasOutcome := args["outcome"].(string)
	if !hasOutcome || outcomeStr == "" {
		stats, err := s.eng.EffectivenessStatsFor(ctx, persona, domain)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return errorResult(fmt.Sprintf("no effectiveness data for persona=%s domain=%s", persona, domain))
			}
			return errorResult(err.Error())
		}
		return jsonResult(trackPayload(stats, false, ""))
	}

	o := engine.Outcome(outcomeStr)
	if !o.Valid() {
		return errorResult(fmt.Sprintf("invalid outcome %q (valid: accepted, overridden)", outcomeStr))
	}
	stats, err := s.eng.Track(ctx, persona, domain, o, reason)
	if err != nil {
		return errorResult(err.Error())
	}
	return jsonResult(trackPayload(stats, true, o))
}

func trackPayload(s engine.EffectivenessStats, recorded bool, o engine.Outcome) map[string]any {
	// acceptance_rate is included only when the window had samples;
	// otherwise omit it so MCP clients can't conflate "100% overridden"
	// (0.0) with "no data" (absent).
	out := map[string]any{
		"persona":    s.Persona,
		"domain":     s.Domain,
		"recorded":   recorded,
		"accepted":   s.Accepted,
		"overridden": s.Overridden,
		"total":      s.Total,
	}
	if s.HasSamples() {
		out["acceptance_rate"] = s.AcceptanceRate
	}
	if recorded {
		out["outcome"] = string(o)
	}
	return out
}
