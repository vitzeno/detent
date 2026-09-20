package ui

import (
	"context"
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
	assert.Contains(t, v, "not rolled back", "the bind-mount caveat is stated up front")
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
