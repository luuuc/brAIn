package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/store"
)

func TestHandler_Track_RecordHappyPath(t *testing.T) {
	var gotPersona, gotDomain, gotReason string
	var gotOutcome engine.Outcome
	srv := newTestServer(&stubEngine{
		trackFn: func(_ context.Context, persona, domain string, o engine.Outcome, reason string) (engine.EffectivenessStats, error) {
			gotPersona, gotDomain, gotOutcome, gotReason = persona, domain, o, reason
			return engine.EffectivenessStats{
				Persona: persona, Domain: domain,
				Accepted: 1, Total: 1, AcceptanceRate: 1.0,
				WindowDays: 90,
			}, nil
		},
	})

	result := srv.handleTrack(context.Background(), map[string]any{
		"persona": "kent-beck",
		"domain":  "testing",
		"outcome": "accepted",
		"reason":  "PR #52",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if gotPersona != "kent-beck" || gotDomain != "testing" || gotOutcome != engine.OutcomeAccepted || gotReason != "PR #52" {
		t.Errorf("engine args: persona=%q domain=%q outcome=%q reason=%q",
			gotPersona, gotDomain, gotOutcome, gotReason)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["recorded"] != true {
		t.Errorf("recorded = %v, want true", payload["recorded"])
	}
	if payload["outcome"] != "accepted" {
		t.Errorf("outcome = %v, want accepted", payload["outcome"])
	}
	if payload["acceptance_rate"] != 1.0 {
		t.Errorf("acceptance_rate = %v, want 1.0", payload["acceptance_rate"])
	}
	if _, ok := payload["window_days"]; ok {
		t.Errorf("window_days should be absent from MCP payload (immutable constant)")
	}
}

func TestHandler_Track_ViewStatsWhenNoOutcome(t *testing.T) {
	srv := newTestServer(&stubEngine{
		effectivenessFn: func(_ context.Context, persona, domain string) (engine.EffectivenessStats, error) {
			return engine.EffectivenessStats{
				Persona: persona, Domain: domain,
				Accepted: 8, Overridden: 2, Total: 10,
				AcceptanceRate: 0.8, WindowDays: 90,
			}, nil
		},
		trackFn: func(context.Context, string, string, engine.Outcome, string) (engine.EffectivenessStats, error) {
			t.Fatal("Track must not be called when outcome is omitted")
			return engine.EffectivenessStats{}, nil
		},
	})

	result := srv.handleTrack(context.Background(), map[string]any{
		"persona": "kent-beck",
		"domain":  "testing",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["recorded"] != false {
		t.Errorf("recorded = %v, want false", payload["recorded"])
	}
	if _, present := payload["outcome"]; present {
		t.Errorf("outcome should be absent on view-only, got %v", payload["outcome"])
	}
	if payload["total"] != float64(10) {
		t.Errorf("total = %v, want 10", payload["total"])
	}
}

func TestHandler_Track_OmitsAcceptanceRateWhenNoSamples(t *testing.T) {
	// Total==0 means no samples in the window. The wire format must
	// omit acceptance_rate entirely so clients can't confuse it with
	// "all overridden" (a real 0.0 that MUST be present).
	srv := newTestServer(&stubEngine{
		effectivenessFn: func(_ context.Context, persona, domain string) (engine.EffectivenessStats, error) {
			return engine.EffectivenessStats{
				Persona: persona, Domain: domain,
				// All aged out of the window — file exists, samples don't.
				Total: 0, WindowDays: 90,
			}, nil
		},
	})
	result := srv.handleTrack(context.Background(), map[string]any{
		"persona": "silent", "domain": "testing",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := payload["acceptance_rate"]; present {
		t.Errorf("acceptance_rate must be absent when Total==0, got %v", payload["acceptance_rate"])
	}
	if payload["total"] != float64(0) {
		t.Errorf("total = %v, want 0", payload["total"])
	}
}

func TestHandler_Track_ViewUnknownReturnsError(t *testing.T) {
	srv := newTestServer(&stubEngine{
		effectivenessFn: func(context.Context, string, string) (engine.EffectivenessStats, error) {
			return engine.EffectivenessStats{}, store.ErrNotFound
		},
	})
	result := srv.handleTrack(context.Background(), map[string]any{
		"persona": "ghost",
		"domain":  "testing",
	})
	if !result.IsError {
		t.Fatal("expected error for unknown persona-domain on view")
	}
	if !containsText(result, "no effectiveness data") {
		t.Errorf("text = %q, want 'no effectiveness data'", result.Content[0].Text)
	}
}

func TestHandler_Track_MissingPersona(t *testing.T) {
	srv := newTestServer(&stubEngine{})
	result := srv.handleTrack(context.Background(), map[string]any{"domain": "testing", "outcome": "accepted"})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !containsText(result, "persona is required") {
		t.Errorf("text = %q", result.Content[0].Text)
	}
}

func TestHandler_Track_MissingDomain(t *testing.T) {
	srv := newTestServer(&stubEngine{})
	result := srv.handleTrack(context.Background(), map[string]any{"persona": "kent-beck", "outcome": "accepted"})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !containsText(result, "domain is required") {
		t.Errorf("text = %q", result.Content[0].Text)
	}
}

func TestHandler_Track_InvalidOutcome(t *testing.T) {
	srv := newTestServer(&stubEngine{})
	result := srv.handleTrack(context.Background(), map[string]any{
		"persona": "kent-beck",
		"domain":  "testing",
		"outcome": "maybe",
	})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !containsText(result, "invalid outcome") {
		t.Errorf("text = %q", result.Content[0].Text)
	}
}

func TestHandler_Track_EmptyOutcomeTakesViewPath(t *testing.T) {
	// Empty outcome string means "view stats" — Track must not be called.
	var trackCalled bool
	srv := newTestServer(&stubEngine{
		trackFn: func(context.Context, string, string, engine.Outcome, string) (engine.EffectivenessStats, error) {
			trackCalled = true
			return engine.EffectivenessStats{}, nil
		},
	})
	result := srv.handleTrack(context.Background(), map[string]any{
		"persona": "kent-beck", "domain": "testing", "outcome": "",
	})
	if result.IsError {
		t.Fatalf("unexpected error on empty outcome: %s", result.Content[0].Text)
	}
	if trackCalled {
		t.Error("Track must not be called when outcome is empty string")
	}
}

func TestHandler_Track_EngineErrorPropagates(t *testing.T) {
	// Track returns a validation error (e.g. bad persona slug). The
	// handler surfaces the error message verbatim.
	srv := newTestServer(&stubEngine{
		trackFn: func(context.Context, string, string, engine.Outcome, string) (engine.EffectivenessStats, error) {
			return engine.EffectivenessStats{}, errBadPersona
		},
	})
	result := srv.handleTrack(context.Background(), map[string]any{
		"persona": "../etc/passwd", "domain": "testing", "outcome": "accepted",
	})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !containsText(result, "invalid persona") {
		t.Errorf("text = %q, want to contain 'invalid persona'", result.Content[0].Text)
	}
}

var errBadPersona = &stubErr{msg: "engine: track: invalid persona"}

type stubErr struct{ msg string }

func (e *stubErr) Error() string { return e.msg }
