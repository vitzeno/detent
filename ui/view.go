package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
)

// View returns the screen plus the terminal state that goes with it.
// Under v2 altscreen and keyboard enhancements are properties of what
// we render, not program options set once at startup.
func (m Model) View() tea.View {
	v := tea.NewView(m.baseView())
	v.AltScreen = true
	v.KeyboardEnhancements = tea.KeyboardEnhancements{}
	return v
}

func (m Model) baseView() string {
	if m.layout.width <= 0 {
		return "loading…"
	}
	outputBlock := island.Render(m.viewportHeader(), m.nav.focus == focusOutput, m.detailLines(), m.layout.outputColW, m.output.Height()+1)
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
		b.WriteString(island.Render("", m.nav.focus == focusInput, strings.Split(m.inputBar(), "\n"), m.layout.width, m.prompt.Rows()))
	}
	return b.String()
}

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

func (m Model) inputBar() string {
	return m.prompt.View(paneMark(m.nav.focus == focusInput))
}
