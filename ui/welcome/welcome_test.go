package welcome

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWelcome_DetentAnimationSteps(t *testing.T) {
	seatedAt := func(frame int) int {
		return displayCol(plain(Track(frame)[0]), "◆")
	}
	first := seatedAt(0)
	assert.NotEqual(t, first, seatedAt(1), "the pawl advances between frames")
	assert.Equal(t, first, seatedAt(notches), "and wraps back round")

	// The pawl marker sits under the notch it has seated into.
	for frame := range notches {
		lines := Track(frame)
		assert.Equal(t, seatedAt(frame), displayCol(plain(lines[1]), "▲"),
			"pawl must line up with the seated notch")
	}
}

// The pane drops whole sections rather than letting the island cut a
// row, and where commands run outranks the niceties.
func TestWelcome_FitsAndCentresAtEverySize(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {110, 30}, {100, 24}, {90, 20}, {80, 16}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			f, w, h := testFacts(), size[0]-50, size[1]-8
			lines := Lines(f, w, h, 0)

			require.LessOrEqual(t, len(lines), h,
				"must fit the pane rather than rely on the island cutting it")
			joined := plain(strings.Join(lines, "\n"))
			assert.Contains(t, joined, "d e t e n t", "the banner always survives")
			for _, lower := range []string{"models", "session", "start with"} {
				if strings.Contains(joined, lower) {
					assert.Contains(t, joined, "sandboxed",
						"where commands run outranks %q", lower)
				}
			}
		})
	}
}

// Vertical centring: an emptier pane pads above, never below the point
// of pushing content out.
func TestWelcome_CentresVertically(t *testing.T) {
	f, w, h := testFacts(), 70, 36
	lines := Lines(f, w, h, 0)

	lead := 0
	for _, l := range lines {
		if strings.TrimSpace(plain(l)) != "" {
			break
		}
		lead++
	}
	trail := h - len(lines)
	assert.Positive(t, lead, "content must be pushed down, not pinned to the top")
	assert.InDelta(t, lead, trail, 2, "roughly as much space above as below")
}

// The welcome pane is where a human learns which build is running.
func TestLines_ShowsTheVersion(t *testing.T) {
	got := Lines(Facts{
		Version: "9.9.9", Model: "m", RunMode: "host",
	}, 90, 30, 0)
	assert.Contains(t, strings.Join(got, "\n"), "9.9.9")
}

// The boot pane is where you find the id to resume this run later,
// and where a session nothing records has to say so.
func TestWelcome_SaysWhatTheSessionIs(t *testing.T) {
	const id = "01a0cf7f-dffa-7d71-b7a1-419e79eed0d2"

	recorded := strings.Join(Lines(Facts{
		Version: "v", Model: "m", RunMode: "host",
		Session: id, Recorded: true, Sessions: 3,
	}, 80, 40, 0), "\n")
	assert.Contains(t, recorded, id, "the id is what you type after -resume")
	assert.Contains(t, recorded, "3 sessions")
	assert.NotContains(t, recorded, "persistence pending", "that layer landed")

	unrecorded := strings.Join(Lines(Facts{
		Version: "v", Model: "m", RunMode: "host", Recorded: false,
	}, 80, 40, 0), "\n")
	assert.Contains(t, unrecorded, "not being recorded")

	resumed := strings.Join(Lines(Facts{
		Version: "v", Model: "m", RunMode: "host",
		Session: id, Recorded: true, Resumed: 12,
	}, 80, 40, 0), "\n")
	assert.Contains(t, resumed, "12 records")
}

// Everything the pane reports is handed in, so a test can pin the rows
// that once read the process: the machine and the working directory.
func TestWelcome_ReportsTheMachineItIsHanded(t *testing.T) {
	f := testFacts()
	f.OS, f.Arch, f.CPUs, f.WorkDir = "linux", "arm64", 8, "~/src/detent"
	got := plain(strings.Join(Lines(f, 90, 40, 0), "\n"))
	assert.Contains(t, got, "linux/arm64 · 8 cpu")
	assert.Contains(t, got, "~/src/detent")
}

func testFacts() Facts {
	return Facts{
		Model: "test-model", Judge: "jev", RunMode: "sandbox",
		Image: "docker.io/library/buildpack-deps:24.04-scm", Mount: "/workspace", Network: "host",
	}
}

func plain(s string) string { return ansi.Strip(s) }

// displayCol is where sub starts on screen, in columns not bytes.
func displayCol(line, sub string) int {
	before, _, ok := strings.Cut(line, sub)
	if !ok {
		return -1
	}
	return lipgloss.Width(before)
}
