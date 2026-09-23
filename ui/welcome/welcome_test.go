package welcome

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFacts() Facts {
	return Facts{
		Proposer: "test-model", Judge: "jev", RunMode: "sandbox",
		Image: "docker.io/library/buildpack-deps:24.04-scm", Mount: "/workspace", Network: "host",
	}
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

// displayCol is where sub starts on screen, in columns not bytes.
func displayCol(line, sub string) int {
	i := strings.Index(line, sub)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(line[:i])
}

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

// TestWelcome_FitsAndCentresAtEverySize: the island cuts whatever
// overflows, so the welcome pane drops whole sections itself rather
// than losing a row mid-sentence. Where commands run outranks the
// niceties, and what's left sits in the middle of the pane.
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

// The welcome pane is where a human learns which build they are
// running, so the version sits under the name rather than only in the
// status bar, which truncates first when the window is narrow.
func TestLines_ShowsTheVersion(t *testing.T) {
	got := Lines(Facts{
		Version: "9.9.9", Proposer: "m", RunMode: "host",
	}, 90, 30, 0)
	assert.Contains(t, strings.Join(got, "\n"), "9.9.9")
}

// The boot pane is where you find the id to resume this run later,
// and where a session nothing records has to say so.
func TestWelcome_SaysWhatTheSessionIs(t *testing.T) {
	const id = "01a0cf7f-dffa-7d71-b7a1-419e79eed0d2"

	recorded := strings.Join(Lines(Facts{
		Version: "v", Proposer: "m", RunMode: "host",
		Session: id, Recorded: true, Sessions: 3,
	}, 80, 40, 0), "\n")
	assert.Contains(t, recorded, id, "the id is what you type after -resume")
	assert.Contains(t, recorded, "3 sessions")
	assert.NotContains(t, recorded, "persistence pending", "that layer landed")

	unrecorded := strings.Join(Lines(Facts{
		Version: "v", Proposer: "m", RunMode: "host", Recorded: false,
	}, 80, 40, 0), "\n")
	assert.Contains(t, unrecorded, "not being recorded")

	resumed := strings.Join(Lines(Facts{
		Version: "v", Proposer: "m", RunMode: "host",
		Session: id, Recorded: true, Resumed: 12,
	}, 80, 40, 0), "\n")
	assert.Contains(t, resumed, "12 records")
}
