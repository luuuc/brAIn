package memory

import (
	"strings"
	"time"
)

// Layer identifies which of the five memory layers a memory belongs to.
type Layer string

const (
	LayerFact          Layer = "fact"
	LayerLesson        Layer = "lesson"
	LayerDecision      Layer = "decision"
	LayerEffectiveness Layer = "effectiveness"
	LayerCorrection    Layer = "correction"
)

// Valid reports whether l is a known memory layer.
func (l Layer) Valid() bool {
	switch l {
	case LayerFact, LayerLesson, LayerDecision,
		LayerEffectiveness, LayerCorrection:
		return true
	}
	return false
}

// Source identifies who created a memory.
type Source string

const (
	SourceHuman   Source = "human"
	SourceTool    Source = "tool"
	SourceRefresh Source = "refresh"
)

// Confidence represents the certainty level of a memory.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Memory is the unified type for all five layers. Layer-specific fields are
// optional — most are zero-valued for any given layer. This is a deliberate
// trade-off: one type keeps frontmatter parsing generic and the storage
// interface simple.
type Memory struct {
	// Path is the relative path within .brain/ (e.g. "facts/users-table.md").
	// Empty on creation; set by the storage adapter after write.
	Path string `yaml:"-"`

	// Required fields
	Layer   Layer     `yaml:"layer"`
	Domain  string    `yaml:"domain"`
	Created time.Time `yaml:"created"`

	// Optional common fields
	Updated       *time.Time  `yaml:"updated,omitempty"`
	Source        Source      `yaml:"source,omitempty"`
	Confidence    Confidence  `yaml:"confidence,omitempty"`
	Tags          []string    `yaml:"tags,omitempty,flow"`
	RevisitIf     string      `yaml:"revisit_if,omitempty"`
	Supersedes    string      `yaml:"supersedes,omitempty"`
	Retired       bool        `yaml:"retired,omitempty"`
	RetiredReason string      `yaml:"retired_reason,omitempty"`

	// Layer-specific: facts
	StaleAfter *time.Time `yaml:"stale_after,omitempty"`

	// Layer-specific: lessons
	StreakClean  int `yaml:"streak_clean,omitempty"`
	RetireAfter int `yaml:"retire_after,omitempty"`

	// Layer-specific: effectiveness
	// The outcome list in Body is the single source of truth; rates are
	// derived on read (see engine.parseOutcomes / computeStats).
	Persona string `yaml:"persona,omitempty"`

	// Layer-specific: corrections
	Immutable bool `yaml:"immutable,omitempty"`

	// Body is the markdown content below the frontmatter.
	Body string `yaml:"-"`
}

// Title returns a one-line label for the memory, for display.
//
// Most layers use the first line of the body, which is how the memory was
// written and therefore the best summary of it. Effectiveness is the
// exception: its body opens with the "## Outcomes" list, so its first line
// is a markdown heading that says nothing about the memory. Its identity is
// structured instead — a persona and a domain — so the title is built from
// those rather than parsed back out of prose.
//
// Deriving here rather than in each caller keeps recall, the MCP tools, and
// the session-start hook from drifting into three different answers for the
// same memory.
func (m Memory) Title() string {
	if m.Layer == LayerEffectiveness && m.Persona != "" {
		return m.Persona + " effectiveness in " + m.Domain
	}
	if i := strings.IndexByte(m.Body, '\n'); i >= 0 {
		return m.Body[:i]
	}
	return m.Body
}
