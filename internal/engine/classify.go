package engine

import (
	"strings"

	"github.com/luuuc/brain/internal/memory"
)

// ClassifyLayer attempts to determine the memory layer from content signals.
// This is best-effort — explicit layer always takes precedence. Returns
// LayerFact as the default when no strong signal is found.
func ClassifyLayer(content string) memory.Layer {
	lower := strings.ToLower(content)

	// Correction signals — strongest, check first.
	if containsAny(lower, correctionSignals) {
		return memory.LayerCorrection
	}

	// Decision signals.
	if containsAny(lower, decisionSignals) {
		return memory.LayerDecision
	}

	// Lesson signals.
	if containsAny(lower, lessonSignals) {
		return memory.LayerLesson
	}

	// Default to fact.
	return memory.LayerFact
}

// Corrections are owner overrides: the owner telling the tool that what it
// did was wrong. The signals are deliberately about the tool's behaviour
// rather than about the codebase.
//
// Bare prohibitions ("never ...", "always ...", "don't ...") are absent on
// purpose. They read as rules drawn from something that happened, which is a
// lesson, and the two layers have opposite lifetimes: a lesson retires after
// a clean streak, a correction is permanent and immutable. Filing a lesson
// here makes a rule that can never be forgotten — the failure brAIn exists to
// avoid — so an ambiguous prohibition falls through to lessonSignals below.
var correctionSignals = []string{
	"stop ",
	"override",
	"wrong",
	"incorrect",
	"don't flag",
	"do not flag",
	"don't suggest",
	"do not suggest",
}

var decisionSignals = []string{
	"we decided",
	"decision:",
	"agreed to",
	"settled on",
	// "chose " rather than "chose to": the object of the choice is usually
	// the thing chosen ("chose Go over Rust"), not an infinitive.
	"chose ",
	"picked ",
	"going with",
	"went with",
}

var lessonSignals = []string{
	"learned that",
	"lesson:",
	"pattern:",
	"when this happens",
	"turns out",
	"keep in mind",
	"watch out for",
	"next time",
	// Prescriptive rules. A rule is what a lesson sounds like once it has
	// been learned; see the note on correctionSignals.
	"never ",
	"always ",
	"don't ",
	"do not ",
	"must not",
}

func containsAny(s string, signals []string) bool {
	for _, sig := range signals {
		if strings.Contains(s, sig) {
			return true
		}
	}
	return false
}
