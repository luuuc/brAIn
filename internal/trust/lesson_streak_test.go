package trust

import (
	"context"
	"testing"
	"time"

	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
)

// seedLesson writes a lesson directly via the store so tests can control
// every field (domain, streak, retired, retire_after).
func seedLesson(t *testing.T, s store.Store, m memory.Memory) string {
	t.Helper()
	if m.Created.IsZero() {
		m.Created = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	m.Layer = memory.LayerLesson
	path, err := s.Write(context.Background(), m)
	if err != nil {
		t.Fatalf("seed lesson: %v", err)
	}
	return path
}

func TestLessonStreak_matchingDomainIncremented(t *testing.T) {
	eng, md, _ := newTestEngineWithStore(t)
	path := seedLesson(t, md, memory.Memory{
		Domain: "database",
		Body:   "# Migrations need a maintenance window\n",
	})

	_, err := eng.Record(context.Background(), "database", OutcomeClean, RecordOptions{})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	m, err := md.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("Read lesson: %v", err)
	}
	if m.StreakClean != 1 {
		t.Fatalf("streak_clean = %d, want 1", m.StreakClean)
	}
	if m.Retired {
		t.Fatal("lesson should not be retired yet")
	}
}

func TestLessonStreak_otherDomainUntouched(t *testing.T) {
	eng, md, _ := newTestEngineWithStore(t)
	path := seedLesson(t, md, memory.Memory{
		Domain: "frontend",
		Body:   "# Unrelated lesson\n",
	})
	_, err := eng.Record(context.Background(), "database", OutcomeClean, RecordOptions{})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	m, err := md.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if m.StreakClean != 0 {
		t.Fatalf("other-domain lesson streak = %d, want 0", m.StreakClean)
	}
}

func TestLessonStreak_retiresAtThreshold(t *testing.T) {
	eng, md, _ := newTestEngineWithStore(t)
	path := seedLesson(t, md, memory.Memory{
		Domain:      "database",
		Body:        "# Migration lesson\n",
		StreakClean: LessonRetireAfter - 1,
	})
	r, err := eng.Record(context.Background(), "database", OutcomeClean, RecordOptions{})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if r.LessonsRetired != 1 {
		t.Fatalf("LessonsRetired = %d, want 1", r.LessonsRetired)
	}
	m, err := md.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !m.Retired {
		t.Fatal("lesson should be retired after hitting threshold")
	}
	if m.StreakClean != LessonRetireAfter {
		t.Fatalf("streak_clean = %d, want %d", m.StreakClean, LessonRetireAfter)
	}
}

func TestLessonStreak_retiresAtCustomRetireAfter(t *testing.T) {
	eng, md, _ := newTestEngineWithStore(t)
	path := seedLesson(t, md, memory.Memory{
		Domain:      "database",
		Body:        "# Custom-threshold lesson\n",
		StreakClean: 4,
		RetireAfter: 5,
	})
	if _, err := eng.Record(context.Background(), "database", OutcomeClean, RecordOptions{}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	m, err := md.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !m.Retired {
		t.Fatal("expected retirement at custom threshold")
	}
}

func TestLessonStreak_alreadyRetiredNotIncremented(t *testing.T) {
	eng, md, _ := newTestEngineWithStore(t)
	path := seedLesson(t, md, memory.Memory{
		Domain:      "database",
		Body:        "# Already-retired lesson\n",
		StreakClean: 25,
		Retired:     true,
	})
	_, err := eng.Record(context.Background(), "database", OutcomeClean, RecordOptions{})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	m, err := md.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if m.StreakClean != 25 {
		t.Fatalf("retired-lesson streak changed: got %d, want 25", m.StreakClean)
	}
}

func TestLessonStreak_noLessonsNoOp(t *testing.T) {
	eng := newTestEngine(t)
	r, err := eng.Record(context.Background(), "database", OutcomeClean, RecordOptions{})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if r.LessonsTouched != 0 || r.LessonsRetired != 0 {
		t.Fatalf("expected no lesson activity, got touched=%d retired=%d", r.LessonsTouched, r.LessonsRetired)
	}
}

func TestLessonStreak_failureDoesNotTick(t *testing.T) {
	eng, md, _ := newTestEngineWithStore(t)
	path := seedLesson(t, md, memory.Memory{
		Domain: "database",
		Body:   "# Should not tick on failure\n",
	})
	_, err := eng.Record(context.Background(), "database", OutcomeFailure, RecordOptions{Reason: "broke"})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	m, err := md.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if m.StreakClean != 0 {
		t.Fatalf("streak_clean ticked on failure: got %d", m.StreakClean)
	}
}
