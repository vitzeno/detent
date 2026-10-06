package ui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
)

// The resume picker: stored sessions beside the selected one's history. This
// session needs no saving before enter leaves it, since every fact is stored.

// sessionPreview is a stored session's records and its history drawn from
// them, both empty until the store answers.
type sessionPreview struct {
	id      uuid.UUID
	records []event.Record
	err     string
	model   *Model
}

// showResume is /resume: the picker, on the named session if one is given,
// else the newest that is not this one.
func (m Model) showResume(input string) (Model, tea.Cmd) {
	m.resume = resumeState{back: m.nav.focus, loaded: map[uuid.UUID]*sessionPreview{}}
	m.mode = modeResume
	m.resume.cursor = m.resumeStart(strings.TrimSpace(strings.TrimPrefix(input, "/resume")))
	// Listed afresh, since this session has grown since the last listing.
	m.send(event.ListSessions{})
	m.loadSelected()
	return m, nil
}

// resumeStart is where the cursor opens: the session named by id or name,
// else the first that is not this one.
func (m Model) resumeStart(want string) int {
	if want != "" {
		if i := slices.IndexFunc(m.sessions, func(s event.SessionSummary) bool {
			return strings.EqualFold(s.Name, want) || strings.HasPrefix(s.ID.String(), strings.ToLower(want))
		}); i >= 0 {
			return i
		}
	}
	return max(0, slices.IndexFunc(m.sessions, func(s event.SessionSummary) bool { return s.ID != m.run.Session }))
}

// closeResume returns to the pane the picker was opened from.
func (m Model) closeResume() (Model, tea.Cmd) {
	back := m.resume.back
	m.resume = resumeState{}
	m.backToInput()
	if m.mode == modeInput && back != focusInput {
		m.nav.focus = back
		m.prompt.Blur()
	}
	return m, nil
}

// resumeKey owns every key while the picker is open.
func (m Model) resumeKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.closeResume()
	case "enter":
		return m.pickSession()
	case "tab":
		m.resume.previewFocused = !m.resume.previewFocused
	case "up", "down":
		d := 1
		if msg.String() == "up" {
			d = -1
		}
		if m.resume.previewFocused {
			m.scrollResume(-d)
			break
		}
		m.resume.cursor = min(max(m.resume.cursor+d, 0), max(0, len(m.sessions)-1))
		m.resume.scroll = 0
		m.loadSelected()
		return m, nil
	case "pgup":
		m.scrollResume(m.modalPaneHeight())
	case "pgdown":
		m.scrollResume(-m.modalPaneHeight())
	}
	return m, nil
}

// pickSession resumes the selected session, once its records are in and no
// request is running, since the engine would refuse it then anyway.
func (m Model) pickSession() (Model, tea.Cmd) {
	s := m.selectedSession()
	if s == nil || s.ID == m.run.Session {
		return m.closeResume()
	}
	if m.cur != nil {
		m.noteErr("a request is running: let it finish, or /abort it, then resume")
		return m, nil
	}
	p := m.resume.loaded[s.ID]
	switch {
	case p == nil || p.model == nil && p.err == "":
		m.noteErr("still loading that session")
		return m, nil
	case p.err != "":
		m.noteErr(p.err)
		return m, nil
	}
	m.resuming = p
	next, _ := m.closeResume()
	next.send(event.ResumeSession{Session: s.ID})
	return next, nil
}

// selectedSession is the session under the cursor, nil when none is stored.
func (m Model) selectedSession() *event.SessionSummary {
	if len(m.sessions) == 0 {
		return nil
	}
	return &m.sessions[min(max(m.resume.cursor, 0), len(m.sessions)-1)]
}

// loadSelected asks for the selected session's records, once.
func (m *Model) loadSelected() {
	s := m.selectedSession()
	if m.mode != modeResume || s == nil || m.resume.loaded[s.ID] != nil {
		return
	}
	m.resume.loaded[s.ID] = &sessionPreview{id: s.ID}
	m.send(event.LoadSession{Session: s.ID})
}

// loaded fills in a preview the picker asked for.
func (m *Model) loaded(v event.SessionLoaded) {
	p := m.resume.loaded[v.Session]
	if p == nil {
		return
	}
	p.records, p.err = v.Records, v.Err
	if v.Err == "" {
		p.model = m.previewOf(v.Records)
	}
}

// previewOf is a session's history as its own Model, never wired to the bus.
func (m Model) previewOf(records []event.Record) *Model {
	p := Model{
		info: m.info, workDir: m.workDir,
		prompt: newPrompt(m.info.PowerShell), output: viewport.New(), spinner: m.spinner,
		nav: navState{follow: true}, hist: &histCache{}, agents: map[uuid.UUID]*agentState{},
	}
	p = p.Restore(records)
	return &p
}

// scrollResume moves the preview up from its newest line by d.
func (m *Model) scrollResume(d int) {
	m.resume.scroll = max(m.resume.scroll+d, 0)
}

// resumed swaps history for the session the picker resumed, as its header
// arrives, replaying the records the picker already holds.
func (m *Model) resumed(v event.SessionStarted) {
	p := m.resuming
	m.resuming = nil
	if p == nil || p.id != v.Session {
		return
	}
	// The replay carries the old listing, and this one is newer.
	sessions := m.sessions
	*m = m.Restore(p.records)
	m.sessions = sessions
}
