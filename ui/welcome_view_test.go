package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for welcome_view.go: the boot pane, shown until a row exists.

func welcomeModel(t *testing.T, info SessionInfo) Model {
	t.Helper()
	m := New(context.Background(), newFakeDriver(), info)
	m.layout.width, m.layout.height = 120, 34
	m.sizeViewport()
	return m
}

func TestWelcome_ShowsUntilARowExists(t *testing.T) {
	m := welcomeModel(t, SessionInfo{Proposer: "m", RunMode: "sandbox"})
	require.True(t, m.showWelcome(), "boots with nothing focused")
	assert.Contains(t, plain(m.View().Content), "this machine")

	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{{command: "ls"}}}}
	require.False(t, m.showWelcome(), "a row takes the pane back")
	assert.NotContains(t, plain(m.View().Content), "this machine")
}

func TestWelcome_ReportsHostAndSandbox(t *testing.T) {
	v := plain(welcomeModel(t, SessionInfo{
		Proposer: "m", RunMode: "sandbox",
		Image: "docker.io/library/buildpack-deps:24.04-scm", Mount: "/workspace",
	}).View().Content)

	assert.Contains(t, v, "darwin/", "host os/arch")
	assert.Contains(t, v, "sandboxed")
	assert.Contains(t, v, "buildpack-deps")
	assert.Contains(t, v, "/workspace")
	assert.Contains(t, v, "never rolled back", "the bind-mount caveat is stated up front")
}

func TestWelcome_HostModeSaysUnsandboxed(t *testing.T) {
	v := plain(welcomeModel(t, SessionInfo{Proposer: "m", RunMode: "host"}).View().Content)
	assert.Contains(t, v, "unsandboxed")
	assert.NotContains(t, v, "image", "no image to report when nothing is sandboxed")
}

func TestWelcome_DetentAnimationSteps(t *testing.T) {
	seatedAt := func(frame int) int {
		return displayCol(plain(detentTrack(frame)[0]), "◆")
	}
	first := seatedAt(0)
	assert.NotEqual(t, first, seatedAt(1), "the pawl advances between frames")
	assert.Equal(t, first, seatedAt(detentNotches), "and wraps back round")

	// The pawl marker sits under the notch it has seated into.
	for frame := range detentNotches {
		lines := detentTrack(frame)
		assert.Equal(t, seatedAt(frame), displayCol(plain(lines[1]), "▲"),
			"pawl must line up with the seated notch")
	}
}

func TestWelcome_TickStopsOnceAGoalExists(t *testing.T) {
	m := welcomeModel(t, SessionInfo{Proposer: "m"})

	nm, cmd := m.Update(welcomeTickMsg{})
	m = nm.(Model)
	assert.Equal(t, 1, m.welcomeFrame)
	require.NotNil(t, cmd, "keeps animating while the pane is up")

	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{{command: "ls"}}}}
	nm, cmd = m.Update(welcomeTickMsg{})
	m = nm.(Model)
	assert.Equal(t, 1, m.welcomeFrame, "frame frozen")
	assert.Nil(t, cmd, "no idle ticker once the pane is gone")
}

// TestWelcome_FitsAndCentresAtEverySize: the island cuts whatever
// overflows, so the welcome pane drops whole sections itself rather
// than losing a row mid-sentence. Where commands run outranks the
// niceties, and what's left sits in the middle of the pane.
func TestWelcome_FitsAndCentresAtEverySize(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {110, 30}, {100, 24}, {90, 20}, {80, 16}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := welcomeModelSized(t, size[0], size[1])
			lines := m.welcomeLines()

			require.LessOrEqual(t, len(lines), m.output.Height(),
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
	m := welcomeModelSized(t, 120, 44)
	lines := m.welcomeLines()

	lead := 0
	for _, l := range lines {
		if strings.TrimSpace(plain(l)) != "" {
			break
		}
		lead++
	}
	trail := m.output.Height() - len(lines)
	assert.Positive(t, lead, "content must be pushed down, not pinned to the top")
	assert.InDelta(t, lead, trail, 2, "roughly as much space above as below")
}

func welcomeModelSized(t *testing.T, w, h int) Model {
	t.Helper()
	m := New(context.Background(), newFakeDriver(), SessionInfo{
		Proposer: "test-model", Judge: "jev", RunMode: "sandbox",
		Image: "docker.io/library/buildpack-deps:24.04-scm", Mount: "/workspace", Network: "host",
	})
	m.layout.width, m.layout.height = w, h
	m.sizeViewport()
	return m
}
