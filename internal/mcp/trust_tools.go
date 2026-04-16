package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/luuuc/brain/internal/store"
	"github.com/luuuc/brain/internal/trust"
)

func (s *Server) handleTrust(ctx context.Context, args map[string]any) toolCallResult {
	domain, _ := args["domain"].(string)
	if domain == "" {
		return errorResult("domain is required")
	}
	opts := trust.CheckOptions{}
	if u, ok := args["urgency"].(string); ok && u != "" {
		if u != "hotfix" {
			return errorResult(fmt.Sprintf("invalid urgency %q (valid: hotfix)", u))
		}
		opts.Hotfix = true
	}
	verbose, _ := args["verbose"].(bool)

	dec, err := s.trust.Check(ctx, domain, opts)
	if err != nil {
		return trustErr(err)
	}
	return jsonResult(trust.DecisionJSON(dec, verbose))
}

func (s *Server) handleTrustRecord(ctx context.Context, args map[string]any) toolCallResult {
	domain, _ := args["domain"].(string)
	if domain == "" {
		return errorResult("domain is required")
	}
	outcomeStr, _ := args["outcome"].(string)
	o := trust.Outcome(outcomeStr)
	if !o.Valid() {
		return errorResult(fmt.Sprintf("invalid outcome %q (valid: clean, failure)", outcomeStr))
	}
	ref, _ := args["ref"].(string)
	reason, _ := args["reason"].(string)

	r, err := s.trust.Record(ctx, domain, o, trust.RecordOptions{Ref: ref, Reason: reason})
	if err != nil {
		return trustErr(err)
	}

	payload := map[string]any{
		"decision":     trust.DecisionJSON(r.Decision, false),
		"promoted":     r.Promoted,
		"demoted":      r.Demoted,
		"deduplicated": r.Deduplicated,
	}
	if r.LessonsTouched > 0 {
		payload["lessons_touched"] = r.LessonsTouched
	}
	if r.LessonsRetired > 0 {
		payload["lessons_retired"] = r.LessonsRetired
	}
	if r.EvictedRefs > 0 {
		payload["evicted_refs"] = r.EvictedRefs
	}
	return jsonResult(payload)
}

func (s *Server) handleTrustOverride(ctx context.Context, args map[string]any) toolCallResult {
	domain, _ := args["domain"].(string)
	if domain == "" {
		return errorResult("domain is required")
	}
	reason, _ := args["reason"].(string)
	if reason == "" {
		return errorResult("reason is required")
	}
	dec, err := s.trust.Override(ctx, domain, reason)
	if err != nil {
		return trustErr(err)
	}
	return jsonResult(trust.DecisionJSON(dec, false))
}

// trustErr maps known trust-path errors to tool results.
func trustErr(err error) toolCallResult {
	if errors.Is(err, store.ErrConflict) {
		return errorResult(fmt.Sprintf("conflict: %s", err))
	}
	return errorResult(err.Error())
}
