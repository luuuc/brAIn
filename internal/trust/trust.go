// Package trust implements the trust ladder: per-domain autonomy levels that
// evolve from outcomes. Clean outcomes promote, failures demote. The engine
// also ticks lesson retirement streaks as a side effect of recording outcomes.
package trust

import "time"

// Level is a per-domain autonomy level.
type Level string

const (
	LevelAsk      Level = "ask"
	LevelNotify   Level = "notify"
	LevelAutoShip Level = "auto_ship"
	LevelFullAuto Level = "full_auto"
)

// Valid reports whether l is a known trust level.
func (l Level) Valid() bool {
	switch l {
	case LevelAsk, LevelNotify, LevelAutoShip, LevelFullAuto:
		return true
	}
	return false
}

// Recommendation is what brAIn tells the caller to do with AI-produced work.
type Recommendation string

const (
	RecommendationEscalate   Recommendation = "escalate"
	RecommendationShipNotify Recommendation = "ship_notify"
	RecommendationShip       Recommendation = "ship"
)

// Outcome is the result of a ship: clean or failure.
type Outcome string

const (
	OutcomeClean   Outcome = "clean"
	OutcomeFailure Outcome = "failure"
)

// Valid reports whether o is a known outcome.
func (o Outcome) Valid() bool {
	switch o {
	case OutcomeClean, OutcomeFailure:
		return true
	}
	return false
}

// Promotion thresholds. Counter resets to zero at each level.
const (
	PromoteAskToNotify        = 10
	PromoteNotifyToAutoShip   = 30
	PromoteAutoShipToFullAuto = 100
)

// LessonRetireAfter is the default streak_clean threshold at which a lesson
// retires. Lessons may override via their retire_after field.
const LessonRetireAfter = 20

// SeenRefsCap bounds SeenRefs per domain. Oldest entries are evicted FIFO
// once the cap is hit. 500 refs covers years of CI traffic; the cap keeps
// the linear dedup scan and marshal cost predictable.
const SeenRefsCap = 500

// EventKind tags an entry in a domain's history.
type EventKind string

const (
	EventOutcome   EventKind = "outcome"
	EventPromotion EventKind = "promotion"
	EventDemotion  EventKind = "demotion"
	EventOverride  EventKind = "override"
)

// Event is one entry in a domain's append-only history.
type Event struct {
	At      time.Time `yaml:"at"`
	Kind    EventKind `yaml:"kind"`
	Outcome Outcome   `yaml:"outcome,omitempty"`
	From    Level     `yaml:"from,omitempty"`
	To      Level     `yaml:"to,omitempty"`
	Ref     string    `yaml:"ref,omitempty"`
	Reason  string    `yaml:"reason,omitempty"`
}

// SeenRef is a single (ref, outcome) pair used for ref-based dedup.
type SeenRef struct {
	Ref     string  `yaml:"ref"`
	Outcome Outcome `yaml:"outcome"`
}

// DomainState is the trust state for a single domain.
type DomainState struct {
	Level         Level      `yaml:"level"`
	CleanShips    int        `yaml:"clean_ships"`
	LastFailure   *time.Time `yaml:"last_failure,omitempty"`
	LastPromotion *time.Time `yaml:"last_promotion,omitempty"`
	// SeenRefs deduplicates outcome records by ref. FIFO order — oldest
	// first — so the head of the slice is what gets evicted when the
	// engine's cap is exceeded. Linear lookup is fine at 500 entries.
	SeenRefs []SeenRef `yaml:"seen_refs,omitempty"`
	History  []Event   `yaml:"history,omitempty"`
}

// SchemaVersion is the on-disk trust.yml schema version. Bumped on any
// breaking layout change. Repair refuses to auto-promote a backup whose
// schema doesn't match — silent schema drift is how you lose data.
//
// When bumping: add a read-side migration in readState (convert v{N-1} on
// load) and update Repair's schema-mismatch error message accordingly.
const SchemaVersion = 1

// State is the complete trust state: a map of domain name to DomainState.
type State struct {
	SchemaVersion int                     `yaml:"schema_version"`
	Domains       map[string]*DomainState `yaml:"domains"`
}

// schemaVersionOrDefault treats a zero SchemaVersion as 1 — the only
// layout shipped before the field existed. Centralised so future bumps
// change one call site, not every consumer.
func (s *State) schemaVersionOrDefault() int {
	if s.SchemaVersion == 0 {
		return 1
	}
	return s.SchemaVersion
}

// Get returns the domain state, creating an entry at LevelAsk if missing.
// The returned pointer is always safe to mutate; any zero fields are
// normalised (Level defaults to ask, SeenRefs map is initialised).
func (s *State) Get(domain string) *DomainState {
	if s.Domains == nil {
		s.Domains = make(map[string]*DomainState)
	}
	d, ok := s.Domains[domain]
	if !ok {
		d = &DomainState{Level: LevelAsk}
		s.Domains[domain] = d
	}
	if d.Level == "" {
		d.Level = LevelAsk
	}
	return d
}

// findRef returns the prior outcome for ref (and whether it was found).
// Linear scan — SeenRefs is capped at SeenRefsCap, so O(n) at n≤500.
func (d *DomainState) findRef(ref string) (Outcome, bool) {
	for _, sr := range d.SeenRefs {
		if sr.Ref == ref {
			return sr.Outcome, true
		}
	}
	return "", false
}

// Recommend maps a level plus urgency to a Recommendation. When hotfix is
// true, the floor is raised from escalate to ship_notify; higher levels are
// unaffected.
func Recommend(level Level, hotfix bool) Recommendation {
	switch level {
	case LevelNotify:
		return RecommendationShipNotify
	case LevelAutoShip, LevelFullAuto:
		return RecommendationShip
	}
	if hotfix {
		return RecommendationShipNotify
	}
	return RecommendationEscalate
}

// promoteThreshold returns the clean_ships count needed to promote out of level.
// Returns 0 for LevelFullAuto (no further promotion).
func promoteThreshold(level Level) int {
	switch level {
	case LevelAsk:
		return PromoteAskToNotify
	case LevelNotify:
		return PromoteNotifyToAutoShip
	case LevelAutoShip:
		return PromoteAutoShipToFullAuto
	}
	return 0
}

// nextLevel returns the next level up, or the same level if already at the top.
func nextLevel(level Level) Level {
	switch level {
	case LevelAsk:
		return LevelNotify
	case LevelNotify:
		return LevelAutoShip
	case LevelAutoShip:
		return LevelFullAuto
	}
	return level
}
