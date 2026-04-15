package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/brain/internal/engine"
	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
)

// newTestServer creates a Server with a stub engine and fixed clock for handler tests.
func newTestServer(eng Engine) *Server {
	return &Server{
		eng: eng,
		now: func() time.Time { return time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC) },
	}
}

// --- brain_remember ---

func TestHandler_Remember_HappyPath(t *testing.T) {
	srv := newTestServer(&stubEngine{
		rememberFn: func(_ context.Context, m memory.Memory) (engine.RememberResult, error) {
			if m.Domain != "database" {
				t.Errorf("domain = %q, want database", m.Domain)
			}
			if m.Body != "Users table has 12M rows" {
				t.Errorf("body = %q", m.Body)
			}
			if m.Source != memory.SourceTool {
				t.Errorf("source = %q, want tool", m.Source)
			}
			if m.Created.IsZero() {
				t.Error("created should not be zero")
			}
			return engine.RememberResult{Path: "facts/users-table.md", Layer: memory.LayerFact}, nil
		},
	})

	result := srv.handleRemember(context.Background(), map[string]any{
		"content": "Users table has 12M rows",
		"domain":  "database",
		"layer":   "fact",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	assertJSONContains(t, result, "path", "facts/users-table.md")
	assertJSONContains(t, result, "layer", "fact")
	assertJSONContains(t, result, "domain", "database")
}

func TestHandler_Remember_AutoClassify(t *testing.T) {
	srv := newTestServer(&stubEngine{
		rememberFn: func(_ context.Context, m memory.Memory) (engine.RememberResult, error) {
			// Layer should be empty — engine does the classification.
			return engine.RememberResult{Path: "decisions/use-cobra.md", Layer: memory.LayerDecision}, nil
		},
	})

	result := srv.handleRemember(context.Background(), map[string]any{
		"content": "We decided to use Cobra",
		"domain":  "tooling",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	assertJSONContains(t, result, "layer", "decision")
}

func TestHandler_Remember_WithTags(t *testing.T) {
	var gotTags []string
	srv := newTestServer(&stubEngine{
		rememberFn: func(_ context.Context, m memory.Memory) (engine.RememberResult, error) {
			gotTags = m.Tags
			return engine.RememberResult{Path: "facts/test.md", Layer: memory.LayerFact}, nil
		},
	})

	srv.handleRemember(context.Background(), map[string]any{
		"content": "test",
		"domain":  "db",
		"tags":    "postgres, schema, migration",
	})

	want := []string{"postgres", "schema", "migration"}
	if len(gotTags) != len(want) {
		t.Fatalf("tags = %v, want %v", gotTags, want)
	}
	for i, tag := range gotTags {
		if tag != want[i] {
			t.Errorf("tag[%d] = %q, want %q", i, tag, want[i])
		}
	}
}

func TestHandler_Remember_MissingContent(t *testing.T) {
	srv := newTestServer(&stubEngine{})
	result := srv.handleRemember(context.Background(), map[string]any{
		"domain": "db",
	})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !containsText(result, "content is required") {
		t.Errorf("text = %q", result.Content[0].Text)
	}
}

func TestHandler_Remember_MissingDomain(t *testing.T) {
	srv := newTestServer(&stubEngine{})
	result := srv.handleRemember(context.Background(), map[string]any{
		"content": "test",
	})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !containsText(result, "domain is required") {
		t.Errorf("text = %q", result.Content[0].Text)
	}
}

func TestHandler_Remember_InvalidLayer(t *testing.T) {
	srv := newTestServer(&stubEngine{})
	result := srv.handleRemember(context.Background(), map[string]any{
		"content": "test",
		"domain":  "db",
		"layer":   "bogus",
	})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !containsText(result, "invalid layer") {
		t.Errorf("text = %q", result.Content[0].Text)
	}
}

func TestHandler_Remember_EngineError(t *testing.T) {
	srv := newTestServer(&stubEngine{
		rememberFn: func(context.Context, memory.Memory) (engine.RememberResult, error) {
			return engine.RememberResult{}, fmt.Errorf("engine: %w", store.ErrConflict)
		},
	})

	result := srv.handleRemember(context.Background(), map[string]any{
		"content": "test",
		"domain":  "db",
	})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !containsText(result, "conflict") {
		t.Errorf("text = %q, want conflict mention", result.Content[0].Text)
	}
}

func TestHandler_Remember_Clock(t *testing.T) {
	var gotCreated time.Time
	srv := newTestServer(&stubEngine{
		rememberFn: func(_ context.Context, m memory.Memory) (engine.RememberResult, error) {
			gotCreated = m.Created
			return engine.RememberResult{Path: "facts/test.md", Layer: memory.LayerFact}, nil
		},
	})

	srv.handleRemember(context.Background(), map[string]any{
		"content": "test",
		"domain":  "db",
	})

	want := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	if !gotCreated.Equal(want) {
		t.Errorf("created = %v, want %v", gotCreated, want)
	}
}

// --- brain_recall ---

func TestHandler_Recall_HappyPath(t *testing.T) {
	srv := newTestServer(&stubEngine{
		recallFn: func(_ context.Context, opts engine.RecallOptions) ([]memory.Memory, error) {
			if opts.Domain != "database" {
				t.Errorf("domain = %q, want database", opts.Domain)
			}
			if opts.Limit != 5 {
				t.Errorf("limit = %d, want 5 (default)", opts.Limit)
			}
			return []memory.Memory{
				{Path: "facts/test.md", Layer: memory.LayerFact, Domain: "database", Body: "12M rows\nsecond line", Tags: []string{"postgres"}},
			}, nil
		},
	})

	result := srv.handleRecall(context.Background(), map[string]any{
		"domain": "database",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	var data struct {
		Memories []map[string]any `json:"memories"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(data.Memories) != 1 {
		t.Fatalf("got %d memories, want 1", len(data.Memories))
	}
	m := data.Memories[0]
	if m["title"] != "12M rows" {
		t.Errorf("title = %q, want '12M rows'", m["title"])
	}
	if m["body"] != "12M rows\nsecond line" {
		t.Errorf("body = %q", m["body"])
	}
	tags, ok := m["tags"].([]any)
	if !ok || len(tags) != 1 {
		t.Errorf("tags = %v", m["tags"])
	}
}

func TestHandler_Recall_CustomLimit(t *testing.T) {
	var gotLimit int
	srv := newTestServer(&stubEngine{
		recallFn: func(_ context.Context, opts engine.RecallOptions) ([]memory.Memory, error) {
			gotLimit = opts.Limit
			return nil, nil
		},
	})

	srv.handleRecall(context.Background(), map[string]any{
		"limit": float64(10), // JSON numbers are float64
	})

	if gotLimit != 10 {
		t.Errorf("limit = %d, want 10", gotLimit)
	}
}

func TestHandler_Recall_WithLayerFilter(t *testing.T) {
	var gotLayer *memory.Layer
	srv := newTestServer(&stubEngine{
		recallFn: func(_ context.Context, opts engine.RecallOptions) ([]memory.Memory, error) {
			gotLayer = opts.Layer
			return nil, nil
		},
	})

	srv.handleRecall(context.Background(), map[string]any{
		"layer": "correction",
	})

	if gotLayer == nil || *gotLayer != memory.LayerCorrection {
		t.Errorf("layer = %v, want correction", gotLayer)
	}
}

func TestHandler_Recall_InvalidLayer(t *testing.T) {
	srv := newTestServer(&stubEngine{})
	result := srv.handleRecall(context.Background(), map[string]any{
		"layer": "bogus",
	})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !containsText(result, "invalid layer") {
		t.Errorf("text = %q", result.Content[0].Text)
	}
}

func TestHandler_Recall_EmptyResult(t *testing.T) {
	srv := newTestServer(&stubEngine{
		recallFn: func(context.Context, engine.RecallOptions) ([]memory.Memory, error) {
			return nil, nil
		},
	})

	result := srv.handleRecall(context.Background(), map[string]any{})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	var data struct {
		Memories []any `json:"memories"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(data.Memories) != 0 {
		t.Errorf("got %d memories, want 0", len(data.Memories))
	}
}

// --- brain_list ---

func TestHandler_List_HappyPath(t *testing.T) {
	srv := newTestServer(&stubEngine{
		recallFn: func(_ context.Context, opts engine.RecallOptions) ([]memory.Memory, error) {
			if opts.Limit != 0 {
				t.Errorf("limit = %d, want 0 (no limit)", opts.Limit)
			}
			return []memory.Memory{
				{Path: "facts/a.md", Layer: memory.LayerFact, Domain: "db", Created: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
				{Path: "lessons/b.md", Layer: memory.LayerLesson, Domain: "db", Created: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Retired: true},
			}, nil
		},
	})

	result := srv.handleList(context.Background(), map[string]any{
		"domain":          "db",
		"include_retired": true,
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	var data struct {
		Memories []map[string]any `json:"memories"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(data.Memories) != 2 {
		t.Fatalf("got %d memories, want 2", len(data.Memories))
	}
	// Second memory should have retired=true
	if data.Memories[1]["retired"] != true {
		t.Error("second memory should be retired")
	}
	// First memory should not have retired key
	if _, ok := data.Memories[0]["retired"]; ok {
		t.Error("first memory should not have retired key")
	}
}

func TestHandler_List_InvalidLayer(t *testing.T) {
	srv := newTestServer(&stubEngine{})
	result := srv.handleList(context.Background(), map[string]any{
		"layer": "bogus",
	})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !containsText(result, "invalid layer") {
		t.Errorf("text = %q", result.Content[0].Text)
	}
}

// --- brain_forget ---

func TestHandler_Forget_HappyPath(t *testing.T) {
	var gotPath, gotReason string
	srv := newTestServer(&stubEngine{
		forgetFn: func(_ context.Context, path, reason string) error {
			gotPath = path
			gotReason = reason
			return nil
		},
	})

	result := srv.handleForget(context.Background(), map[string]any{
		"path":   "facts/old.md",
		"reason": "outdated",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}
	if gotPath != "facts/old.md" {
		t.Errorf("path = %q", gotPath)
	}
	if gotReason != "outdated" {
		t.Errorf("reason = %q", gotReason)
	}
	assertJSONContains(t, result, "status", "retired")
}

func TestHandler_Forget_MissingPath(t *testing.T) {
	srv := newTestServer(&stubEngine{})
	result := srv.handleForget(context.Background(), map[string]any{})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !containsText(result, "path is required") {
		t.Errorf("text = %q", result.Content[0].Text)
	}
}

func TestHandler_Forget_NotFound(t *testing.T) {
	srv := newTestServer(&stubEngine{
		forgetFn: func(context.Context, string, string) error {
			return fmt.Errorf("retire %q: %w", "facts/gone.md", store.ErrNotFound)
		},
	})

	result := srv.handleForget(context.Background(), map[string]any{
		"path": "facts/gone.md",
	})
	if !result.IsError {
		t.Fatal("expected error")
	}
	if !containsText(result, "not found") {
		t.Errorf("text = %q, want 'not found'", result.Content[0].Text)
	}
}

// --- engineError mapping ---

func TestEngineError_Mapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"ErrNotFound", fmt.Errorf("retire: %w", store.ErrNotFound), "not found"},
		{"ErrConflict", fmt.Errorf("write: %w", store.ErrConflict), "conflict"},
		{"generic", fmt.Errorf("something broke"), "something broke"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engineError(tt.err)
			if !result.IsError {
				t.Fatal("expected IsError=true")
			}
			if !containsText(result, tt.want) {
				t.Errorf("text = %q, want to contain %q", result.Content[0].Text, tt.want)
			}
		})
	}
}

// --- helpers ---

func assertJSONContains(t *testing.T, result toolCallResult, key, want string) {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got, ok := data[key].(string)
	if !ok {
		t.Errorf("key %q not found or not string in %s", key, result.Content[0].Text)
		return
	}
	if got != want {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}

func containsText(result toolCallResult, substr string) bool {
	if len(result.Content) == 0 {
		return false
	}
	return strings.Contains(result.Content[0].Text, substr)
}
