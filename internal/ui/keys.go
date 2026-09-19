package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/internal/ui/slash"
	"github.com/vitzeno/detent/internal/ui/tabular"
)

// keyOwner names who owns a keystroke. mode × focus × waiting collapse
// to five real states; handleKey computes exactly one and switches on
// it, so priority is visible in one place instead of emerging from
// nested ifs.
type keyOwner int

const (
	ownerConfirm keyOwner = iota
	ownerInput            // idle typing, slash dropdown included
	ownerBusy             // waiting: slash entry only
	ownerOutput
	ownerHistory
)

func (m Model) owner() keyOwner {
	if m.mode == modeConfirm {
		return ownerConfirm
	}
	if m.focus == focusOutput {
		return ownerOutput
	}
	if m.focus == focusHistory || !m.input.Focused() {
		return ownerHistory
	}
	if m.waiting {
		return ownerBusy
	}
	return ownerInput
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global first: these preempt every state.
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		return m.onEscape()
	case "tab":
		return m.onTab()
	}
	switch m.owner() {
	case ownerConfirm:
		return m.confirmKey(msg)
	case ownerInput:
		return m.inputKey(msg)
	case ownerBusy:
		return m.busyKey(msg)
	case ownerOutput:
		return m.outputKey(msg)
	default:
		return m.historyKey(msg)
	}
}

// onTab completes an open dropdown, otherwise cycles panes. One place —
// tab racing two nest levels was a real bug here before the router.
func (m Model) onTab() (tea.Model, tea.Cmd) {
	if m.mode == modeConfirm {
		return m, nil
	}
	if m.focus == focusInput && m.mode == modeInput && m.input.Focused() && len(m.slash) > 0 {
		return m.acceptSlash()
	}
	return m.toggleFocus()
}

func (m Model) confirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		return m.approve()
	case "n", "N":
		return m.decline()
	}
	return m, nil
}

// inputKey gives a focused idle input every keystroke — typing a goal
// containing "q", "v", "j", "k" or space must never trigger navigation.
// Only arrows/pgup/pgdn (never text) and enter (submit) bypass it.
func (m Model) inputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if next, _, handled := m.slashKey(msg); handled {
		return next, nil
	}
	switch msg.String() {
	case "enter":
		return m.startGoal()
	case "up":
		return m.navUp()
	case "down":
		return m.navDown()
	case "pgup", "pgdown":
		return m.scrollViewport(msg.String())
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.notice = ""
	m.updateSlash()
	return m, cmd
}

// busyKey narrows a focused waiting input to slash entry: plain goals
// can't start mid-run, but /abort and /quit stay reachable — exactly
// when they matter most.
func (m Model) busyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if next, _, handled := m.slashKey(msg); handled {
		return next, nil
	}
	switch msg.String() {
	case "up":
		return m.navUp()
	case "down":
		return m.navDown()
	case "pgup", "pgdown":
		return m.scrollViewport(msg.String())
	}
	// Busy with empty input: only "/" may start slash entry.
	// With a "/" prefix already typed, all keys go to the input.
	if strings.HasPrefix(m.input.Value(), "/") ||
		(msg.String() == "/" && m.input.Value() == "") {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.notice = ""
		m.updateSlash()
		return m, cmd
	}
	return m, nil
}

// slashKey drives the open dropdown (arrows/enter/esc); tab is handled
// in onTab. Reports handled=false when no dropdown is open.
func (m Model) slashKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if len(m.slash) == 0 {
		return m, nil, false
	}
	switch msg.String() {
	case "up":
		if m.slashCursor > 0 {
			m.slashCursor--
		}
		return m, nil, true
	case "down":
		if m.slashCursor < len(m.slash)-1 {
			m.slashCursor++
		}
		return m, nil, true
	case "enter":
		if !slash.Exact(m.input.Value()) {
			next, cmd := m.acceptSlash()
			return next, cmd, true
		}
		next, cmd := m.startGoal()
		return next, cmd, true
	case "esc":
		m.slash = nil
		m.sizeViewport()
		return m, nil, true
	}
	return m, nil, false
}

// outputKey acts inside the detail component instead of moving rows.
func (m Model) outputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		return m.outputNav(-1)
	case "down", "j":
		return m.outputNav(1)
	case "pgup":
		m.output.HalfViewUp()
		return m, nil
	case "pgdown":
		m.output.HalfViewDown()
		return m, nil
	case "enter", "v", " ":
		if r := m.focused(); r != nil {
			r.expanded = !r.expanded
			m.refreshViewport()
		}
		return m, nil
	case "q":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.output, cmd = m.output.Update(msg)
	return m, cmd
}

func (m Model) historyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		return m.navUp()
	case "down", "j":
		return m.navDown()
	case "pgup", "pgdown":
		return m.scrollViewport(msg.String())
	case "enter":
		if r := m.focused(); r != nil && !r.running {
			r.expanded = !r.expanded
			m.refreshViewport()
		}
		return m, nil
	case "v", " ":
		if r := m.focused(); r != nil {
			r.expanded = !r.expanded
			m.refreshViewport()
		}
		return m, nil
	case "q":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.output, cmd = m.output.Update(msg)
	return m, cmd
}

// outputNav moves inside the detail component: the table cursor for
// tabular rows, viewport lines otherwise.
func (m Model) outputNav(d int) (tea.Model, tea.Cmd) {
	if r := m.focused(); r != nil && !r.running {
		if src, ok := r.tableText(); ok {
			if _, rows, ok := tabular.Parse(src, m.output.Width); ok && len(rows) > 0 {
				r.tableCursor = min(max(r.tableCursor+d, 0), len(rows)-1)
				return m, nil
			}
		}
	}
	if d < 0 {
		m.output.LineUp(1)
	} else {
		m.output.LineDown(1)
	}
	return m, nil
}

func (m Model) navUp() (tea.Model, tea.Cmd) {
	if m.cursor > 0 {
		m.cursor--
		m.follow = false
	}
	m.refreshViewport()
	return m, nil
}

func (m Model) navDown() (tea.Model, tea.Cmd) {
	rows := m.rows()
	if m.cursor < len(rows)-1 {
		m.cursor++
		if m.cursor == len(rows)-1 {
			m.follow = true
		}
	}
	m.refreshViewport()
	return m, nil
}

func (m Model) scrollViewport(key string) (tea.Model, tea.Cmd) {
	if key == "pgup" {
		m.output.HalfViewUp()
	} else {
		m.output.HalfViewDown()
	}
	return m, nil
}

// toggleFocus cycles input → history → output → input. Arrows act in
// whichever pane is focused.
func (m Model) toggleFocus() (tea.Model, tea.Cmd) {
	switch m.focus {
	case focusInput:
		m.focus = focusHistory
		m.input.Blur()
	case focusHistory:
		m.focus = focusOutput
	default:
		m.focus = focusInput
		m.input.Focus()
	}
	return m, nil
}

func (m Model) onEscape() (tea.Model, tea.Cmd) {
	if m.mode == modeConfirm {
		return m.decline()
	}
	// Idle esc in the output pane steps back to history; a running
	// command still aborts.
	if m.focus == focusOutput && m.abort == nil {
		m.focus = focusHistory
		return m, nil
	}
	if m.abort != nil {
		m.abort()
		m.abort = nil
	}
	return m, nil
}
