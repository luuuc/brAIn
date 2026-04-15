package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
)

// TestStore runs the conformance suite against any Store implementation.
// newStore must return a fresh, empty store for each call.
func TestStore(t *testing.T, newStore func(t *testing.T) store.Store) {
	t.Helper()

	ctx := context.Background()

	t.Run("write_and_read_roundtrip", func(t *testing.T) {
		s := newStore(t)

		m := memory.Memory{
			Layer:      memory.LayerFact,
			Domain:     "database",
			Created:    time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
			Source:     memory.SourceHuman,
			Confidence: memory.ConfidenceHigh,
			Tags:       []string{"schema", "performance"},
			Body:       "# Users table\n\nThe users table has 12M rows.\n",
		}

		path, err := s.Write(ctx, m)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		if path == "" {
			t.Fatal("Write returned empty path")
		}

		got, err := s.Read(ctx, path)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}

		if got.Layer != m.Layer {
			t.Errorf("Layer = %q, want %q", got.Layer, m.Layer)
		}
		if got.Domain != m.Domain {
			t.Errorf("Domain = %q, want %q", got.Domain, m.Domain)
		}
		if !got.Created.Equal(m.Created) {
			t.Errorf("Created = %v, want %v", got.Created, m.Created)
		}
		if got.Source != m.Source {
			t.Errorf("Source = %q, want %q", got.Source, m.Source)
		}
		if got.Confidence != m.Confidence {
			t.Errorf("Confidence = %q, want %q", got.Confidence, m.Confidence)
		}
		if len(got.Tags) != len(m.Tags) {
			t.Errorf("Tags = %v, want %v", got.Tags, m.Tags)
		} else {
			for i := range m.Tags {
				if got.Tags[i] != m.Tags[i] {
					t.Errorf("Tags[%d] = %q, want %q", i, got.Tags[i], m.Tags[i])
				}
			}
		}
		if got.Body != m.Body {
			t.Errorf("Body = %q, want %q", got.Body, m.Body)
		}
		if got.Path != path {
			t.Errorf("Path = %q, want %q", got.Path, path)
		}
	})

	t.Run("create_vs_update", func(t *testing.T) {
		s := newStore(t)

		m := memory.Memory{
			Layer:   memory.LayerDecision,
			Domain:  "api",
			Created: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			Body:    "# Original decision\n",
		}

		// Create (empty Path)
		path, err := s.Write(ctx, m)
		if err != nil {
			t.Fatalf("Write (create): %v", err)
		}

		// Update (set Path)
		m.Path = path
		m.Body = "# Updated decision\n"
		path2, err := s.Write(ctx, m)
		if err != nil {
			t.Fatalf("Write (update): %v", err)
		}
		if path2 != path {
			t.Errorf("Update returned different path: got %q, want %q", path2, path)
		}

		got, err := s.Read(ctx, path)
		if err != nil {
			t.Fatalf("Read after update: %v", err)
		}
		if got.Body != "# Updated decision\n" {
			t.Errorf("Body after update = %q, want %q", got.Body, "# Updated decision\n")
		}
	})

	t.Run("list_empty_store", func(t *testing.T) {
		s := newStore(t)

		got, err := s.List(ctx, store.Filter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if got == nil {
			t.Fatal("List returned nil, want empty slice")
		}
		if len(got) != 0 {
			t.Errorf("List returned %d items, want 0", len(got))
		}
	})

	t.Run("list_all", func(t *testing.T) {
		s := newStore(t)
		writeTestMemories(t, ctx, s)

		got, err := s.List(ctx, store.Filter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 3 {
			t.Errorf("List(no filter) returned %d items, want 3", len(got))
		}
	})

	t.Run("list_filter_by_layer", func(t *testing.T) {
		s := newStore(t)
		writeTestMemories(t, ctx, s)

		layer := memory.LayerFact
		got, err := s.List(ctx, store.Filter{Layer: &layer})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("List(layer=fact) returned %d items, want 2", len(got))
		}
		for _, m := range got {
			if m.Layer != memory.LayerFact {
				t.Errorf("List(layer=fact) returned memory with layer %q", m.Layer)
			}
		}
	})

	t.Run("list_filter_by_domain", func(t *testing.T) {
		s := newStore(t)
		writeTestMemories(t, ctx, s)

		domain := "api"
		got, err := s.List(ctx, store.Filter{Domain: &domain})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("List(domain=api) returned %d items, want 1", len(got))
		}
		if got[0].Domain != "api" {
			t.Errorf("List(domain=api) returned memory with domain %q", got[0].Domain)
		}
	})

	t.Run("list_filter_by_tags", func(t *testing.T) {
		s := newStore(t)
		writeTestMemories(t, ctx, s)

		got, err := s.List(ctx, store.Filter{Tags: []string{"schema"}})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("List(tags=schema) returned %d items, want 1", len(got))
		}
	})

	t.Run("list_filter_combined", func(t *testing.T) {
		s := newStore(t)
		writeTestMemories(t, ctx, s)

		layer := memory.LayerFact
		domain := "database"
		got, err := s.List(ctx, store.Filter{Layer: &layer, Domain: &domain})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("List(layer=fact, domain=database) returned %d items, want 1", len(got))
		}
	})

	t.Run("delete", func(t *testing.T) {
		s := newStore(t)

		m := memory.Memory{
			Layer:   memory.LayerLesson,
			Domain:  "testing",
			Created: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
			Body:    "# Test lesson\n",
		}

		path, err := s.Write(ctx, m)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}

		if err := s.Delete(ctx, path); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		_, err = s.Read(ctx, path)
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Read after Delete: got %v, want ErrNotFound", err)
		}
	})

	t.Run("read_nonexistent", func(t *testing.T) {
		s := newStore(t)

		_, err := s.Read(ctx, "nonexistent/path.md")
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Read nonexistent: got %v, want ErrNotFound", err)
		}
	})

	t.Run("delete_nonexistent", func(t *testing.T) {
		s := newStore(t)

		err := s.Delete(ctx, "nonexistent/path.md")
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Delete nonexistent: got %v, want ErrNotFound", err)
		}
	})

	t.Run("create_avoids_slug_collision", func(t *testing.T) {
		s := newStore(t)

		m := memory.Memory{
			Layer:   memory.LayerFact,
			Domain:  "database",
			Created: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
			Body:    "# Same title\n",
		}

		path1, err := s.Write(ctx, m)
		if err != nil {
			t.Fatalf("Write first: %v", err)
		}

		// Second write with empty Path (create) and identical content
		m.Path = ""
		path2, err := s.Write(ctx, m)
		if err != nil {
			t.Fatalf("Write second: %v", err)
		}

		if path1 == path2 {
			t.Errorf("two creates produced the same path %q — expected collision avoidance", path1)
		}

		// Both should be independently readable
		if _, err := s.Read(ctx, path1); err != nil {
			t.Errorf("Read first: %v", err)
		}
		if _, err := s.Read(ctx, path2); err != nil {
			t.Errorf("Read second: %v", err)
		}
	})
}

// writeTestMemories populates a store with a known set of memories for
// filter tests.
func writeTestMemories(t *testing.T, ctx context.Context, s store.Store) {
	t.Helper()

	memories := []memory.Memory{
		{
			Layer:   memory.LayerFact,
			Domain:  "database",
			Created: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
			Tags:    []string{"schema"},
			Body:    "# Users table\n",
		},
		{
			Layer:   memory.LayerFact,
			Domain:  "frontend",
			Created: time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC),
			Tags:    []string{"react"},
			Body:    "# React version\n",
		},
		{
			Layer:   memory.LayerDecision,
			Domain:  "api",
			Created: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			Tags:    []string{"conventions"},
			Body:    "# camelCase for API\n",
		},
	}

	for _, m := range memories {
		if _, err := s.Write(ctx, m); err != nil {
			t.Fatalf("writeTestMemories: %v", err)
		}
	}
}
