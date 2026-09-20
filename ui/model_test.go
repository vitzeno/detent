package ui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Tests for model.go: Model-level behavior not owned by any one flow —
// trackNewest's "don't yank a reader's view" rule, and Update's own
// message dispatch.

func TestUI_TrackNewestParksOutputReaders(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{tableBlock()}
	m.nav.cursor = 0
	m.nav.follow = true

	m.nav.focus = focusOutput
	m.blocks[0].steps = append(m.blocks[0].steps, tableBlock().steps[0])
	m.trackNewest()
	require.Equal(t, 0, m.nav.cursor, "parked reader keeps its row")
	require.False(t, m.nav.follow, "parking unfollows")

	m.nav.focus = focusHistory
	m.nav.follow = true // re-followed by navigating to the bottom
	m.trackNewest()
	require.Equal(t, len(m.rows())-1, m.nav.cursor, "history focus still follows")
	require.True(t, m.nav.follow)
}

// TestUI_SpinnerTickAnimatesHistory locks a bug where the history
// pane's spinner line froze on the last content change while the
// status bar's own spinner kept animating. History is now derived per
// render, so both move together.
func TestUI_SpinnerTickAnimatesHistory(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g"}}
	m.cur = m.blocks[0]
	m.waiting = true

	nm, _ := m.Update(m.spinner.Tick())
	m = nm.(Model)

	require.Contains(t, m.View().Content, m.spinner.View(), "history's spinner must show the current frame, not a stale one")
}
