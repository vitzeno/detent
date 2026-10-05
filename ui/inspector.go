package ui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/termsafe"
)

// The inspector: one subagent's work laid over the panes, the way the
// finder is. Its question can be answered here, with every guard the
// approval box has: the settle and the whole command read.

// openInspector shows a's work, on its waiting call when it has one, else
// on its report once done, else on its newest row.
func (m Model) openInspector(a *agentState) (Model, tea.Cmd) {
	if a == nil {
		return m, nil
	}
	m.insp = inspectorState{agent: a}
	rows := m.inspectorRows(a)
	m.insp.cursor = len(rows) - 1
	if i := slices.IndexFunc(rows, func(r *historyRow) bool { return m.askOf(r) != nil }); i >= 0 {
		m.insp.cursor = i
	}
	m.mode = modeInspector
	m.syncInspector()
	return m, nil
}

// closeInspector puts back whatever question waited for it to close.
func (m Model) closeInspector() (Model, tea.Cmd) {
	m.insp = inspectorState{}
	m.backToInput()
	return m, nil
}

// inspectorKey owns every key while the inspector is open.
func (m Model) inspectorKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	rows := m.inspectorRows(m.insp.agent)
	switch msg.String() {
	case "esc":
		return m.closeInspector()
	case "tab":
		m.insp.outputFocused = !m.insp.outputFocused
	case "up", "down":
		d := 1
		if msg.String() == "up" {
			d = -1
		}
		if m.insp.outputFocused {
			m.scrollInspector(d)
			break
		}
		m.insp.cursor = min(max(m.insp.cursor+d, 0), len(rows)-1)
		m.insp.output = 0
	case "pgup":
		m.scrollInspector(-m.inspectorHeight())
	case "pgdown":
		m.scrollInspector(m.inspectorHeight())
	case "left", "right":
		return m.nextAgent(msg.String() == "right")
	case "x":
		return m.stopAgent(m.insp.agent)
	case "y", "Y", "enter":
		return m.answerInInspector(true)
	case "n", "N":
		return m.answerInInspector(false)
	}
	m.syncInspector()
	return m, nil
}

// nextAgent moves to the agent started before or after this one.
func (m Model) nextAgent(forward bool) (Model, tea.Cmd) {
	i := slices.Index(m.agentOrder, m.insp.agent)
	if forward {
		i++
	} else {
		i--
	}
	if i < 0 || i >= len(m.agentOrder) {
		return m, nil
	}
	return m.openInspector(m.agentOrder[i])
}

// answerInInspector answers the selected row's question, once it has settled
// and every line of its command has been on screen.
func (m Model) answerInInspector(yes bool) (Model, tea.Cmd) {
	q := m.askOf(m.inspectorRow())
	if q == nil || time.Since(m.insp.shownAt) < questionSettle {
		return m, nil
	}
	if yes && !m.insp.seenEnd {
		m.noteErr("read to the end of the command first: tab, then ↓ scrolls it")
		return m, nil
	}
	call := q.ToolCall
	m.unask(call)
	m.syncInspector()
	return m, m.send(event.ResolveApproval{ToolCall: call, Approved: yes})
}

// stopAgent stops a subagent, asked twice so a stray key keeps its work.
func (m Model) stopAgent(a *agentState) (Model, tea.Cmd) {
	if a == nil || a.ended {
		return m, nil
	}
	if m.stopArmed != a.id {
		m.stopArmed = a.id
		m.noteErr("x again to stop " + a.name + ", which still reports what it found")
		return m, nil
	}
	m.stopArmed = uuid.Nil
	return m, m.send(event.StopAgent{Agent: a.id})
}

// inspectorRows is the agent's task, its own rows, and its report once done.
func (m Model) inspectorRows(a *agentState) []*historyRow {
	if a == nil {
		return nil
	}
	rows := append([]*historyRow{{prose: "**Task**\n\n" + a.task}}, a.rows...)
	if a.ended && a.spawn != nil && a.spawn.result != nil {
		rows = append(rows, a.spawn)
	}
	return rows
}

// inspectorRow is the selected row.
func (m Model) inspectorRow() *historyRow {
	rows := m.inspectorRows(m.insp.agent)
	if len(rows) == 0 {
		return nil
	}
	return rows[min(max(m.insp.cursor, 0), len(rows)-1)]
}

// askOf is the question a row's call is waiting on, nil for none.
func (m Model) askOf(r *historyRow) *event.ApprovalAsked {
	if r == nil || r.id == uuid.Nil {
		return nil
	}
	i := slices.IndexFunc(m.childAsks, func(q event.ApprovalAsked) bool { return q.ToolCall == r.id })
	if i < 0 {
		return nil
	}
	return &m.childAsks[i]
}

// syncInspector notices a question coming on screen, which starts its settle
// and its reading afresh, and whether all of it already fits.
func (m *Model) syncInspector() {
	if m.mode != modeInspector {
		return
	}
	q := m.askOf(m.inspectorRow())
	if q == nil {
		m.insp.shown = uuid.Nil
		return
	}
	if q.ToolCall != m.insp.shown {
		m.insp.shown, m.insp.shownAt, m.insp.seenEnd = q.ToolCall, time.Now(), false
		m.insp.output = 0
	}
	if len(m.inspectorOutput()) <= m.inspectorHeight() {
		m.insp.seenEnd = true
	}
}

// scrollInspector moves the output pane, marking a question read once its
// last line has been on screen.
func (m *Model) scrollInspector(d int) {
	lines, h := len(m.inspectorOutput()), m.inspectorHeight()
	m.insp.output = min(max(m.insp.output+d, 0), max(0, lines-h))
	if m.insp.output+h >= lines {
		m.insp.seenEnd = true
	}
}

// inspectorCommand is a waiting call as it will run, defused.
func inspectorCommand(q *event.ApprovalAsked) string {
	return termsafe.Printable(event.Command(q.Tool, q.Args))
}
