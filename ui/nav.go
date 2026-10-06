package ui

import (
	"slices"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Where a keystroke moves things: the history cursor, the output pane's
// scroll and focus. keys.go decides what a key means, this what it does.

// outputNav moves inside the detail component: the view's own
// selection when it draws one, viewport lines otherwise.
func (m Model) outputNav(d int) (Model, tea.Cmd) {
	if r := m.focused(); r != nil && !r.running {
		if b, ok := boundView(r); ok {
			if n, ok := b.SelectableRows(); ok && n > 0 {
				r.tableCursor = min(max(r.tableCursor+d, 0), n-1)
				return m, nil
			}
		}
	}
	if d < 0 {
		m.output.ScrollUp(-d)
	} else {
		m.output.ScrollDown(d)
	}
	return m, nil
}

func (m Model) navUp() (Model, tea.Cmd) {
	if m.nav.cursor > 0 {
		if m.nav.follow {
			// Following never counted a total, so pin one here.
			lines, _ := m.historyAll()
			m.nav.histOffset = max(0, len(lines)-m.histRows())
		}
		m.nav.cursor--
		m.nav.follow = false
	}
	return m, nil
}

// navTop puts the cursor on the first row, which stops following the newest.
func (m Model) navTop() (Model, tea.Cmd) {
	m.nav.cursor, m.nav.follow, m.nav.histOffset = 0, false, 0
	return m, nil
}

func (m Model) navDown() (Model, tea.Cmd) {
	rows := m.rows()
	if m.nav.cursor < len(rows)-1 {
		m.nav.cursor++
		if m.nav.cursor == len(rows)-1 {
			m.nav.follow = true
		}
	}
	return m, nil
}

func (m Model) scrollViewport(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	if key.Matches(msg, keymap.input.pageUp) {
		m.output.HalfPageUp()
	} else {
		m.output.HalfPageDown()
	}
	return m, nil
}

// toggleFocus cycles input → history → output → input. Arrows act in
// whichever pane is focused.
func (m Model) toggleFocus() (Model, tea.Cmd) {
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

// toggleExpand opens or shuts a row's preview, outside apply, so it
// marks the row's block and history itself.
func (m *Model) toggleExpand(r *historyRow) {
	r.expanded = !r.expanded
	for _, b := range m.blocks {
		if slices.Contains(b.rows, r) {
			b.rev++
		}
	}
	m.histRev++
}
