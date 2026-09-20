package ui

import (
	"strings"
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

// TestUI_SpinnerTickRefreshesHistoryWindow locks a bug where the
// history pane's spinner line (baked into the nav.histWindow cache)
// froze on the last real content change while the status bar's own
// spinner, computed fresh each View(), kept animating.
func TestUI_SpinnerTickRefreshesHistoryWindow(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g"}}
	m.cur = m.blocks[0]
	m.waiting = true
	m.refreshViewport()

	prepsBefore := m.perf.uiPreps
	nm, _ := m.Update(m.spinner.Tick())
	m = nm.(Model)

	require.Greater(t, m.perf.uiPreps, prepsBefore, "a spinner tick while waiting must refresh the cached history window")
	require.Contains(t, strings.Join(m.nav.histWindow, "\n"), m.spinner.View(), "history's spinner line must reflect the current frame, not a stale cached one")
}
