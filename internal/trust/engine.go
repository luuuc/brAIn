package trust

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/luuuc/brain/internal/memory"
	"github.com/luuuc/brain/internal/store"
)

// lockTimeoutEnvVar names the env var that overrides the advisory-lock
// acquisition timeout. Value is milliseconds (e.g. "150"). Intended for
// tests and occasional operator debugging — production should leave it
// unset and use the default (5 s). The name is documented to operators
// via trustLongHelp in the CLI and the "Environment variables" table in
// .doc/definition/06-mcp-and-cli.md — not as a Go symbol.
const lockTimeoutEnvVar = "BRAIN_TRUST_LOCK_TIMEOUT_MS"

// defaultLockTimeout is the advisory-lock acquisition ceiling when no
// option overrides it. Writes are fast; 5 s is generous.
const defaultLockTimeout = 5 * time.Second

// Engine is the trust engine. It reads and writes trust state and ticks
// lesson retirement streaks as a side effect of recording clean outcomes.
type Engine struct {
	dir         string
	store       store.Store
	now         func() time.Time
	lockTimeout time.Duration
	seenRefsCap int
	syncWrites  bool // if true, writeState fsyncs the tmp file and parent dir for crash durability
}

// Option configures an Engine at construction.
type Option func(*Engine)

// WithLockTimeout overrides the advisory-lock acquisition timeout. Tests
// use this to exercise lock-conflict paths without waiting seconds.
func WithLockTimeout(d time.Duration) Option {
	return func(e *Engine) { e.lockTimeout = d }
}

// WithLockTimeoutFromEnv reads lockTimeoutEnvVar (in milliseconds) and
// applies it as WithLockTimeout if set and valid. An unset env var is a
// silent no-op; malformed or non-positive values emit a slog.Warn so a
// typo doesn't turn into "my override isn't working" at 3am. Callers pass
// this unconditionally during construction.
func WithLockTimeoutFromEnv() Option {
	return func(e *Engine) {
		raw := os.Getenv(lockTimeoutEnvVar)
		if raw == "" {
			return
		}
		ms, err := strconv.Atoi(raw)
		if err != nil {
			slog.Warn("trust: "+lockTimeoutEnvVar+" not an integer; using default", "value", raw, "err", err)
			return
		}
		if ms <= 0 {
			slog.Warn("trust: "+lockTimeoutEnvVar+" must be positive milliseconds; using default", "value", raw)
			return
		}
		e.lockTimeout = time.Duration(ms) * time.Millisecond
	}
}

// WithSeenRefsCap overrides the per-domain SeenRefs FIFO cap. Tests use
// this to exercise eviction without writing hundreds of records. Non-
// positive values are refused with a slog.Warn; the engine keeps its
// default cap.
func WithSeenRefsCap(n int) Option {
	return func(e *Engine) {
		if n <= 0 {
			slog.Warn("trust: WithSeenRefsCap requires n > 0; keeping default", "n", n, "default", e.seenRefsCap)
			return
		}
		e.seenRefsCap = n
	}
}

