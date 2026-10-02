package ui

import (
	"strings"

	"github.com/vitzeno/detent/ui/layout"
)

// How the three zones divide the window. Every size comes from here, so
// the panes tile the terminal and the session bar stays on top.

// sizeViewport refits every pane to the window and what it holds, then
// re-renders the output. Update calls it once per message.
func (m *Model) sizeViewport() {
	m.prompt.Resize(m.layout.width)
	// Measured, not guessed: a question varies with its rationale, and the
	// input grows with what is typed.
	bottom := m.prompt.Rows() + 2
	switch m.mode {
	case modeConfirm:
		bottom = len(strings.Split(m.confirmBox(), "\n"))
	case modeBound, modeUndo, modeForget:
		bottom = len(strings.Split(m.questionBox(), "\n"))
	}
	avail := m.layout.height - 2 - islandOverhead - bottom
	if avail < 6 {
		avail = 6
	}
	m.nav.histHeight = avail
	m.output.SetHeight(avail)

	widths := layout.Split(m.layout.width, bodyWeights, minPaneWidth)
	m.layout.outputColW, m.layout.histColW = widths[0], widths[1]
	m.output.SetWidth(paneInner(m.layout.outputColW))
	m.refreshViewport()
}

var bodyWeights = []int{3, 2} // output, history

const (
	// minPaneWidth is the outer-width floor below which a pane stops
	// being worth rendering as its own island.
	minPaneWidth = 28
	// islandOverhead is a titled zone island's non-content lines:
	// header plus top and bottom border.
	islandOverhead = 3
)

// paneInner matches island.Render's own inner := width-4.
func paneInner(outer int) int {
	return max(20, outer-4)
}
