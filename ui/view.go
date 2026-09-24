package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
	"github.com/vitzeno/detent/ui/theme"
)

// View returns the screen plus the terminal state that goes with it.
// Under v2 altscreen, background and keyboard enhancements are
// properties of what we render, not program options set at startup.
func (m Model) View() tea.View {
	v := tea.NewView(m.baseView())
	v.AltScreen = true
	v.KeyboardEnhancements = tea.KeyboardEnhancements{}
	// The palette only works against the ground it was picked for, so
	// the theme decides both.
	v.BackgroundColor = theme.Background
	return v
}

func (m Model) baseView() string {
	if m.layout.width <= 0 {
		return "loading…"
	}
	outputBlock := island.Render(m.viewportHeader(), m.nav.focus == focusOutput, m.detailLines(), m.layout.outputColW, m.output.Height()+1)
	historyBlock := island.Render(m.historyHeader(), m.nav.focus == focusHistory, m.histWindow(), m.layout.histColW, m.nav.histHeight+1)

	var b strings.Builder
	b.WriteString(m.sessionBar())
	b.WriteString("\n")
	b.WriteString(layout.Row(outputBlock, historyBlock))
	b.WriteString("\n")
	b.WriteString(m.statusBar() + "\n")

	switch m.mode {
	case modeConfirm:
		b.WriteString(m.confirmBox())
	case modeBound, modeUndo:
		b.WriteString(m.questionBox())
	default:
		b.WriteString(island.Render("", m.nav.focus == focusInput, strings.Split(m.inputBar(), "\n"), m.layout.width, m.prompt.Rows()))
	}
	return b.String()
}

// histWindow is what sizeViewport laid out on the way into this
// frame. Falling back keeps the first frame right, before any Update.
func (m Model) histWindow() []string {
	if m.nav.histWindow != nil {
		return m.nav.histWindow
	}
	window, _ := m.historyWindow()
	return window
}

// historyWindow returns the visible slice and the offset it settled
// on. Pure, so sizeViewport can call it before anything is committed.
func (m Model) historyWindow() (window []string, offset int) {
	// Following means the tail is all anyone sees, so only the tail is
	// rendered. Offset is -1 because the total was never counted;
	// navUp pins it before it is ever read.
	if m.nav.follow {
		lines := flatten(m.historyTail(m.nav.histHeight))
		if len(lines) > m.nav.histHeight {
			lines = lines[len(lines)-m.nav.histHeight:]
		}
		return lines, -1
	}

	lines, cursorLine := m.historyAll()
	start := 0
	if len(lines) > m.nav.histHeight {
		start = m.nav.histOffset
		if cursorLine < start {
			start = cursorLine
		}
		if cursorLine >= start+m.nav.histHeight {
			start = cursorLine - m.nav.histHeight + 1
		}
		start = max(start, 0)
	}
	return lines[start:min(start+m.nav.histHeight, len(lines))], start
}

// historyAll flattens every entry and reports the cursor's line.
func (m Model) historyAll() (lines []string, cursorLine int) {
	entries, cursorEntry := m.historyLines()
	for i, e := range entries {
		if i == cursorEntry {
			cursorLine = len(lines)
		}
		lines = append(lines, strings.Split(e, "\n")...)
	}
	return lines, cursorLine
}

func flatten(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, strings.Split(e, "\n")...)
	}
	return out
}

func (m Model) inputBar() string {
	return m.prompt.View(paneMark(m.nav.focus == focusInput))
}
