package ui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/event"
)

// Everything the human can ask for, each one published intent. What
// comes back arrives as a fact, through apply.

// submit sends the prompt. Typed while a request runs it is steering,
// not a new request, and the engine decides that, not this.
func (m Model) submit() (Model, tea.Cmd) {
	text := strings.TrimSpace(m.prompt.Value())
	if text == "" {
		return m, nil
	}
	m.prompt.Clear()
	m.clearNotice()
	if strings.HasPrefix(text, "/") {
		return m.runSlash(text)
	}
	// A skill named mid-sentence is loaded first, and the sentence is the request.
	if named := skillsNamed(text, m.skillCmds); len(named) > 0 {
		text = loadSkills(named) + " The request: " + text
	}
	return m.sendPrompt(text)
}

// sendPrompt hands text to the engine as a request, or as steering.
func (m Model) sendPrompt(text string) (Model, tea.Cmd) {
	// cur is nil between Turns, so it says whether the engine will read
	// this as a note or start a Turn with it.
	if m.cur == nil {
		m.waiting = true
	} else {
		m.noteOK("steering the current request")
	}
	m.send(event.SubmitPrompt{Text: text})
	return m, m.spinner.Tick
}

// runShell sends what was typed to the shell. No Turn opens, and the
// model only reads it afterwards.
func (m Model) runShell() (Model, tea.Cmd) {
	text := strings.TrimSpace(m.prompt.Value())
	if text == "" {
		return m, nil
	}
	m.prompt.Clear()
	m.clearNotice()
	m.send(event.RunCommand{Text: text})
	return m, nil
}

func (m Model) approve() (Model, tea.Cmd) { return m.answerApproval(true) }
func (m Model) decline() (Model, tea.Cmd) { return m.answerApproval(false) }

func (m Model) answerApproval(yes bool) (Model, tea.Cmd) {
	a := m.asking()
	if a == nil {
		m.mode = modeInput
		return m, nil
	}
	call := a.ToolCall
	m.unask(call)
	m.backToInput()
	// Still asking if another question was queued behind this one.
	m.waiting = m.asking() == nil
	m.send(event.ResolveApproval{ToolCall: call, Approved: yes})
	return m, m.spinner.Tick
}

func (m Model) answerBound(keepGoing bool) (Model, tea.Cmd) {
	if m.bound == nil {
		m.mode = modeInput
		return m, nil
	}
	turn := m.bound.Turn
	m.bound = nil
	m.backToInput()
	m.waiting = keepGoing
	m.send(event.Continue{Turn: turn, Approved: keepGoing})
	return m, m.spinner.Tick
}

// listSessions opens the panel and asks for a fresh listing, since
// another detent may have recorded one since this started.
func (m Model) listSessions(string) (Model, tea.Cmd) {
	next, _ := m.openPanel(panelSessions)
	m.send(event.ListSessions{})
	return next, nil
}

// listServers asks for the page afresh each time, since a stale one is
// worse than a blank frame. /mcp auth <server> asks for a new sign-in.
func (m Model) listServers(input string) (Model, tea.Cmd) {
	args := strings.Fields(strings.TrimPrefix(input, "/mcp"))
	if len(args) == 0 {
		next, _ := m.openPanel(panelMCP)
		m.send(event.ListServers{})
		return next, nil
	}
	if len(args) != 2 || args[0] != "auth" {
		m.noteErr("usage: /mcp, or /mcp auth <server>")
		return m, nil
	}
	if !m.knowsServer(args[1]) {
		m.noteErr(args[1] + " is not a configured MCP server")
		return m, nil
	}
	m.noteOK("asking " + args[1] + " for a new link")
	m.send(event.AuthorizeServer{Server: args[1]})
	return m, nil
}

// knowsServer reports whether the last listing named server. Whether it
// can sign in is internal/mcp's to say: any reached server can.
func (m Model) knowsServer(server string) bool {
	return slices.ContainsFunc(m.servers, func(s event.ServerSummary) bool { return s.Name == server })
}

// renameSession names this run so a listing shows something a human
// recognises rather than a uuid.
func (m Model) renameSession(input string) (Model, tea.Cmd) {
	name := strings.TrimSpace(strings.TrimPrefix(input, "/rename"))
	if name == "" {
		m.noteErr("usage: /rename <name>")
		return m, nil
	}
	if !m.run.Recorded {
		m.noteErr("nothing is recording this session, so a name would not keep")
		return m, nil
	}
	// No claim of success: only the store knows whether the name took,
	// and it says so either way.
	m.send(event.RenameSession{Session: m.run.Session, Name: name})
	return m, nil
}

// onQuit asks first when a request is running, because one keystroke
// is thin to rest abandoning it on. repeat is what they just pressed.
func (m Model) onQuit(repeat string) (Model, tea.Cmd) {
	if m.cur == nil || m.quitArmed {
		return m, tea.Quit
	}
	m.quitArmed = true
	m.noteErr("a request is running: " + repeat + " again to quit, or /abort it")
	return m, nil
}

// abortRunning stops the open request. Cancels in flight, unlike a
// decline, which stops one call and lets the model react.
func (m Model) abortRunning() (Model, tea.Cmd) {
	if m.cur == nil {
		m.noteErr("nothing is running")
		return m, nil
	}
	m.send(event.Abort{Turn: m.cur.id})
	return m, nil
}

// startOver asks for a new session. History clears when it starts, and the
// container keeps running: the conversation and the environment differ.
func (m Model) startOver() (Model, tea.Cmd) {
	m.backToInput()
	m.send(event.ResetSession{})
	return m, nil
}