// NewEngine creates a trust engine rooted at trustDir (typically
// ".brain/trust"). The store is used to read and update lesson memories when
// a clean outcome is recorded — the coupling is intentional and visible in
// the constructor signature.
func NewEngine(_ context.Context, trustDir string, s store.Store, opts ...Option) (*Engine, error) {
	if trustDir == "" {
		return nil, errors.New("trust: trustDir must not be empty")
	}
	if s == nil {
		return nil, errors.New("trust: store must not be nil")
	}
	e := &Engine{
		dir:         trustDir,
		store:       s,
		now:         time.Now,
		lockTimeout: defaultLockTimeout,
		seenRefsCap: SeenRefsCap,
		syncWrites:  true,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e, nil
}

// Decision is the result of Check.
type Decision struct {
	Domain         string
	Level          Level
	CleanShips     int
	LastFailure    *time.Time
	LastPromotion  *time.Time
	Hotfix         bool
	Recommendation Recommendation
	History        []Event
}

// CheckOptions tunes Check.
type CheckOptions struct {
	// Hotfix raises the floor from escalate to ship_notify.
	Hotfix bool
}

// Check reports the trust level and recommendation for a domain. An unknown
// domain returns a zero-state Decision at LevelAsk — every domain starts at
// ask until proven. Readers take a shared lock so they see a consistent
// snapshot of an in-flight writer.
func (e *Engine) Check(ctx context.Context, domain string, opts CheckOptions) (Decision, error) {
	if domain == "" {
		return Decision{}, errors.New("trust: check: domain must not be empty")
	}
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	lock, err := acquireLock(ctx, e.dir, e.lockTimeout, lockShared)
	if err != nil {
		return Decision{}, err
	}
	defer lock.releaseAndLog()

	s, err := readState(e.dir)
	if err != nil {
		return Decision{}, err
	}
	d := s.Get(domain)
	return e.decisionFor(domain, d, opts.Hotfix), nil
}

// List returns decisions for every known domain, sorted by domain name.
func (e *Engine) List(ctx context.Context) ([]Decision, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lock, err := acquireLock(ctx, e.dir, e.lockTimeout, lockShared)
	if err != nil {
		return nil, err
	}
	defer lock.releaseAndLog()

	s, err := readState(e.dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(s.Domains))
	for n := range s.Domains {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Decision, 0, len(names))
	for _, n := range names {
		out = append(out, e.decisionFor(n, s.Get(n), false))
	}
	return out, nil
}

// RecordOptions tunes Record.
type RecordOptions struct {
	Ref    string
	Reason string
}

// RecordResult reports what happened when Record was called.
type RecordResult struct {
	Decision       Decision
	Promoted       bool
	Demoted        bool
	Deduplicated   bool // true if the outcome was silently dropped due to a seen ref
	EvictedRefs    int  // number of seen-refs evicted FIFO to make room for this one
	LessonsTouched int  // number of lessons incremented (clean outcomes only)
	LessonsRetired int  // number of lessons retired as a result of this record
}

// Record applies an outcome to a domain and returns the resulting decision.
// Clean outcomes may promote; any failure immediately demotes to ask.
// On clean outcomes, lessons in the same domain also tick toward retirement.
//
// Ordering is deliberate: trust state writes first, lesson ticks second.
// A crash between the two steps leaves the trust state committed and the
// lesson streaks slightly stale — at worst one tick gets replayed on the
// next clean outcome. The reverse ordering would let lessons get ticked
// from an outcome that never persists into the counter, and a next clean
// outcome would double-tick the same lessons.
func (e *Engine) Record(ctx context.Context, domain string, outcome Outcome, opts RecordOptions) (RecordResult, error) {
	if domain == "" {
		return RecordResult{}, errors.New("trust: record: domain must not be empty")
	}
	if !outcome.Valid() {
		return RecordResult{}, fmt.Errorf("trust: record: invalid outcome %q", outcome)
	}
	if err := ctx.Err(); err != nil {
		return RecordResult{}, err
	}

	lock, err := acquireLock(ctx, e.dir, e.lockTimeout, lockExclusive)
	if err != nil {
		return RecordResult{}, err
	}
	defer lock.releaseAndLog()

	s, err := readState(e.dir)
	if err != nil {
		return RecordResult{}, err
	}

	d := s.Get(domain)

	// Dedup by ref. A matching prior outcome is silently ignored if outcomes
	// agree; rejected with an error if they disagree.
	if opts.Ref != "" {
		if prior, ok := d.findRef(opts.Ref); ok {
			if prior == outcome {
				return RecordResult{Decision: e.decisionFor(domain, d, false), Deduplicated: true}, nil
			}
			return RecordResult{}, fmt.Errorf("trust: record: %w: ref %q already recorded with outcome %q", store.ErrConflict, opts.Ref, prior)
		}
	}

	now := e.now()
	res := RecordResult{}

	switch outcome {
	case OutcomeClean:
		d.CleanShips++
		d.History = append(d.History, Event{
			At: now, Kind: EventOutcome, Outcome: OutcomeClean,
			Ref: opts.Ref, Reason: opts.Reason,
		})
		if threshold := promoteThreshold(d.Level); threshold > 0 && d.CleanShips >= threshold {
			from := d.Level
			d.Level = nextLevel(from)
			d.CleanShips = 0
			promotedAt := now
			d.LastPromotion = &promotedAt
			d.History = append(d.History, Event{At: now, Kind: EventPromotion, From: from, To: d.Level})
			res.Promoted = true
		}

	case OutcomeFailure:
		from := d.Level
		d.CleanShips = 0
		d.Level = LevelAsk
		failedAt := now
		d.LastFailure = &failedAt
		d.History = append(d.History, Event{
			At: now, Kind: EventOutcome, Outcome: OutcomeFailure,
			Ref: opts.Ref, Reason: opts.Reason,
		})
		if from != LevelAsk {
			d.History = append(d.History, Event{At: now, Kind: EventDemotion, From: from, To: LevelAsk, Reason: opts.Reason})
			res.Demoted = true
		}
	}

	if opts.Ref != "" {
		res.EvictedRefs = e.rememberRef(d, opts.Ref, outcome)
	}

	if err := writeState(e.dir, s, e.syncWrites); err != nil {
		return RecordResult{}, err
	}

	// Lesson coupling only after trust state is durable. A failure here
	// leaves the counter correct; at worst we lose a streak tick, which is
	// recoverable the next time the domain goes clean.
	if outcome == OutcomeClean {
		touched, retired, lerr := e.tickLessons(ctx, domain, now)
		res.LessonsTouched = touched
		res.LessonsRetired = retired
		if lerr != nil {
			res.Decision = e.decisionFor(domain, d, false)
			return res, fmt.Errorf("trust: record: state committed but lesson tick failed: %w", lerr)
		}
	}

	res.Decision = e.decisionFor(domain, d, false)
	return res, nil
}

// rememberRef appends (ref, outcome) to the domain's SeenRefs, evicting
// the oldest entries FIFO once the engine's configured cap is exceeded.
// Returns the number of entries evicted to make room — surfaced as
// RecordResult.EvictedRefs so the FIFO is auditable.
func (e *Engine) rememberRef(d *DomainState, ref string, outcome Outcome) (evicted int) {
	d.SeenRefs = append(d.SeenRefs, SeenRef{Ref: ref, Outcome: outcome})
	for len(d.SeenRefs) > e.seenRefsCap {
		d.SeenRefs = d.SeenRefs[1:]
		evicted++
	}
	return evicted
}

// decisionFor builds a Decision snapshot from a DomainState. History is
// returned as a fresh slice so callers can't mutate the state's underlying
// buffer.
func (e *Engine) decisionFor(domain string, d *DomainState, hotfix bool) Decision {
	hist := make([]Event, len(d.History))
	copy(hist, d.History)
	return Decision{
		Domain:         domain,
		Level:          d.Level,
		CleanShips:     d.CleanShips,
		LastFailure:    d.LastFailure,
		LastPromotion:  d.LastPromotion,
		Hotfix:         hotfix,
		Recommendation: Recommend(d.Level, hotfix),
		History:        hist,
	}
}

// tickLessons increments streak_clean on every non-retired lesson in the
// domain. Lessons whose streak reaches their retire_after (defaulting to
// LessonRetireAfter) are marked retired. Returns (touched, retired, error).
// Called only after trust state has been written — see Record.
func (e *Engine) tickLessons(ctx context.Context, domain string, now time.Time) (int, int, error) {
	layer := memory.LayerLesson
	lessons, err := e.store.List(ctx, store.Filter{Layer: &layer, Domain: &domain})
	if err != nil {
		return 0, 0, fmt.Errorf("list lessons: %w", err)
	}
	touched, retired := 0, 0
	for _, m := range lessons {
		if m.Retired {
			continue
		}
		m.StreakClean++
		threshold := m.RetireAfter
		if threshold <= 0 {
			threshold = LessonRetireAfter
		}
		isRetiring := m.StreakClean >= threshold
		if isRetiring {
			m.Retired = true
			m.RetiredReason = "streak_clean reached retire_after"
		}
		updated := now
		m.Updated = &updated
		// Counters bump only after the Write commits, so a mid-loop
		// failure never overcounts relative to what's actually on disk.
		if _, err := e.store.Write(ctx, m); err != nil {
			return touched, retired, fmt.Errorf("update lesson %s: %w", m.Path, err)
		}
		touched++
		if isRetiring {
			retired++
		}
	}
	return touched, retired, nil
}

// Override records a human override for a domain. It writes the trust
// state update first, then writes a correction memory to the store. The
// ordering mirrors Record: state is the source of truth; the correction
// memory is a side effect that can be retried.
func (e *Engine) Override(ctx context.Context, domain, reason string) (Decision, error) {
	if domain == "" {
		return Decision{}, errors.New("trust: override: domain must not be empty")
	}
	if reason == "" {
		return Decision{}, errors.New("trust: override: reason must not be empty")
	}
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}

	lock, err := acquireLock(ctx, e.dir, e.lockTimeout, lockExclusive)
	if err != nil {
		return Decision{}, err
	}
	defer lock.releaseAndLog()

	s, err := readState(e.dir)
	if err != nil {
		return Decision{}, err
	}
	d := s.Get(domain)
	now := e.now()
	d.History = append(d.History, Event{At: now, Kind: EventOverride, Reason: reason})
	if err := writeState(e.dir, s, e.syncWrites); err != nil {
		return Decision{}, err
	}

	corr := memory.Memory{
		Layer:      memory.LayerCorrection,
		Domain:     domain,
		Created:    now,
		Source:     memory.SourceHuman,
		Confidence: memory.ConfidenceHigh,
		Immutable:  true,
		Body:       reason,
	}
	if _, err := e.store.Write(ctx, corr); err != nil {
		return e.decisionFor(domain, d, false), fmt.Errorf("trust: override: state committed but correction write failed: %w", err)
	}
	return e.decisionFor(domain, d, false), nil
}
