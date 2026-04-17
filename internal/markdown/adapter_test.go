package markdown_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/brain/internal/markdown"
	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
	"github.com/luuuc/brain/internal/storetest"
)

func TestConformance(t *testing.T) {
	storetest.TestStore(t, func(t *testing.T) store.Store {
		t.Helper()
		return markdown.New(t.TempDir())
	})
}

func TestDirectoryAutoCreation(t *testing.T) {
	root := t.TempDir()
	a := markdown.New(root)
	ctx := context.Background()

	m := memory.Memory{
		Layer:   memory.LayerFact,
		Domain:  "database",
		Created: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
		Body:    "# Test\n",
	}

	path, err := a.Write(ctx, m)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Verify the layer subdirectory was created
	dir := filepath.Dir(filepath.Join(root, path))
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat layer dir: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("layer dir is not a directory")
	}
}

func TestAtomicWriteSafety(t *testing.T) {
	root := t.TempDir()
	a := markdown.New(root)
	ctx := context.Background()

	m := memory.Memory{
		Layer:   memory.LayerDecision,
		Domain:  "api",
		Created: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Body:    "# Decision\n",
	}

	path, err := a.Write(ctx, m)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Verify no .tmp files remain
	dir := filepath.Dir(filepath.Join(root, path))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp" {
			t.Errorf("temp file left behind: %s", entry.Name())
		}
	}
}

func TestFrontmatterEdgeCases(t *testing.T) {
	root := t.TempDir()
	a := markdown.New(root)
	ctx := context.Background()

	t.Run("body_containing_triple_dashes", func(t *testing.T) {
		m := memory.Memory{
			Layer:   memory.LayerLesson,
			Domain:  "testing",
			Created: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
			Body:    "# Lesson\n\nSome content\n\n---\n\nMore content after horizontal rule\n",
		}

		path, err := a.Write(ctx, m)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}

		got, err := a.Read(ctx, path)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.Body != m.Body {
			t.Errorf("Body = %q, want %q", got.Body, m.Body)
		}
	})

	t.Run("empty_body", func(t *testing.T) {
		m := memory.Memory{
			Layer:   memory.LayerFact,
			Domain:  "deploy",
			Created: time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC),
			Body:    "",
		}

		path, err := a.Write(ctx, m)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}

		got, err := a.Read(ctx, path)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.Body != "" {
			t.Errorf("Body = %q, want empty", got.Body)
		}
	})
}

func TestCorrectionDatePrefix(t *testing.T) {
	root := t.TempDir()
	a := markdown.New(root)
	ctx := context.Background()

	m := memory.Memory{
		Layer:     memory.LayerCorrection,
		Domain:    "testing",
		Created:   time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Immutable: true,
		Body:      "# No mocking integration tests\n",
	}

	path, err := a.Write(ctx, m)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	filename := filepath.Base(path)
	if !hasDatePrefix(filename, "2026-04-01") {
		t.Errorf("correction filename %q does not have date prefix 2026-04-01", filename)
	}
}

func hasDatePrefix(filename, date string) bool {
	return len(filename) > len(date) && filename[:len(date)] == date
}

func TestLayerSpecificFields(t *testing.T) {
	root := t.TempDir()
	a := markdown.New(root)
	ctx := context.Background()

	t.Run("fact_stale_after", func(t *testing.T) {
		staleAfter := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
		m := memory.Memory{
			Layer:      memory.LayerFact,
			Domain:     "database",
			Created:    time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
			StaleAfter: &staleAfter,
			Body:       "# Fact with staleness\n",
		}

		path, err := a.Write(ctx, m)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}

		got, err := a.Read(ctx, path)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.StaleAfter == nil {
			t.Fatal("StaleAfter is nil, want non-nil")
		}
		if !got.StaleAfter.Equal(staleAfter) {
			t.Errorf("StaleAfter = %v, want %v", *got.StaleAfter, staleAfter)
		}
	})

	t.Run("lesson_retirement", func(t *testing.T) {
		m := memory.Memory{
			Layer:       memory.LayerLesson,
			Domain:      "payments",
			Created:     time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
			StreakClean:  7,
			RetireAfter:  20,
			Retired:      false,
			Body:         "# Payments race condition\n",
		}

		path, err := a.Write(ctx, m)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}

		got, err := a.Read(ctx, path)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.StreakClean != 7 {
			t.Errorf("StreakClean = %d, want 7", got.StreakClean)
		}
		if got.RetireAfter != 20 {
			t.Errorf("RetireAfter = %d, want 20", got.RetireAfter)
		}
	})

	t.Run("effectiveness_persona", func(t *testing.T) {
		// Pitch 01-06 moved counters out of frontmatter — the outcome list
		// in the body is the single source of truth. The adapter just
		// round-trips persona metadata.
		m := memory.Memory{
			Layer:   memory.LayerEffectiveness,
			Domain:  "testing",
			Created: time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC),
			Persona: "kent-beck",
			Body: "# Kent Beck effectiveness in testing\n\n" +
				"## Outcomes\n" +
				"- 2026-04-14: accepted — PR #52\n" +
				"- 2026-04-10: accepted\n" +
				"- 2026-04-03: overridden — false positive on STI model tests\n",
		}

		path, err := a.Write(ctx, m)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}

		got, err := a.Read(ctx, path)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.Persona != "kent-beck" {
			t.Errorf("Persona = %q, want kent-beck", got.Persona)
		}
		if !strings.Contains(got.Body, "## Outcomes") {
			t.Errorf("Body missing ## Outcomes section: %q", got.Body)
		}
		if !strings.Contains(got.Body, "2026-04-14: accepted") {
			t.Errorf("Body missing first outcome line: %q", got.Body)
		}
	})

	t.Run("correction_immutable", func(t *testing.T) {
		m := memory.Memory{
			Layer:     memory.LayerCorrection,
			Domain:    "testing",
			Created:   time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			Immutable: true,
			Body:      "# Correction\n",
		}

		path, err := a.Write(ctx, m)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}

		got, err := a.Read(ctx, path)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if !got.Immutable {
			t.Error("Immutable = false, want true")
		}
	})
}
