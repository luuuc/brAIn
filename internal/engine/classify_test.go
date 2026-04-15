package engine

import (
	"testing"

	"github.com/luuuc/brain/internal/memory"
)

func TestClassifyLayer(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    memory.Layer
	}{
		// Correction signals
		{name: "stop signal", content: "Stop using the old API endpoint", want: memory.LayerCorrection},
		{name: "override signal", content: "Override the default timeout to 30s", want: memory.LayerCorrection},
		{name: "never signal", content: "Never deploy on Friday afternoons", want: memory.LayerCorrection},
		{name: "always signal", content: "Always run migrations before deploy", want: memory.LayerCorrection},
		{name: "don't signal", content: "Don't use fmt.Println in production code", want: memory.LayerCorrection},
		{name: "do not signal", content: "Do not commit .env files", want: memory.LayerCorrection},
		{name: "wrong signal", content: "The previous approach was wrong", want: memory.LayerCorrection},
		{name: "must not signal", content: "You must not bypass the auth middleware", want: memory.LayerCorrection},

		// Decision signals
		{name: "we decided", content: "We decided to use PostgreSQL for the main store", want: memory.LayerDecision},
		{name: "decision colon", content: "Decision: all APIs return JSON", want: memory.LayerDecision},
		{name: "agreed to", content: "The team agreed to freeze deps before release", want: memory.LayerDecision},
		{name: "settled on", content: "Settled on Go 1.23 as minimum version", want: memory.LayerDecision},
		{name: "chose to", content: "We chose to use conventional commits", want: memory.LayerDecision},
		{name: "going with", content: "Going with the markdown adapter as default", want: memory.LayerDecision},

		// Lesson signals
		{name: "learned that", content: "We learned that batch inserts are 10x faster", want: memory.LayerLesson},
		{name: "lesson colon", content: "Lesson: check error returns in Go", want: memory.LayerLesson},
		{name: "pattern colon", content: "Pattern: use table-driven tests for exhaustive coverage", want: memory.LayerLesson},
		{name: "turns out", content: "Turns out the cache invalidation was the bottleneck", want: memory.LayerLesson},
		{name: "keep in mind", content: "Keep in mind that the CI runner has limited memory", want: memory.LayerLesson},
		{name: "watch out for", content: "Watch out for nil pointer dereferences in the parser", want: memory.LayerLesson},
		{name: "next time", content: "Next time we should run benchmarks before optimizing", want: memory.LayerLesson},

		// Default to fact
		{name: "plain fact", content: "The users table has 12 columns", want: memory.LayerFact},
		{name: "empty content", content: "", want: memory.LayerFact},
		{name: "no signals", content: "PostgreSQL version is 15.2", want: memory.LayerFact},

		// Edge cases: misclassification scenarios
		{name: "we decided in factual context", content: "The committee we decided to observe uses Robert's Rules", want: memory.LayerDecision},
		{name: "correction wins over decision", content: "Stop doing what we decided last week — it's wrong", want: memory.LayerCorrection},
		{name: "case insensitive", content: "WE DECIDED to use uppercase sometimes", want: memory.LayerDecision},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyLayer(tt.content)
			if got != tt.want {
				t.Errorf("ClassifyLayer(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}
