package ui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/termsafe"
	"github.com/vitzeno/detent/ui/status"
)

// maxLiveLines bounds what one running call keeps on screen. The rest
// is counted away and the full output arrives with ToolCallEnded.
const maxLiveLines = 200

// Restore rebuilds history from a stored session by looping over apply.
func (m Model) Restore(records []event.Record) Model {
	m.replaying = true
	for _, r := range records {
		m.apply(r.Event)
	}
	m.replaying = false
	m.staleSignIns()
	m.followNewest()
	// Nothing waits on a question the old process asked.
	m.asked, m.childAsks, m.bound = nil, nil, nil
	m.backToInput()
	return m
}

// apply folds one fact into the Model, and is the whole of how the UI
// learns anything. A test drives it with events and no harness.
func (m *Model) apply(ev event.Event) {
	// Any fact may change what history assembles. A block's own drawing
	// goes stale only through the lookups below, which bump its rev.
	m.histRev++
	switch v := ev.(type) {
	case event.SessionStarted:
		// Another id is /new or /resume: the old session stays stored, and history starts again.
		if m.run.Session != uuid.Nil && v.Session != m.run.Session {
			m.clearHistory()
			m.resumed(v)
		}
		m.run = v
		m.skillCmds = skillCommands(v.Skills)
		m.prompt.SetExtraCommands(m.skillCmds)

	case event.SessionsListed:
		m.sessions = v.Sessions

	case event.SessionLoaded:
		m.loaded(v)

	case event.DiffLoaded:
		m.diffLoaded(v)

	case event.ServersListed:
		m.servers = v.Servers

	case event.ContextMeasured:
		m.measured = v

	case event.TurnStarted:
		b := &turnBlock{id: v.Turn, n: v.N, prompt: v.Prompt}
		m.blocks = append(m.blocks, b)
		m.setCur(b)
		m.waiting = true
		// The human just asked, so they are looking for the answer.
		m.followNewest()

	case event.CheckpointTaken:
		b := m.block(v.Turn)
		if b == nil {
			break
		}
		b.base = v.Tree
		// Replayed, its snapshot died with the container, so there is nothing to undo.
		if !m.replaying {
			b.undoable = true
			b.files, b.container = v.Tree != "", v.Snapshot != ""
		}

	case event.TurnEnded:
		m.endTurn(v)

	case event.BoundReached:
		m.bound = &v
		m.waiting = false
		m.raise(modeBound)

	case event.AgentStarted:
		m.addAgent(v)

	case event.AgentEnded:
		if a := m.agents[v.Agent]; a != nil {
			a.ended, a.reason = true, v.Reason
			m.touch(a)
		}

	case event.StepStarted:
		if a := m.agents[v.Agent]; a != nil {
			a.steps++
			m.touch(a)
		}

	case event.ModelText:
		if a := m.agents[v.Agent]; a != nil {
			a.rows = append(a.rows, &historyRow{prose: termsafe.Printable(v.Text)})
			m.touch(a)
			return
		}
		m.addProse(v.Text)

	case event.ToolCallProposed:
		if a := m.agents[v.Agent]; a != nil {
			r := newCallRow(v)
			r.child = true
			a.rows = append(a.rows, r)
			a.calls++
			a.last = termsafe.Printable(event.Command(v.Tool, v.Args))
			m.touch(a)
			return
		}
		m.addToolCall(v)

	case event.ToolCallAssessed:
		if r := m.row(v.ToolCall); r != nil {
			r.risk = v.Risk
		}

	case event.ApprovalAsked:
		// A child waiting on the human shows on its row and in the agents block.
		if a := m.agents[v.Agent]; a != nil {
			m.childAsks = append(m.childAsks, v)
			if a.waitingSince.IsZero() {
				a.waitingSince = time.Now()
			}
			m.touch(a)
			return
		}
		// One arriving behind the shown question leaves its scroll and settle alone.
		m.asked = append(m.asked, v)
		if len(m.asked) == 1 {
			m.confirm = confirmState{}
			m.waiting = false
			m.raise(modeConfirm)
		}

	case event.ToolCallStarted:
		if r := m.row(v.ToolCall); r != nil {
			r.running = true
		}

	case event.OutputChunk:
		m.addLine(v)

	case event.ToolCallEnded:
		// A call can end unanswered, by an abort, and its question with it.
		m.unask(v.ToolCall)
		if r, a := m.rowOf(v.ToolCall); r != nil {
			r.running = false
			result := styled(v.Result)
			r.result, r.took = &result, v.Took
			r.created = createdDiff(r.wrote, result)
			r.wrote = nil
			// The counters are the root's. A child's work shows in its own figures.
			if a == nil {
				m.calls++
				if result.Err != "" || result.ExitCode != 0 {
					m.errors++
				}
			}
		}

	case event.SessionResumed:
		m.blocks = append(m.blocks, &turnBlock{seam: &v})

	case event.UserCommandStarted:
		m.addUserCommand(v)

	case event.UserCommandEnded:
		if r := m.row(v.UserCommand); r != nil {
			r.running = false
			result := styled(v.Result)
			r.result = &result
		}

	case event.ToolCallJudged:
		m.judged(v)

	case event.ViewReady:
		m.viewReady(v)

	case event.AuthorizationWaiting:
		m.addSignIn(v)

	case event.ServerAuthorized:
		if s := m.signInFor(v.Server); s != nil {
			s.stage = stageSignedIn
		}
		m.noteOK("signed in to " + v.Server)

	case event.AuthorizationFailed:
		if s := m.signInFor(v.Server); s != nil {
			s.stage, s.reason = stageFailed, v.Reason
		}

	case event.RolledBack:
		m.rolledBack(v.Turn)

	case event.SessionReset:
		m.clearHistory()

	case event.StepEnded:
		// A child's Steps are its own: the gauge and counters measure the root's
		// context, and TurnEnded's usage already counts the child's spend.
		if a := m.agents[v.Agent]; a != nil {
			a.used = a.used.Add(v.Usage)
			a.ctx = v.Usage.PromptTokens
			m.touch(a)
			return
		}
		m.steps++
		if v.Usage.PromptTokens > 0 {
			m.ctxTokens = v.Usage.PromptTokens
		}

	case event.Compacted:
		// The next Step measures the new size. Until then the old
		// reading is stale and would overstate the budget.
		m.ctxTokens = 0
		m.noteOK(fmt.Sprintf("compacted, %d messages summarised", v.Dropped))

	case event.Notice:
		m.noteLevel(v.Level, v.Text)
	}
}

