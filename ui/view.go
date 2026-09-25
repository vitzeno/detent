package ui

import (
	"image/color"
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
	outputBlock := island.Render(m.viewportHeader(), paneBorder(m.nav.focus == focusOutput), m.detailLines(), m.layout.outputColW, m.output.Height()+1)
	historyBlock := island.Render(m.historyHeader(), paneBorder(m.nav.focus == focusHistory), m.histWindow(), m.layout.histColW, m.nav.histHeight+1)

	var b strings.Builder
	b.WriteString(m.sessionBar())
	b.WriteString("\n")
	b.WriteString(layout.Row(outputBlock, historyBlock))
	b.WriteString("\n")
	b.WriteString(m.statusBar() + "\n")

	switch m.mode {
	case modeConfirm:
		b.WriteString(m.confirmBox())
	case modeBound, modeUndo, modeForget:
		b.WriteString(m.questionBox())
	default:
		b.WriteString(island.Render("", paneBorder(m.nav.focus == focusInput), strings.Split(m.inputBar(), "\n"), m.layout.width, m.prompt.Rows()))
	}
	return b.String()
}

// paneBorder is the ordinary rule every zone follows: accent when it
// has focus, the neutral border when it does not.
func paneBorder(active bool) color.Color {
	if active {
		return accent
	}
	return border
}

// histWindow is what sizeViewport laid out. The fallback is the first
// frame, which lands before any Update.
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
	// Only the tail is on screen, so only the tail is drawn. Offset is
	// -1 because no total was counted; navUp pins one before it matters.
	if m.nav.follow {
		lines := m.historyTail(m.nav.histHeight)
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

// historyAll is every line and the cursor's. Assembling it walks every
// block, and Update wants it for every message once the pane is
// scrolled away from the end, so the whole of it is cached too.
func (m Model) historyAll() (lines []string, cursorLine int) {
	key := histKey{
		rev: m.histRev, width: m.blockWidth(),
		cursor: m.nav.cursor, spinner: m.spinnerFrame(),
	}
	if m.hist != nil && m.hist.lines != nil && m.hist.key == key {
		return m.hist.lines, m.hist.cursorLine
	}
	lines, cursorLine = m.historyLines()
	if m.hist != nil {
		*m.hist = histCache{key: key, lines: lines, cursorLine: cursorLine}
	}
	return lines, cursorLine
}

func (m Model) inputBar() string {
	return m.prompt.View(paneMark(m.nav.focus == focusInput))
}
