package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
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

	// esc closes the panel: it is a page about the session rather than
	// a row to step back from, so there is nothing left behind.
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = nm.(Model)
	assert.False(t, m.panel.open())
	assert.NotContains(t, m.View().Content, "session · 1 goal(s)")
}

// /usage is a page about the session, not something the session did,
// so it leaves history alone. Filing one put a row where the running
// goal's own row belongs, which is what you are looking at when you
// ask a long goal how it is going.
func TestUsage_LeavesHistoryAlone(t *testing.T) {
	m, _ := usageModel()
	before := len(m.blocks)

	nm, _ := m.runSlash("/usage")
	m = nm.(Model)
	assert.Len(t, m.blocks, before, "no new block")
	assert.True(t, m.panel.open())

	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Len(t, nm.(Model).blocks, before, "and none left behind")
}

// The panel scrolls, so nothing is capped. Eight goals used to be the
// limit, with the rest replaced by a count.
func TestUsage_ShowsEveryGoalRatherThanTheFirstFew(t *testing.T) {
	m, drv := usageModel()
	for i := range 20 {
		drv.addGoal(fmt.Sprintf("goal %d", i))
	}
	nm, _ := m.runSlash("/usage")
	m = nm.(Model)

	lines := m.panelLines()
	body := strings.Join(lines, "\n")
	assert.Contains(t, body, "goal 19", "the last one is there to scroll to")
	assert.NotContains(t, body, "more goals", "nothing is summarised away")
}
