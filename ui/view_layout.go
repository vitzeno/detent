package ui

import (
	"strings"

	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
)

// How the three zones divide the window. Every size comes from here, so
// the panes tile the terminal and the session bar stays on top.

var bodyWeights = []int{3, 2} // output, history

const (
	// minPaneWidth is the outer-width floor below which a pane stops
	// being worth rendering as its own island.
	minPaneWidth = 28
	// islandOverhead is a titled zone island's non-content lines:
	// header plus top and bottom border.
	islandOverhead = 3
	// minBodyRows is the fewest rows the panes shrink to for a question.
	minBodyRows = 6
)

// sizeViewport refits every pane to the window and what it holds, then
// re-renders the output. Update calls it once per message.
func (m *Model) sizeViewport() {
	m.prompt.Resize(m.layout.width)
	// Measured, not guessed: a question varies with its rationale, and the
	// input grows with what is typed.
	var bottom int
	switch m.mode {
	case modeInput, modeModal:
		bottom = m.prompt.Rows() + 2
	case modeConfirm:
		bottom = len(strings.Split(m.confirmBox(), "\n"))
	case modeBound, modeUndo, modeForget:
		bottom = len(strings.Split(m.questionBox(), "\n"))
	}
	// A command being approved outranks the panes on a short screen.
	floor := minBodyRows
	if m.mode == modeConfirm {
		floor = 1
	}
	avail := max(floor, m.layout.height-2-islandOverhead-bottom)
	m.nav.histHeight = avail
	m.output.SetHeight(avail)

	widths := layout.Split(m.layout.width, bodyWeights, minPaneWidth)
	m.layout.outputColW, m.layout.histColW = widths[0], widths[1]
	m.output.SetWidth(paneInner(m.layout.outputColW))
	m.refreshViewport()
	if m.modal != nil {
		m.modal.sync(m)
	}
}

// paneInner is what island.Render leaves for content.
func paneInner(outer int) int { return island.Inner(outer) }
