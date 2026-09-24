package ui

import (
	tea "charm.land/bubbletea/v2"
)

// Where a keystroke moves things: the history cursor, the output
// pane's own scroll, and which zone has focus. keys.go decides what a
// key means; this decides what it does.

// outputNav moves inside the detail component: the view's own
// selection when it draws one, viewport lines otherwise.
func (m Model) outputNav(d int) (tea.Model, tea.Cmd) {
	if r := m.focused(); r != nil && !r.running {
		if b, ok := boundView(r); ok {
			if n, ok := b.SelectableRows(); ok && n > 0 {
				r.tableCursor = min(max(r.tableCursor+d, 0), n-1)
				return m, nil
			}
		}
	}
	if d < 0 {
		m.output.ScrollUp(1)
	} else {
		m.output.ScrollDown(1)
	}
	return m, nil
}

func (m Model) navUp() (tea.Model, tea.Cmd) {
	if m.nav.cursor > 0 {
		if m.nav.follow {
			// Leaving follow needs a real offset, and following never
			// counted one. One full layout, on a keystroke.
			lines, _ := m.historyAll()
			m.nav.histOffset = max(0, len(lines)-m.nav.histHeight)
		}
		m.nav.cursor--
		m.nav.follow = false
	}
	return m, nil
}

func (m Model) navDown() (tea.Model, tea.Cmd) {
	rows := m.rows()
	if m.nav.cursor < len(rows)-1 {
		m.nav.cursor++
		if m.nav.cursor == len(rows)-1 {
			m.nav.follow = true
		}
	}
	return m, nil
}

func (m Model) scrollViewport(key string) (tea.Model, tea.Cmd) {
	if key == "pgup" {
		m.output.HalfPageUp()
	} else {
		m.output.HalfPageDown()
	}
	return m, nil
}

// toggleFocus cycles input → history → output → input. Arrows act in
// whichever pane is focused.
func (m Model) toggleFocus() (tea.Model, tea.Cmd) {
	switch m.nav.focus {
	case focusInput:
		m.nav.focus = focusHistory
		m.prompt.Blur()
	case focusHistory:
		m.nav.focus = focusOutput
	default:
		m.nav.focus = focusInput
		m.prompt.Focus()
	}
	return m, nil
}
