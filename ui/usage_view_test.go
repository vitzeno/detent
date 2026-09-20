package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// Tests for usage_view.go: the /usage tool block.

func TestUI_UsageToolBlock(t *testing.T) {
	m, _ := usageModel()

	nm, _ := m.runSlash("/usage")
	m = nm.(Model)
	require.Equal(t, focusOutput, m.nav.focus, "/usage jumps straight to viewing it")

	v := m.View().Content
	require.Contains(t, v, "session · 1 goal(s)")
	require.Contains(t, v, "find it")
	require.NotContains(t, v, "ls -la", "steps hidden until expanded")

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = nm.(Model)
	require.Contains(t, m.View().Content, "ls -la")
	require.Contains(t, m.View().Content, "dwell")

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = nm.(Model)
	require.Equal(t, focusHistory, m.nav.focus, "esc steps back to history like any other output-pane view")
	require.Contains(t, m.View().Content, "session · 1 goal(s)", "the tool block itself is untouched by esc")
}
