package trust

// DecisionJSON is the shared serializable projection of a Decision used by
// both the CLI (--json) and MCP tools. History is included only when
// verbose is true — verbose is the only knob; everything else comes from
// the Decision as-is.
func DecisionJSON(d Decision, verbose bool) map[string]any {
	out := map[string]any{
		"domain":         d.Domain,
		"level":          string(d.Level),
		"clean_ships":    d.CleanShips,
		"recommendation": string(d.Recommendation),
	}
	if d.Hotfix {
		out["urgency"] = "hotfix"
	}
	if d.LastFailure != nil {
		out["last_failure"] = d.LastFailure.Format("2006-01-02")
	}
	if d.LastPromotion != nil {
		out["last_promotion"] = d.LastPromotion.Format("2006-01-02")
	}
	if verbose && len(d.History) > 0 {
		out["history"] = eventsJSON(d.History)
	}
	return out
}

func eventsJSON(evs []Event) []map[string]any {
	out := make([]map[string]any, 0, len(evs))
	for _, e := range evs {
		ev := map[string]any{
			"at":   e.At.Format("2006-01-02T15:04:05Z07:00"),
			"kind": string(e.Kind),
		}
		if e.Outcome != "" {
			ev["outcome"] = string(e.Outcome)
		}
		if e.From != "" {
			ev["from"] = string(e.From)
		}
		if e.To != "" {
			ev["to"] = string(e.To)
		}
		if e.Ref != "" {
			ev["ref"] = e.Ref
		}
		if e.Reason != "" {
			ev["reason"] = e.Reason
		}
		out = append(out, ev)
	}
	return out
}
