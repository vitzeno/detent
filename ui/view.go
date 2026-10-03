package ui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/ui/island"
	"github.com/vitzeno/detent/ui/layout"
)

// View returns the screen plus the terminal state that goes with it:
// altscreen, background and keyboard enhancements are part of the view.
func (m Model) View() tea.View {
	v := tea.NewView(m.baseView())
	v.AltScreen = true
	v.KeyboardEnhancements = tea.KeyboardEnhancements{}
	// The palette only works against the ground it was picked for, so
	// the theme decides both.
	v.BackgroundColor = palette.Background
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
		b.WriteString(island.Render("", m.inputBorder(), strings.Split(m.inputBar(), "\n"), m.layout.width, m.prompt.Rows()))
	}
	return b.String()
}

// paneBorder is the ordinary rule every zone follows: accent when it
// has focus, the neutral border when it does not.
func paneBorder(active bool) color.Color {
	if active {
		return palette.Accent
	}
	return palette.Border
}

// inputBorder says which language the bar is in. Amber for a command,
// which runs with neither the approval gate nor a checkpoint.
func (m Model) inputBorder() color.Color {
	if m.nav.focus != focusInput {
		return palette.Border
	}
	if m.entry == entryShell {
		return palette.Caution
	}
	return palette.Accent
}

func (m Model) inputBar() string {
	return m.prompt.View(paneMark(m.nav.focus == focusInput))
}
