package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

// Tests for usage_view.go: the /usage tool block.

func TestUI_UsageToolBlock(t *testing.T) {
	m, _ := usageModel()

	nm, _ := m.runSlash("/usage")
	m = nm.(Model)
	require.Equal(t, focusOutput, m.nav.focus, "/usage jumps straight to viewing it")

	v := m.View()
	require.Contains(t, v, "session · 1 goal(s)")
	require.Contains(t, v, "find it")
	require.NotContains(t, v, "ls -la", "steps hidden until expanded")

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Contains(t, m.View(), "ls -la")
	require.Contains(t, m.View(), "dwell")

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	require.Equal(t, focusHistory, m.nav.focus, "esc steps back to history like any other output-pane view")
	require.Contains(t, m.View(), "session · 1 goal(s)", "the tool block itself is untouched by esc")
}
