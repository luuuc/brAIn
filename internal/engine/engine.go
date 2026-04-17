package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
)

// RecallOptions controls what Recall returns.
type RecallOptions struct {
	Domain         string        // filter to a specific domain (empty = all)
	Query          string        // substring/keyword match against titles and tags
	Layer          *memory.Layer // filter to a specific layer (nil = all)
	Limit          int           // max results; 0 means no limit
	IncludeRetired bool          // include retired lessons (default false)

	// UseEffectiveness, when true and Domain is set, makes Recall load
	// the domain's effectiveness memories and rank memories with higher
	// persona acceptance rates above lower ones within the same layer.
	UseEffectiveness bool
}

// RememberResult is the return value of Remember.
type RememberResult struct {
	Path     string       // storage path of the written memory
	Layer    memory.Layer // layer (may have been auto-classified)
	Warnings []string     // non-fatal issues (e.g., superseded target not found)
}

// Engine sits between the storage adapter and the interfaces (CLI/MCP).
// It handles recall with ranking, staleness, retirement, classification,
// supersession, and effectiveness tracking.
type Engine struct {
	store store.Store
	now   func() time.Time

	// lockDir is the root under which the effectiveness advisory lock
	// lives. Empty means in-process-only serialisation (tests / callers
	// that don't need cross-process safety). When set, Track/EffectivenessStatsFor
	// flock <lockDir>/effectiveness/.lock.
	lockDir     string
	lockTimeout time.Duration

	// effMu serialises the effectiveness verbs in-process (Track,
	// EffectivenessStatsFor, loadEffectivenessScores). It does NOT
	// guard Remember/Recall/Forget — those remain lock-free. Held
	// across the flock wait is intentional at brain's scale. Operator
	// recovery on lock-timeout is in the error message itself (lock.go).
	effMu sync.Mutex
}

// Option customises Engine construction.
type Option func(*Engine)

// WithLockDir enables cross-process serialisation of effectiveness writes by
// placing an advisory lock at <dir>/effectiveness/.lock. Without this option
// the engine serialises only within the current process.
func WithLockDir(dir string) Option {
	return func(e *Engine) { e.lockDir = dir }
}

// WithLockTimeout overrides the advisory-lock acquisition ceiling. Intended
// for tests; production uses defaultEngineLockTimeout.
func WithLockTimeout(d time.Duration) Option {
	return func(e *Engine) { e.lockTimeout = d }
}

// WithClock overrides the engine's clock. Intended for tests that need
// deterministic "now" values; production uses time.Now.
func WithClock(clock func() time.Time) Option {
	return func(e *Engine) { e.now = clock }
}

// NewEngine creates a MemoryEngine backed by the given store.
func NewEngine(_ context.Context, s store.Store, opts ...Option) (*Engine, error) {
	if s == nil {
		return nil, fmt.Errorf("engine: store must not be nil")
	}
	e := &Engine{
		store:       s,
		now:         time.Now,
		lockTimeout: defaultEngineLockTimeout,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

// Remember writes a memory to the store. If no layer is set, it classifies
// the layer from content. If the memory specifies Supersedes, the target
// is marked as retired.
func (e *Engine) Remember(ctx context.Context, m memory.Memory) (RememberResult, error) {
	if m.Domain == "" {
		return RememberResult{}, errors.New("engine: remember: domain must not be empty")
	}
	if m.Created.IsZero() {
		return RememberResult{}, errors.New("engine: remember: created must not be zero")
	}

	if m.Layer == "" {
		m.Layer = ClassifyLayer(m.Body)
	}

	path, err := e.store.Write(ctx, m)
	if err != nil {
		return RememberResult{}, fmt.Errorf("engine: remember: %w", err)
	}

	var warnings []string

	if m.Supersedes != "" {
		if err := e.retire(ctx, m.Supersedes, "superseded"); err != nil {
			warnings = append(warnings, fmt.Sprintf("could not retire superseded memory %q: %v", m.Supersedes, err))
		}
	}

	return RememberResult{Path: path, Layer: m.Layer, Warnings: warnings}, nil
}

// Recall returns memories matching the given options, ranked by authority
// hierarchy and recency.
func (e *Engine) Recall(ctx context.Context, opts RecallOptions) ([]memory.Memory, error) {
	f := store.Filter{}
	if opts.Layer != nil {
		f.Layer = opts.Layer
	}
	if opts.Domain != "" {
		f.Domain = &opts.Domain
	}

	all, err := e.store.List(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("engine: recall: %w", err)
	}

	if opts.Query != "" {
		all = filterByQuery(all, opts.Query)
	}

	var scores map[string]float64
	if opts.UseEffectiveness && opts.Domain != "" {
		scores, err = e.loadEffectivenessScores(ctx, opts.Domain)
		if err != nil {
			return nil, fmt.Errorf("engine: recall: %w", err)
		}
	}

	ranked := Rank(all, RankOptions{
		Now:                 e.now(),
		Limit:               opts.Limit,
		IncludeRetired:      opts.IncludeRetired,
		EffectivenessScores: scores,
	})

	return ranked, nil
}

// loadEffectivenessScores builds a persona → acceptance-rate map for the
// given domain by reading all effectiveness files for that domain. Takes a
// shared lock so it never sees a Track mid-write.
func (e *Engine) loadEffectivenessScores(ctx context.Context, domain string) (map[string]float64, error) {
	lock, err := e.acquireEffectivenessLock(ctx, lockShared)
	if err != nil {
		return nil, err
	}
	defer lock.releaseAndLog()

	layer := memory.LayerEffectiveness
	filter := store.Filter{Layer: &layer, Domain: &domain}
	list, err := e.store.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	now := e.now()
	scores := make(map[string]float64, len(list))
	for _, m := range list {
		if m.Persona == "" {
			continue
		}
		entries := loadOutcomes(m.Body).entries()
		stats := computeStats(entries, now, effectivenessWindowDays)
		scores[m.Persona] = stats.AcceptanceRate
	}
	return scores, nil
}

// Forget marks a memory as retired without deleting the file.
// An optional reason is stored in the memory's RetiredReason field.
func (e *Engine) Forget(ctx context.Context, path, reason string) error {
	return e.retire(ctx, path, reason)
}

// retire reads a memory, sets Retired=true, and writes it back.
func (e *Engine) retire(ctx context.Context, path, reason string) error {
	m, err := e.store.Read(ctx, path)
	if err != nil {
		return fmt.Errorf("engine: retire %q: %w", path, err)
	}
	m.Retired = true
	m.RetiredReason = reason
	now := e.now()
	m.Updated = &now
	if _, err := e.store.Write(ctx, m); err != nil {
		return fmt.Errorf("engine: retire %q: %w", path, err)
	}
	return nil
}

// filterByQuery returns memories whose title (first line of body) or tags
// contain the query as a case-insensitive substring.
func filterByQuery(memories []memory.Memory, query string) []memory.Memory {
	q := strings.ToLower(query)
	var out []memory.Memory
	for _, m := range memories {
		if matchesQuery(m, q) {
			out = append(out, m)
		}
	}
	return out
}

// matchesQuery checks if a memory's title or tags contain the query substring.
func matchesQuery(m memory.Memory, q string) bool {
	title := firstLine(m.Body)
	if strings.Contains(strings.ToLower(title), q) {
		return true
	}
	for _, tag := range m.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
