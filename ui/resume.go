package ui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
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

// resumeModal is the resume picker: which stored session is selected and
// each one's preview, asked for as the cursor first reaches it.
type resumeModal struct {
	cursor int
	// scroll is how far the preview is moved up from its newest line, and
	// previewFocused that arrows move it.
	scroll         int
	previewFocused bool
	loaded         map[uuid.UUID]*sessionPreview
	// back is the pane it was opened from, which esc returns to.
	back focusPane
}

// showResume is /resume: the picker, on the named session if one is given,
// else the newest that is not this one.
func (m Model) showResume(input string) (Model, tea.Cmd) {
	p := &resumeModal{back: m.nav.focus, loaded: map[uuid.UUID]*sessionPreview{}}
	p.cursor = m.resumeStart(strings.TrimSpace(strings.TrimPrefix(input, "/resume")))
	m.openModal(p)
	// Listed afresh, since this session has grown since the last listing.
	m.send(event.ListSessions{})
	p.loadSelected(&m)
	return m, nil
}

func (p *resumeModal) key(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	k := keymap.resume
	switch {
	case key.Matches(msg, k.close):
		m.closeModal(p.back)
	case key.Matches(msg, k.resume):
		p.pick(m)
	case key.Matches(msg, k.pane):
		p.previewFocused = !p.previewFocused
	case key.Matches(msg, k.move.pageUp, k.move.pageDown):
		// The preview's scroll counts up from its newest line.
		d, _ := k.move.delta(msg, m.modalPaneHeight())
		p.scrollBy(*m, -d)
	default:
		d, ok := k.move.delta(msg, 0)
		switch {
		case !ok:
		case p.previewFocused:
			p.scrollBy(*m, -d)
		default:
			p.cursor = min(max(p.cursor+d, 0), max(0, len(m.sessions)-1))
			p.scroll = 0
			p.loadSelected(m)
		}
	}
	return nil
}

// sync asks for the selected session, since a listing may have moved another under the cursor.
func (p *resumeModal) sync(m *Model) { p.loadSelected(m) }

func (p *resumeModal) hint(Model) string {
	k := keymap.resume
	return barLine(does("closes", k.close), does("resume", k.resume), does("move", k.move.up, k.move.down))
}

// pick resumes the selected session, once its records are in and no request
// is running, since the engine would refuse it then anyway.
func (p *resumeModal) pick(m *Model) {
	s := p.selected(*m)
	if s == nil || s.ID == m.run.Session {
		m.closeModal(p.back)
		return
	}
	if m.cur != nil {
		m.noteErr("a request is running: let it finish, or /abort it, then resume")
		return
	}
	pv := p.loaded[s.ID]
	switch {
	case pv == nil || pv.model == nil && pv.err == "":
		m.noteErr("still loading that session")
		return
	case pv.err != "":
		m.noteErr(pv.err)
		return
	}
	m.resuming = pv
	m.closeModal(p.back)
	m.send(event.ResumeSession{Session: s.ID})
}

// selected is the session under the cursor, nil when none is stored.
func (p *resumeModal) selected(m Model) *event.SessionSummary {
	if len(m.sessions) == 0 {
		return nil
	}
	return &m.sessions[min(max(p.cursor, 0), len(m.sessions)-1)]
}

// loadSelected asks for the selected session's records, once.
func (p *resumeModal) loadSelected(m *Model) {
	s := p.selected(*m)
	if s == nil || p.loaded[s.ID] != nil {
		return
	}
	p.loaded[s.ID] = &sessionPreview{id: s.ID}
	m.send(event.LoadSession{Session: s.ID})
}

// scrollBy moves the preview up from its newest line by d.
func (p *resumeModal) scrollBy(m Model, d int) {
	_, right := m.resumePaneWidths()
	top := max(0, len(p.history(m, right))-m.modalPaneHeight())
	p.scroll = min(max(p.scroll+d, 0), top)
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

// loaded fills in a preview the picker asked for.
func (m *Model) loaded(v event.SessionLoaded) {
	p := modalAs[*resumeModal](*m)
	if p == nil || p.loaded[v.Session] == nil {
		return
	}
	pv := p.loaded[v.Session]
	pv.records, pv.err = v.Records, v.Err
	if v.Err == "" {
		pv.model = m.previewOf(v.Records)
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