func (m *Model) endTurn(v event.TurnEnded) {
	// The block may be gone already, but the Turn still ended and
	// nothing may be left waiting on it.
	if b := m.block(v.Turn); b != nil {
		b.ended, b.end, b.summary, b.used, b.tree = true, v.Reason, v.Summary, v.Usage, v.Tree
		if v.Reason == event.EndError {
			b.err = v.Summary
		}
	}
	m.setCur(nil)
	m.asked, m.childAsks, m.bound = nil, nil, nil
	m.confirm = confirmState{}
	m.waiting = false
	m.tokens += v.Usage.Tokens()
	if !m.askingOwn() {
		m.backToInput()
	}
}

// addProse gives the model's words a row of their own: it is the
// answer, not a banner nobody can scroll.
func (m *Model) addProse(text string) {
	if m.cur == nil || strings.TrimSpace(text) == "" {
		return
	}
	m.cur.rev++
	// Defused once here, since the pane draws it whole through glamour.
	m.cur.rows = append(m.cur.rows, &historyRow{prose: termsafe.Printable(text)})
	m.trackNewest()
}

func (m *Model) addToolCall(v event.ToolCallProposed) {
	if m.cur == nil {
		return
	}
	m.cur.rev++
	m.cur.rows = append(m.cur.rows, newCallRow(v))
	m.trackNewest()
}

func newCallRow(v event.ToolCallProposed) *historyRow {
	return &historyRow{
		id: v.ToolCall, command: event.Command(v.Tool, v.Args), tool: v.Tool,
		headline: headline(v.Tool, v.Args), wrote: wroteBy(v),
		renders: v.Renders, executor: v.Executor,
	}
}

// addAgent ties a subagent to the spawn row that started it, which draws it.
func (m *Model) addAgent(v event.AgentStarted) {
	a := &agentState{id: v.Agent, name: termsafe.Printable(v.Name), task: termsafe.Printable(v.Task)}
	if r := m.row(v.ToolCall); r != nil {
		r.agent, a.spawn = a, r
	}
	m.agents[v.Agent] = a
	m.agentOrder = append(m.agentOrder, a)
}

// touch redraws the block holding an agent's spawn row, and no other.
func (m *Model) touch(a *agentState) {
	if a.spawn != nil {
		m.row(a.spawn.id)
	}
}

// addUserCommand puts a command in the Turn it interrupted, or its own
// block between Turns: anywhere else draws it out of order.
func (m *Model) addUserCommand(v event.UserCommandStarted) {
	b := m.cur
	if b == nil {
		b = m.userCommandBlock()
	}
	b.rev++
	b.rows = append(b.rows, &historyRow{
		id: v.UserCommand, command: v.Command, human: true, running: true,
	})
	m.trackNewest()
}

