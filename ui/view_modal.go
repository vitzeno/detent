package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
)

// The modals: the finder, the inspector, the resume picker and the review, each
// a box over the panes, which stay in view round it, dimmed. Each keeps its own keys.

const (
	// modalChrome is the most lines a modal spends on anything but its body.
	modalChrome = 5
	// A small terminal gives up the padding before the box gets this small.
	modalMinWidth = 60
	modalMinBody  = 4
)

// inModal is whether a modal holds every key.
func (m Model) inModal() bool {
	return m.mode == modeFinder || m.mode == modeInspector || m.mode == modeResume || m.mode == modeReview
}

// withOverlay floats the open modal over base, with the panes behind it dimmed.
func (m Model) withOverlay(base string) string {
	var box string
	switch m.mode {
	case modeFinder:
		box = m.finderBox()
	case modeInspector:
		box = m.inspectorBox()
	case modeResume:
		box = m.resumeBox()
	case modeReview:
		box = m.reviewBox()
	default:
		return base
	}
	lines := strings.Split(base, "\n")
	// The panes start under the session bar.
	for i := 1; i <= m.modalArea() && i < len(lines); i++ {
		lines[i] = styleFaint.Render(ansi.Strip(lines[i]))
	}
	x, y := m.modalPadding()
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(strings.Join(lines, "\n")),
		lipgloss.NewLayer(box).X(x).Y(1+y).Z(1),
	).Render()
}

// modalBox frames body under title, with the key line last.
func (m Model) modalBox(title string, body []string, keys string) string {
	body = append(body, styleFaint.Render(layout.Truncate(keys, island.Inner(m.modalWidth()))))
	return island.Render(title, palette.Accent, body, m.modalWidth(), m.modalHeight()-2)
}

// modalPane is one of a modal's two panes, its lines already fitted to it.
type modalPane struct {
	title   string
	lines   []string
	focused bool
}

// modalPanes sets two panes side by side, lw and rw wide.
func (m Model) modalPanes(lw, rw int, left, right modalPane) []string {
	h := m.modalPaneHeight()
	l := island.Render(styleFaint.Render(left.title), paneBorder(left.focused), left.lines, lw, h+1)
	r := island.Render(styleFaint.Render(right.title), paneBorder(right.focused), right.lines, rw, h+1)
	return strings.Split(layout.Row(l, r), "\n")
}

// modalPaneWidths splits the box as the main view splits the screen, wide then narrow.
func (m Model) modalPaneWidths() (wide, narrow int) {
	w := layout.Split(island.Inner(m.modalWidth()), bodyWeights, minPaneWidth)
	return w[0], w[1]
}

// modalPaneHeight is how many lines each pane shows.
func (m Model) modalPaneHeight() int { return max(1, m.modalHeight()-2-modalChrome) }

// modalArea is the height of the panes a modal floats over.
func (m Model) modalArea() int { return m.nav.histHeight + islandOverhead }

// modalPadding is an eighth of the width each side and a sixth of the
// panes above and below, less when the box would get too small.
func (m Model) modalPadding() (x, y int) {
	x = min(m.layout.width/8, max(0, (m.layout.width-modalMinWidth)/2))
	y = min(m.modalArea()/6, max(0, (m.modalArea()-2-modalChrome-modalMinBody)/2))
	return x, y
}

func (m Model) modalWidth() int {
	x, _ := m.modalPadding()
	return max(minPaneWidth, m.layout.width-2*x)
}

func (m Model) modalHeight() int {
	_, y := m.modalPadding()
	return m.modalArea() - 2*y
}

// listWindow is the run of n entries, height tall, that keeps cursor in view.
func listWindow(n, cursor, height int) (start, end int) {
	start = max(0, cursor-height+1)
	return start, min(start+height, n)
}
