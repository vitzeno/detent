package status

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/vitzeno/detent/event"
)

func TestBadge(t *testing.T) {
	tests := []struct {
		name         string
		row          Row
		icon, detail string
	}{
		{"running", Row{Running: true, LiveLines: 7}, "…", "7 live lines"},
		{"running past the cap", Row{Running: true, LiveLines: 7, Dropped: 2}, "…", "7 live lines (+2 dropped)"},
		{"pending", Row{}, "○", "pending"},
		{"exited 0, awaiting a verdict", Row{HasResult: true, Summary: "exit 0, 3 lines"}, "✓", "exit 0, 3 lines · judging…"},
		{"exited 1, awaiting a verdict", Row{HasResult: true, ExitCode: 1, Summary: "exit 1, 0 lines"}, "✗", "exit 1, 0 lines · judging…"},
		// Nothing judges one of these, so the summary is all there is.
		{"nothing will judge it", Row{HasResult: true, Summary: "clean", NoVerdict: true}, "✓", "clean"},
		// It exited 0 because it never got to exit at all.
		{"could not run", Row{HasResult: true, Err: true, Summary: "stopped by the human", NoVerdict: true}, "✗", "stopped by the human"},
		{"clean", Row{Judged: true, Status: "clean_success"}, "✓", "clean"},
		{"warnings", Row{Judged: true, Status: "success_with_warnings"}, "⚠", "warnings"},
		{"failed", Row{Judged: true, Status: "failed"}, "✗", "failed"},
		{"empty", Row{Judged: true, Status: "empty"}, "○", "no output"},
		{"a status nobody spelt", Row{Judged: true, Status: "novel"}, "○", "done"},
		{"needs attention", Row{Judged: true, Status: "failed", Attention: 0.9}, "⚠", "failed · attention 0.90"},
		{"exactly at the threshold", Row{Judged: true, Status: "clean_success", Attention: AttentionThreshold}, "⚠", "clean · attention 0.70"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			icon, detail := Badge(tt.row, "…")
			assert.Equal(t, tt.icon, ansi.Strip(icon))
			assert.Equal(t, tt.detail, detail)
		})
	}
}

func TestBar(t *testing.T) {
	out := Bar("…", "running…", "[q] quit", Notice{}, true)
	assert.Contains(t, out, "running…")
	assert.Contains(t, out, "[q] quit")
	assert.NotContains(t, out, "✓", "no notice, no mark")

	out = Bar("", "idle", "[tab] history", Notice{Text: "abort sent"}, false)
	assert.Contains(t, out, "abort sent")

	// Success and failure must be told apart without reading the words.
	ok := Bar("", "idle", "[k]", Notice{Text: "done"}, false)
	bad := Bar("", "idle", "[k]", Notice{Text: "done", Bad: true}, false)
	assert.Contains(t, ok, "✓ done")
	assert.Contains(t, bad, "✗ done")
	assert.NotEqual(t, ok, bad, "the two must render differently, not just read differently")
}

func TestDur(t *testing.T) {
	for in, want := range map[time.Duration]string{
		412 * time.Millisecond:    "412ms",
		3200 * time.Millisecond:   "3.2s",
		59_960 * time.Millisecond: "1m0s",
		130 * time.Second:         "2m10s",
	} {
		assert.Equal(t, want, Dur(in), "%v", in)
	}
}

func TestTokens(t *testing.T) {
	for in, want := range map[int]string{
		847:       "847",
		9400:      "9.4k",
		999_949:   "999.9k",
		999_950:   "1.0M",
		2_100_000: "2.1M",
	} {
		assert.Equal(t, want, Tokens(in), "%d", in)
	}
}

// Every status the judge can publish reads as itself rather than "done",
// and every labelled kind is one event has.
func TestVocabulary_AgreesWithEvent(t *testing.T) {
	assert.ElementsMatch(t, event.Statuses(), slices.Collect(maps.Keys(verdicts)))
	for k := range kindLabels {
		assert.Contains(t, event.RenderKinds(), k)
	}
}
