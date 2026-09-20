package ui

import (
	"strings"

	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/slash"
)

func (m Model) View() string {
	return m.baseView()
}

func (m Model) baseView() string {
	if m.layout.width <= 0 {
		return "loading…"
	}
	outputBlock := island.Render(m.viewportHeader(), m.nav.focus == focusOutput, m.detailLines(), m.layout.outputColW, m.output.Height+1)
	histWindow, _ := m.historyWindow()
	historyBlock := island.Render(m.historyHeader(), m.nav.focus == focusHistory, histWindow, m.layout.histColW, m.nav.histHeight+1)

	var b strings.Builder
	b.WriteString(m.sessionBar())
	b.WriteString("\n")
	b.WriteString(layout.Row(outputBlock, historyBlock))
	b.WriteString("\n")
	b.WriteString(m.statusLine() + "\n")

	switch m.mode {
	case modeConfirm:
		b.WriteString(m.confirmBox())
	case modeSaveConfirm:
		b.WriteString(m.saveConfirmBox())
	default:
		b.WriteString(island.Render("", m.nav.focus == focusInput, strings.Split(m.inputBar(), "\n"), m.layout.width, m.slashRows()+m.input.Height()))
	}
	return b.String()
}

// historyWindow returns the visible slice of history and the scroll
// offset it settled on. Pure, so View can call it directly on its
// throwaway copy; Update stores the offset back. Entries flatten to
// lines first, since expanded rows and banners span several each.

// historyWindow returns the visible slice of history and the scroll
// offset it settled on. Pure, so View can call it directly on its
// throwaway copy; Update stores the offset back. Entries flatten to
// lines first, since expanded rows and banners span several each.
func (m Model) historyWindow() (window []string, offset int) {
	entries, cursorEntry := m.historyLines()
	var lines []string
	cursorLine := 0
	for i, e := range entries {
		if i == cursorEntry {
			cursorLine = len(lines)
		}
		lines = append(lines, strings.Split(e, "\n")...)
	}

	start := 0
	if len(lines) > m.nav.histHeight {
		if m.nav.follow {
			start = len(lines) - m.nav.histHeight
		} else {
			start = m.nav.histOffset
			if cursorLine < start {
				start = cursorLine
			}
			if cursorLine >= start+m.nav.histHeight {
				start = cursorLine - m.nav.histHeight + 1
			}
			start = max(start, 0)
		}
	}
	return lines[start:min(start+m.nav.histHeight, len(lines))], start
}

// slashRows caps the dropdown so it can't eat the history pane.
func (m Model) slashRows() int {
	return min(len(m.slash.matches), slash.MaxRows)
}

func (m Model) inputBar() string {
	active := m.nav.focus == focusInput
	// The dropdown is multi-line, so it goes first: prefixing it with
	// the pane mark would indent only its first row and strand the
	// mark away from the prompt it belongs to.
	// Both the dropdown and the input are multi-line, so the pane mark
	// can't just be prefixed onto the whole block: it would indent one
	// row and leave the rest short. The dropdown goes first, then the
	// mark takes its own gutter beside every input row.
	var b strings.Builder
	b.WriteString(slash.View(m.slash.matches, m.slash.cursor))
	for i, line := range strings.Split(m.input.View(), "\n") {
		if i > 0 {
			b.WriteString("\n" + strings.Repeat(" ", inputMarkW))
		} else {
			b.WriteString(paneMark(active) + " ")
		}
		b.WriteString(line)
	}
	return b.String()
}
