package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/luuuc/brain/internal/store"
	"github.com/luuuc/brain/internal/trust"
)

// --- brain_trust ---

func TestHandler_Trust_HappyPath(t *testing.T) {
	srv := newTestServerWithTrust(&stubEngine{}, &stubTrust{
		checkFn: func(_ context.Context, d string, opts trust.CheckOptions) (trust.Decision, error) {
			if d != "database" {
				t.Errorf("domain = %q", d)
			}
			if !opts.Hotfix {
				t.Error("hotfix should be true")
			}
			return trust.Decision{
				Domain:         d,
				Level:          trust.LevelAsk,
				CleanShips:     3,
				Hotfix:         opts.Hotfix,
				Recommendation: trust.Recommend(trust.LevelAsk, opts.Hotfix),
			}, nil
		},
	})

	result := srv.handleTrust(context.Background(), map[string]any{
		"domain":  "database",
		"urgency": "hotfix",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	assertJSONContains(t, result, "level", "ask")
	assertJSONContains(t, result, "recommendation", "ship_notify")
}

func TestHandler_Trust_MissingDomain(t *testing.T) {
	srv := newTestServerWithTrust(&stubEngine{}, &stubTrust{})
	result := srv.handleTrust(context.Background(), map[string]any{})
	if !result.IsError {
		t.Fatal("expected error for missing domain")
	}
}

func TestHandler_Trust_InvalidUrgency(t *testing.T) {
	srv := newTestServerWithTrust(&stubEngine{}, &stubTrust{})
	result := srv.handleTrust(context.Background(), map[string]any{
		"domain":  "db",
		"urgency": "whenever",
	})
	if !result.IsError {
		t.Fatal("expected error for invalid urgency")
	}
}

func TestHandler_Trust_VerboseIncludesHistory(t *testing.T) {
	srv := newTestServerWithTrust(&stubEngine{}, &stubTrust{
		checkFn: func(_ context.Context, d string, _ trust.CheckOptions) (trust.Decision, error) {
			return trust.Decision{
				Domain:         d,
				Level:          trust.LevelNotify,
				Recommendation: trust.RecommendationShipNotify,
				History: []trust.Event{
					{Kind: trust.EventOutcome, Outcome: trust.OutcomeClean, Ref: "PR #1"},
				},
			}, nil
		},
	})
	result := srv.handleTrust(context.Background(), map[string]any{
		"domain":  "db",
		"verbose": true,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := payload["history"]; !ok {
		t.Fatal("expected history in verbose response")
	}
}

// --- brain_trust_record ---

func TestHandler_TrustRecord_Clean(t *testing.T) {
	srv := newTestServerWithTrust(&stubEngine{}, &stubTrust{
		recordFn: func(_ context.Context, d string, o trust.Outcome, opts trust.RecordOptions) (trust.RecordResult, error) {
			if d != "database" || o != trust.OutcomeClean {
				t.Errorf("unexpected args: %q %q", d, o)
			}
			if opts.Ref != "PR #42" {
				t.Errorf("ref = %q", opts.Ref)
			}
			return trust.RecordResult{
				Decision: trust.Decision{Domain: d, Level: trust.LevelAsk, CleanShips: 1, Recommendation: trust.RecommendationEscalate},
			}, nil
		},
	})

	result := srv.handleTrustRecord(context.Background(), map[string]any{
		"domain":  "database",
		"outcome": "clean",
		"ref":     "PR #42",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["deduplicated"] != false {
		t.Errorf("deduplicated should be false, got %v", payload["deduplicated"])
	}
}

func TestHandler_TrustRecord_InvalidOutcome(t *testing.T) {
	srv := newTestServerWithTrust(&stubEngine{}, &stubTrust{})
	result := srv.handleTrustRecord(context.Background(), map[string]any{
		"domain":  "db",
		"outcome": "maybe",
	})
	if !result.IsError {
		t.Fatal("expected error for invalid outcome")
	}
}

func TestHandler_TrustRecord_ConflictMapsToError(t *testing.T) {
	srv := newTestServerWithTrust(&stubEngine{}, &stubTrust{
		recordFn: func(context.Context, string, trust.Outcome, trust.RecordOptions) (trust.RecordResult, error) {
			// Real %w wrap so errors.Is in trustErr actually fires.
			return trust.RecordResult{}, fmt.Errorf("trust: record: %w: test", store.ErrConflict)
		},
	})
	result := srv.handleTrustRecord(context.Background(), map[string]any{
		"domain":  "db",
		"outcome": "clean",
	})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(result.Content[0].Text, "conflict:") {
		t.Fatalf("expected 'conflict:' prefix, got %q", result.Content[0].Text)
	}
}

// --- brain_trust_override ---

func TestHandler_TrustOverride_HappyPath(t *testing.T) {
	srv := newTestServerWithTrust(&stubEngine{}, &stubTrust{
		overrideFn: func(_ context.Context, d, r string) (trust.Decision, error) {
			if d != "database" || r == "" {
				t.Errorf("args = %q %q", d, r)
			}
			return trust.Decision{Domain: d, Level: trust.LevelAsk, Recommendation: trust.RecommendationEscalate}, nil
		},
	})

	result := srv.handleTrustOverride(context.Background(), map[string]any{
		"domain": "database",
		"reason": "owner disagrees",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	assertJSONContains(t, result, "domain", "database")
}

func TestHandler_TrustOverride_MissingReason(t *testing.T) {
	srv := newTestServerWithTrust(&stubEngine{}, &stubTrust{})
	result := srv.handleTrustOverride(context.Background(), map[string]any{"domain": "db"})
	if !result.IsError {
		t.Fatal("expected error for missing reason")
	}
}
