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

// RecallOptions controls what Recall returns.
type RecallOptions struct {
	Domain         string            // filter to a specific domain (empty = all)
	Query          string            // substring/keyword match against titles and tags
	Layer          *memory.Layer     // filter to a specific layer (nil = all)
	Limit          int               // max results; 0 means no limit
	IncludeRetired bool              // include retired lessons (default false)

	// Placeholder for pitch 01-06 effectiveness-adjusted ranking.
	EffectivenessScores map[string]float64
}

// RememberResult is the return value of Remember.
type RememberResult struct {
	Path     string       // storage path of the written memory
	Layer    memory.Layer // layer (may have been auto-classified)
	Warnings []string     // non-fatal issues (e.g., superseded target not found)
}

// Engine sits between the storage adapter and the interfaces (CLI/MCP).
// It handles recall with ranking, staleness, retirement, classification,
// and supersession.
type Engine struct {
	store store.Store
	now   func() time.Time
}

// NewEngine creates a MemoryEngine backed by the given store.
func NewEngine(_ context.Context, s store.Store) (*Engine, error) {
	if s == nil {
		return nil, fmt.Errorf("engine: store must not be nil")
	}
	return &Engine{store: s, now: time.Now}, nil
}

// Remember writes a memory to the store. If no layer is set, it classifies
// the layer from content. If the memory specifies Supersedes, the target
// is marked as retired.
func (e *Engine) Remember(ctx context.Context, m memory.Memory) (RememberResult, error) {
	// 0. Validate required fields.
	if m.Domain == "" {
		return RememberResult{}, errors.New("engine: remember: domain must not be empty")
	}
	if m.Created.IsZero() {
		return RememberResult{}, errors.New("engine: remember: created must not be zero")
	}

	// 1. Classify layer if not set.
	if m.Layer == "" {
		m.Layer = ClassifyLayer(m.Body)
	}

	// 2. Write the new memory.
	path, err := e.store.Write(ctx, m)
	if err != nil {
		return RememberResult{}, fmt.Errorf("engine: remember: %w", err)
	}

	var warnings []string

	// 3. If supersedes is set, retire the target.
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
	// Build store filter from options.
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

	// Query matching: substring match against body first line (title) and tags.
	if opts.Query != "" {
		all = filterByQuery(all, opts.Query)
	}

	// Rank. Limit <= 0 means no limit (return all).
	ranked := Rank(all, RankOptions{
		Now:                 e.now(),
		Limit:               opts.Limit,
		IncludeRetired:      opts.IncludeRetired,
		EffectivenessScores: opts.EffectivenessScores,
	})

	return ranked, nil
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
		return fmt.Errorf("retire %q: %w", path, err)
	}
	m.Retired = true
	m.RetiredReason = reason
	now := e.now()
	m.Updated = &now
	if _, err := e.store.Write(ctx, m); err != nil {
		return fmt.Errorf("retire %q: %w", path, err)
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
	// Check first line of body (title).
	title := firstLine(m.Body)
	if strings.Contains(strings.ToLower(title), q) {
		return true
	}
	// Check tags.
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
