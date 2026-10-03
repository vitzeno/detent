package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/event"
)

// Deleting a stored session. What goes is in the pane, the question
// below it defaults to cancel.

// runForget handles /delete <id or name>. The argument is required:
// nothing that cannot come back gets a default.
func (m Model) runForget(input string) (Model, tea.Cmd) {
	arg := strings.TrimSpace(strings.TrimPrefix(input, "/delete"))
	if arg == "" {
		m.noteErr("usage: /delete <session id or name>")
		return m, nil
	}
	s, err := m.forgetTarget(arg)
	if err != "" {
		m.noteErr(err)
		return m, nil
	}
	m.forget.target = s
	m.mode = modeForget
	m.nav.focus = focusOutput
	return m, nil
}

// forgetTarget resolves an id or a name against what was listed.
func (m Model) forgetTarget(arg string) (*event.SessionSummary, string) {
	if len(m.sessions) == 0 {
		return nil, "no sessions listed yet, try /sessions first"
	}
	for i, s := range m.sessions {
		if s.ID.String() == arg || (s.Name != "" && s.Name == arg) {
			if s.ID == m.run.Session {
				return nil, "this is the running session, so it cannot be deleted"
			}
			return &m.sessions[i], ""
		}
	}
	return nil, fmt.Sprintf("no session %q", arg)
}

// confirmForget publishes the intent. The store reports the outcome.
func (m Model) confirmForget() (Model, tea.Cmd) {
	s := m.forget.target
	m.forget.target = nil
	m.backToInput()
	if s == nil {
		return m, nil
	}
	return m, m.send(event.DeleteSession{Session: s.ID})
}

func (m Model) cancelForget() (Model, tea.Cmd) {
	m.forget.target = nil
	m.backToInput()
	return m, nil
}

// forgetLines names what goes: a count would not say what was lost.
func (m *Model) forgetLines() []string {
	s := m.forget.target
	if s == nil {
		return nil
	}
	name := s.Name
	if name == "" {
		name = styleFaint.Render("(unnamed)")
	}
	return []string{
		styleDanger.Render("delete a stored session"), "",
		"  " + styleGoal.Render(s.ID.String()),
		"  " + name,
		"  " + styleFaint.Render(fmt.Sprintf("%s · %s · %s",
			s.Started.Local().Format("2006-01-02 15:04"), s.Model, countOf(s.Events, "event"))),
		"",
		styleDanger.Render("  this removes, and nothing brings it back:"),
		styleFaint.Render("    the stored session and every event in it"),
		styleFaint.Render("    its container and lease, if it still has one"),
		"",
		styleFaint.Render("  its log stays, for diagnostics"),
	}
}
