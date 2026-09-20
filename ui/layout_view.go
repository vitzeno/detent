package ui

import (
	"strings"

	"github.com/vitzeno/detent/ui/layout"
)

var bodyWeights = []int{3, 2} // [output, history]; output gets the larger share

// minPaneWidth is the outer-width floor below which a pane stops being
// worth rendering as its own island.
const minPaneWidth = 28

// sizeViewport refits every pane to the window and to what the panes
// currently hold, then re-renders the output. Update calls it once per
// message, so no handler has to remember to.
func (m *Model) sizeViewport() {
	m.prompt.Resize(m.layout.width)
	// Bottom zone height is measured, not guessed — content varies with
	// rationale and danger flags, and the input grows with what's typed.
	bottom := m.prompt.Rows() + 2
	switch m.mode {
	case modeConfirm:
		bottom = len(strings.Split(m.confirmBox(), "\n"))
	case modeSaveConfirm:
		bottom = len(strings.Split(m.saveConfirmBox(), "\n"))
	}
	avail := m.layout.height - 2 - islandOverhead - bottom
	if avail < 6 {
		avail = 6
	}
	m.nav.histHeight = avail
	m.output.Height = avail

	widths := layout.Split(m.layout.width, bodyWeights, minPaneWidth)
	m.layout.outputColW, m.layout.histColW = widths[0], widths[1]
	m.output.Width = paneInner(m.layout.outputColW)
	m.refreshViewport()
}

// islandOverhead is a titled zone island's non-content lines: header
// plus top and bottom border.
const islandOverhead = 3

// paneInner matches island.Render's own inner := width-4.
func paneInner(outer int) int {
	return max(20, outer-4)
}

func truncateWidth(s string, w int) string {
	if w < 4 {
		w = 4
	}
	if len(s) <= w {
		return s
	}
	return s[:w-1] + "…"
}