// userCommandBlock is where commands land between Turns. Reused while it is
// the newest, so a burst of them reads as one sitting.
func (m *Model) userCommandBlock() *turnBlock {
	if n := len(m.blocks); n > 0 && m.blocks[n-1].userCommands {
		return m.blocks[n-1]
	}
	b := &turnBlock{userCommands: true}
	m.blocks = append(m.blocks, b)
	return b
}

func (m *Model) addLine(v event.OutputChunk) {
	r := m.row(v.Owner())
	if r == nil {
		return
	}
	if len(r.live) >= maxLiveLines {
		r.dropped++
		r.live = r.live[1:]
	}
	r.live = append(r.live, termsafe.Styled(v.Line))
}

func (m *Model) judged(v event.ToolCallJudged) {
	r := m.row(v.ToolCall)
	if r == nil {
		return
	}
	r.post = &verdict{status: v.Status, renderKind: v.RenderKind,
		attention: v.Attention, fromJudge: v.FromJudge}
	if v.Attention >= status.AttentionThreshold {
		r.expanded = true
	}
}

func (m *Model) viewReady(v event.ViewReady) {
	r := m.row(v.Owner())
	if r == nil || v.Spec == nil {
		return
	}
	if bound, ok := bindSpec(*v.Spec, r.text()); ok {
		r.view, r.viewTried, r.viewSource = bound, true, v.Source
		m.views++
	}
}

func (m *Model) rolledBack(id uuid.UUID) {
	for i, b := range m.blocks {
		if b.id == id {
			m.blocks = m.blocks[:i]
			break
		}
	}
	// Their agents went with their spawn rows, so nothing stale can be inspected.
	kept := m.rows()
	gone := func(a *agentState) bool { return !slices.Contains(kept, a.spawn) }
	maps.DeleteFunc(m.agents, func(_ uuid.UUID, a *agentState) bool { return gone(a) })
	m.agentOrder = slices.DeleteFunc(m.agentOrder, gone)
	m.nav.cursor = min(m.nav.cursor, max(0, len(m.rows())-1))
	m.noteOK("undone")
}

// clearHistory forgets every block, as the engine forgot the transcript.
func (m *Model) clearHistory() {
	m.blocks, m.cur = nil, nil
	clear(m.agents)
	m.agentOrder = nil
	m.nav = navState{follow: true}
	m.calls, m.steps, m.errors, m.views, m.tokens = 0, 0, 0, 0, 0
	m.ctxTokens = 0
}

// block and row find what an event is about and mark its block to redraw.
// Linear: a map would have to be kept in step with the slice that draws them.
func (m *Model) block(id uuid.UUID) *turnBlock {
	if id == uuid.Nil {
		return nil
	}
	for _, b := range m.blocks {
		if b.id == id {
			b.rev++
			return b
		}
	}
	return nil
}

func (m *Model) row(id uuid.UUID) *historyRow {
	r, _ := m.rowOf(id)
	return r
}

// rowOf also says which subagent a row is, nil for history's own. A child's
// row redraws its spawn row's block, which draws it.
func (m *Model) rowOf(id uuid.UUID) (*historyRow, *agentState) {
	if id == uuid.Nil {
		return nil, nil
	}
	for i := len(m.blocks) - 1; i >= 0; i-- {
		for _, r := range m.blocks[i].rows {
			if r.id == id {
				m.blocks[i].rev++
				return r, nil
			}
		}
	}
	for _, a := range m.agents {
		for _, r := range a.rows {
			if r.id == id {
				m.touch(a)
				return r, a
			}
		}
	}
	return nil, nil
}

// setCur moves the live block. Both ends redraw, since only the live
// one draws the thinking line.
func (m *Model) setCur(b *turnBlock) {
	if m.cur != nil {
		m.cur.rev++
	}
	if b != nil {
		b.rev++
	}
	m.cur = b
}

// styled defuses what a command printed as it arrives, keeping its colour,
// so every view, preview and pane after this draws safe text.
func styled(r event.Result) event.Result {
	r.Stdout, r.Stderr, r.Err = termsafe.Styled(r.Stdout), termsafe.Styled(r.Stderr), termsafe.Printable(r.Err)
	return r
}

// wroteBy keeps a write_file call's content, until its result says whether
// the file it wrote was new.
func wroteBy(v event.ToolCallProposed) *written {
	path, _ := v.Args["path"].(string)
	content, ok := v.Args["content"].(string)
	if v.Tool != "write_file" || !ok || content == "" {
		return nil
	}
	return &written{path: termsafe.Printable(path), content: termsafe.Printable(content)}
}

// createdDiff is a new file's content as a diff, empty for anything else: an
// edit already comes back as one, and a failure is shown as it is.
func createdDiff(w *written, r event.Result) string {
	if w == nil || r.ExitCode != 0 || r.Err != "" || !strings.HasPrefix(r.Stdout, newFileReport) {
		return ""
	}
	return addedDiff(w)
}
