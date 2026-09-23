package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/event"
)

// Everything the human can ask for, each one published intent. What
// comes back arrives as a fact, through apply.

// submit sends the prompt. Typed while a request runs it is steering,
// not a new request — the engine makes that call, not this.
func (m Model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.prompt.Value())
	if text == "" {
		return m, nil
	}
	m.prompt.Clear()
	m.clearNotice()
	if strings.HasPrefix(text, "/") {
		return m.runSlash(text)
	}
	if m.cur == nil {
		m.waiting = true
	}
	return m, tea.Batch(m.spinner.Tick, m.send(event.SubmitPrompt{Text: text}))
}

func (m Model) approve() (tea.Model, tea.Cmd) { return m.answerApproval(true) }
func (m Model) decline() (tea.Model, tea.Cmd) { return m.answerApproval(false) }

func (m Model) answerApproval(yes bool) (tea.Model, tea.Cmd) {
	if m.asking == nil {
		m.mode = modeInput
		return m, nil
	}
	call := m.asking.Call
	m.asking = nil
	m.backToInput()
	m.waiting = true
	return m, tea.Batch(m.spinner.Tick, m.send(event.ResolveApproval{Call: call, Approved: yes}))
}

func (m Model) answerBound(keepGoing bool) (tea.Model, tea.Cmd) {
	if m.bound == nil {
		m.mode = modeInput
		return m, nil
	}
	turn := m.bound.Turn
	m.bound = nil
	m.backToInput()
	m.waiting = keepGoing
	return m, tea.Batch(m.spinner.Tick, m.send(event.Continue{Turn: turn, Approved: keepGoing}))
}

// abortRunning stops the open request. Cancels in flight, unlike a
// decline, which stops one call and lets the model react.
func (m Model) abortRunning() (tea.Model, tea.Cmd) {
	if m.cur == nil {
		m.noteErr("nothing is running")
		return m, nil
	}
	return m, m.send(event.Abort{Turn: m.cur.id})
}

// startOver forgets the transcript. The container keeps running: the
// conversation and the environment are different things.
func (m Model) startOver() (tea.Model, tea.Cmd) {
	m.blocks, m.cur = nil, nil
	m.nav = navState{follow: true}
	m.calls, m.steps, m.errors, m.views, m.tokens = 0, 0, 0, 0, 0
	m.backToInput()
	return m, m.send(event.ResetSession{})
}
