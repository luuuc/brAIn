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

var correctionSignals = []string{
	"stop ",
	"override",
	"never ",
	"always ",
	"don't ",
	"do not ",
	"wrong",
	"incorrect",
	"must not",
}

var decisionSignals = []string{
	"we decided",
	"decision:",
	"agreed to",
	"settled on",
	"chose to",
	"going with",
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
}

func containsAny(s string, signals []string) bool {
	for _, sig := range signals {
		if strings.Contains(s, sig) {
			return true
		}
	}
	return false
}
