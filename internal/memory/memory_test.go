package memory

import "testing"

func TestMemory_Title(t *testing.T) {
	tests := []struct {
		name string
		m    Memory
		want string
	}{
		{
			// The case this method exists for. An effectiveness body opens
			// with its "## Outcomes" list, so the first line is a markdown
			// heading that says nothing about the memory.
			name: "effectiveness uses persona and domain",
			m: Memory{
				Layer:   LayerEffectiveness,
				Domain:  "payments",
				Persona: "kent-beck",
				Body:    "## Outcomes\n- 2026-04-14: accepted",
			},
			want: "kent-beck effectiveness in payments",
		},
		{
			name: "effectiveness with no persona falls back to the body",
			m: Memory{
				Layer:  LayerEffectiveness,
				Domain: "payments",
				Body:   "## Outcomes\n- 2026-04-14: accepted",
			},
			want: "## Outcomes",
		},
		{
			name: "fact uses the first line",
			m:    Memory{Layer: LayerFact, Body: "The users table has 12M rows\n\nMore detail."},
			want: "The users table has 12M rows",
		},
		{
			name: "single-line body is the whole body",
			m:    Memory{Layer: LayerCorrection, Body: "Stop flagging nullable email"},
			want: "Stop flagging nullable email",
		},
		{
			name: "empty body",
			m:    Memory{Layer: LayerFact},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.Title(); got != tt.want {
				t.Errorf("Title() = %q, want %q", got, tt.want)
			}
		})
	}
}
