package ui

import (
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/termsafe"
)

// The inspector: one subagent's work over the panes. Its question is answered
// here with every guard the approval box has, the settle and the whole command read.

// inspectorModal is the subagent being looked into and where in its work.
type inspectorModal struct {
	agent  *agentState
	cursor int
	// output is the left pane's scroll, and outputFocused that arrows move it.
	output        int
	outputFocused bool
	// shown is the question on screen and when it appeared, so a key typed
	// before it did is not an answer, and seenEnd whether all of it was read.
	shown   uuid.UUID
	shownAt time.Time
	seenEnd bool
	// back is the pane it was opened from, which esc returns to.
	back focusPane
}

// openInspector shows a's work, on its waiting call when it has one, else
// on its report once done, else on its newest row.
func (m Model) openInspector(a *agentState) (Model, tea.Cmd) {
	if a == nil {
		return m, nil
	}
	in := &inspectorModal{agent: a, back: m.nav.focus}
	// Stepping to another agent keeps the pane the first was opened from.
	if was := modalAs[*inspectorModal](m); was != nil {
		in.back = was.back
	}
	rows := m.inspectorRows(a)
	in.cursor = len(rows) - 1
	if i := slices.IndexFunc(rows, func(r *historyRow) bool { return m.askOf(r) != nil }); i >= 0 {
		in.cursor = i
	}
	m.openModal(in)
	in.sync(&m)
	return m, nil
}

// showAgents is /agents: the inspector on the named subagent, else one waiting
// on the human, else the newest. Agents of a resumed session are there too.
func (m Model) showAgents(input string) (Model, tea.Cmd) {
	if len(m.agentOrder) == 0 {
		m.noteErr("no subagents in this session")
		return m, nil
	}
	name := strings.TrimSpace(strings.TrimPrefix(input, "/agents"))
	a := m.agentOrder[len(m.agentOrder)-1]
	if i := slices.IndexFunc(m.agentOrder, m.blocked); i >= 0 {
		a = m.agentOrder[i]
	}
	if name != "" {
		a = nil
		// The newest of that name, since a request may reuse one.
		for _, b := range slices.Backward(m.agentOrder) {
			if b.name == name {
				a = b
				break
			}
		}
		if a == nil {
			m.noteErr("no subagent named " + name)
			return m, nil
		}
	}
	return m.openInspector(a)
}

func (in *inspectorModal) key(m *Model, msg tea.KeyPressMsg) tea.Cmd {
	rows := m.inspectorRows(in.agent)
	k := keymap.inspector
	switch {
	case key.Matches(msg, k.close):
		m.closeModal(in.back)
		return nil
	case key.Matches(msg, k.pane):
		in.outputFocused = !in.outputFocused
	case key.Matches(msg, k.move.pageUp, k.move.pageDown):
		d, _ := k.move.delta(msg, m.modalPaneHeight())
		in.scrollBy(*m, d)
	case key.Matches(msg, k.prevAgent):
		in.step(m, -1)
	case key.Matches(msg, k.nextAgent):
		in.step(m, 1)
	case key.Matches(msg, k.stop):
		*m, _ = m.stopAgent(in.agent)
	case key.Matches(msg, k.yes):
		in.answer(m, true)
	case key.Matches(msg, k.no):
		in.answer(m, false)
	default:
		d, ok := k.move.delta(msg, 0)
		switch {
		case !ok:
		case in.outputFocused:
			in.scrollBy(*m, d)
		default:
			in.cursor = min(max(in.cursor+d, 0), len(rows)-1)
			in.output = 0
		}
	}
	return nil
}

// sync notices a question coming on screen, which starts its settle and its
// reading afresh, and whether all of it already fits.
func (in *inspectorModal) sync(m *Model) {
	q := m.askOf(in.row(*m))
	if q == nil {
		in.shown = uuid.Nil
		return
	}
	if q.ToolCall != in.shown {
		in.shown, in.shownAt, in.seenEnd = q.ToolCall, time.Now(), false
		in.output = 0
	}
	if len(in.outputLines(*m)) <= m.modalPaneHeight() {
		in.seenEnd = true
	}
}

func (in *inspectorModal) hint(Model) string {
	return barLine(does("back", keymap.inspector.close), note("the agent's keys are in the box"))
}

// step moves to the agent started d before or after this one.
func (in *inspectorModal) step(m *Model, d int) {
	i := slices.Index(m.agentOrder, in.agent) + d
	if i < 0 || i >= len(m.agentOrder) {
		return
	}
	*m, _ = m.openInspector(m.agentOrder[i])
}

// answer answers the selected row's question, once it has settled and every
// line of its command has been on screen.
func (in *inspectorModal) answer(m *Model, yes bool) {
	q := m.askOf(in.row(*m))
	if q == nil || time.Since(in.shownAt) < questionSettle {
		return
	}
	if yes && !in.seenEnd {
		m.noteErr("read to the end of the command first: tab, then ↓ scrolls it")
		return
	}
	call := q.ToolCall
	m.unask(call)
	in.sync(m)
	m.send(event.ResolveApproval{ToolCall: call, Approved: yes})
}

func (in *inspectorModal) row(m Model) *historyRow {
	rows := m.inspectorRows(in.agent)
	if len(rows) == 0 {
		return nil
	}
	return rows[min(max(in.cursor, 0), len(rows)-1)]
}

// scrollBy moves the output pane, marking a question read once its last line
// has been on screen.
func (in *inspectorModal) scrollBy(m Model, d int) {
	lines, h := len(in.outputLines(m)), m.modalPaneHeight()
	in.output = min(max(in.output+d, 0), max(0, lines-h))
	if in.output+h >= lines {
		in.seenEnd = true
	}
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
	a.stopping = true
	m.touch(a)
	m.send(event.StopAgent{Agent: a.id})
	return m, nil
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

// inspectorCommand is a waiting call as it will run, defused.
func inspectorCommand(q *event.ApprovalAsked) string {
	return termsafe.Printable(event.Command(q.Tool, q.Args))
}
