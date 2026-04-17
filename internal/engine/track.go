package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
)

// bodyAppender is implemented by storage adapters that support appending
// to an existing memory's body without rewriting the frontmatter. The
// markdown adapter satisfies it (see AppendBody). Adapters that don't
// implement it fall back to full read-modify-write.
type bodyAppender interface {
	AppendBody(ctx context.Context, path, content string) error
}

// Track records a persona outcome for a domain.
//
// Concurrent Track calls are serialised by an advisory file lock so two
// writers never race-rewrite the log. On the first call for a given
// persona+domain the full file is rendered; subsequent calls append a
// single line via the storage adapter's fast path when available, or
// fall back to read-modify-write otherwise. Stats are always derived
// from the full outcome list after the append.
func (e *Engine) Track(ctx context.Context, persona, domain string, outcome Outcome, reason string) (EffectivenessStats, error) {
	persona = strings.ToLower(strings.TrimSpace(persona))
	domain = strings.ToLower(strings.TrimSpace(domain))
	if err := validateTrackArgs(persona, domain, outcome); err != nil {
		return EffectivenessStats{}, err
	}

	lock, err := e.acquireEffectivenessLock(ctx, lockExclusive)
	if err != nil {
		return EffectivenessStats{}, err
	}
	defer lock.releaseAndLog()

	path := effectivenessPath(persona, domain)
	now := e.now()
	entry := outcomeEntry{
		Date:    dateOf(now),
		Outcome: outcome,
		Reason:  strings.TrimSpace(reason),
	}

	m, readErr := e.store.Read(ctx, path)
	existing := readErr == nil
	switch {
	case existing:
		// File already exists — fall through to append path.
	case errors.Is(readErr, store.ErrNotFound):
		// First outcome for this persona+domain — build the full file.
	default:
		return EffectivenessStats{}, fmt.Errorf("engine: track: %w", readErr)
	}

	if err := ctx.Err(); err != nil {
		return EffectivenessStats{}, err
	}

	var sec *outcomesSection
	if existing {
		sec = loadOutcomes(m.Body)
	} else {
		sec = &outcomesSection{}
	}
	sec.append(entry)

	appender, canAppend := e.store.(bodyAppender)
	if existing && canAppend {
		// Fast path: write only the new line via O_APPEND. The
		// frontmatter's `updated` timestamp is intentionally NOT
		// refreshed — keeping the write truly append-only is worth
		// slightly stale metadata. Operators reading the file get the
		// accurate creation date; the authoritative "last recorded"
		// signal lives in the outcome list.
		if err := appender.AppendBody(ctx, path, formatOutcomeLine(entry)+"\n"); err != nil {
			return EffectivenessStats{}, fmt.Errorf("engine: track: %w", err)
		}
	} else {
		if !existing {
			m = memory.Memory{
				Path:    path,
				Layer:   memory.LayerEffectiveness,
				Domain:  domain,
				Persona: persona,
				Created: now,
				Source:  memory.SourceTool,
			}
		}
		m.Body = sec.render()
		m.Updated = &now
		if _, err := e.store.Write(ctx, m); err != nil {
			return EffectivenessStats{}, fmt.Errorf("engine: track: %w", err)
		}
	}

	stats := computeStats(sec.entries(), now, effectivenessWindowDays)
	stats.Persona = persona
	stats.Domain = domain
	return stats, nil
}

// EffectivenessStatsFor returns the rolling-window stats for a persona in a
// domain without recording a new outcome. Returns ErrNotFound if the file
// doesn't exist yet — callers can treat that as "no stats."
func (e *Engine) EffectivenessStatsFor(ctx context.Context, persona, domain string) (EffectivenessStats, error) {
	persona = strings.ToLower(strings.TrimSpace(persona))
	domain = strings.ToLower(strings.TrimSpace(domain))
	if !isSafeSlug(persona) {
		return EffectivenessStats{}, fmt.Errorf("%w: invalid persona %q", ErrInvalidArgs, persona)
	}
	if !isSafeSlug(domain) {
		return EffectivenessStats{}, fmt.Errorf("%w: invalid domain %q", ErrInvalidArgs, domain)
	}

	lock, err := e.acquireEffectivenessLock(ctx, lockShared)
	if err != nil {
		return EffectivenessStats{}, err
	}
	defer lock.releaseAndLog()

	m, err := e.store.Read(ctx, effectivenessPath(persona, domain))
	if err != nil {
		return EffectivenessStats{}, err
	}
	entries := loadOutcomes(m.Body).entries()
	stats := computeStats(entries, e.now(), effectivenessWindowDays)
	stats.Persona = persona
	stats.Domain = domain
	return stats, nil
}

// dateOf truncates t to its UTC date so repeated same-day records compare
// stably regardless of sub-second drift.
func dateOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
