package ui

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/termsafe"
	"github.com/vitzeno/detent/ui/status"
)

// maxLiveLines bounds what one running call keeps on screen. The rest
// is counted away and the full output arrives with ToolCallEnded.
const maxLiveLines = 200

// Restore rebuilds history from a stored session by looping over apply.
// CheckpointTaken is skipped: its snapshot died with the container.
func (m Model) Restore(records []event.Record) Model {
	m.replaying = true
	for _, r := range records {
		if r.Event.Kind() == event.CheckpointTakenKind {
			continue
		}
		m.apply(r.Event)
	}
	m.replaying = false
	m.staleSignIns()
	m.trackNewest()
	// Nothing waits on a question the old process asked.
	m.asking, m.bound = nil, nil
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
		m.run = v
		m.skillCmds = skillCommands(v.Skills)
		m.prompt.SetExtraCommands(m.skillCmds)

	case event.SessionsListed:
		m.sessions = v.Sessions

	case event.ServersListed:
		m.servers = v.Servers

	case event.ContextMeasured:
		m.measured = v

	case event.TurnStarted:
		b := &turnBlock{id: v.Turn, n: v.N, prompt: v.Prompt}
		m.blocks = append(m.blocks, b)
		m.setCur(b)
		m.waiting = true
		m.trackNewest()

	case event.CheckpointTaken:
		if b := m.block(v.Turn); b != nil {
			b.undoable = true
			b.files, b.container = v.Tree != "", v.Snapshot != ""
		}

	case event.TurnEnded:
		m.endTurn(v)

	case event.BoundReached:
		m.bound = &v
		m.waiting = false
		m.raise(modeBound)

	case event.ModelText:
		m.addProse(v.Text)

	case event.ToolCallProposed:
		m.addToolCall(v)

	case event.ToolCallAssessed:
		if r := m.row(v.ToolCall); r != nil {
			r.risk = v.Risk
		}

	case event.ApprovalAsked:
		m.asking = &v
		m.confirm = confirmState{}
		m.waiting = false
		m.raise(modeConfirm)

	case event.ToolCallStarted:
		if r := m.row(v.ToolCall); r != nil {
			r.running = true
		}

	case event.OutputChunk:
		m.addLine(v)

	case event.ToolCallEnded:
		if r := m.row(v.ToolCall); r != nil {
			r.running = false
			result := v.Result
			r.result = &result
			m.calls++
			if result.Err != "" || result.ExitCode != 0 {
				m.errors++
			}
		}

	case event.SessionResumed:
		m.blocks = append(m.blocks, &turnBlock{seam: &v})

	case event.UserCommandStarted:
		m.addUserCommand(v)

	case event.UserCommandEnded:
		if r := m.row(v.UserCommand); r != nil {
			r.running = false
			result := v.Result
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
		m.steps++
		if v.Usage.PromptTokens > 0 {
			m.context = v.Usage.PromptTokens
		}

	case event.Compacted:
		// The next Step measures the new size. Until then the old
		// reading is stale and would overstate the budget.
		m.context = 0
		m.noteOK(fmt.Sprintf("compacted, %d messages summarised", v.Dropped))

	case event.Notice:
		m.noteLevel(v.Level, v.Text)
	}
}

func (m *Model) endTurn(v event.TurnEnded) {
	// The block may be gone already, but the Turn still ended and
	// nothing may be left waiting on it.
	if b := m.block(v.Turn); b != nil {
		b.ended, b.end, b.summary, b.used = true, v.Reason, v.Summary, v.Usage
		if v.Reason == event.EndError {
			b.err = v.Summary
		}
	}
	m.setCur(nil)
	m.asking, m.bound = nil, nil
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
	m.cur.rows = append(m.cur.rows, &historyRow{
		id: v.ToolCall, command: event.Command(v.Tool, v.Args),
		renders: v.Renders, executor: v.Executor,
	})
	m.trackNewest()
}

// addUserCommand puts a command in the Turn it interrupted, or its own
// block between Turns: anywhere else draws it out of order.
func (m *Model) addUserCommand(v event.UserCommandStarted) {
	b := m.cur
	if b == nil {
		b = m.commandBlock()
	}
	b.rev++
	b.rows = append(b.rows, &historyRow{
		id: v.UserCommand, command: v.Command, human: true, running: true,
	})
	m.trackNewest()
}

// commandBlock is where commands land between Turns. Reused while it is
// the newest, so a burst of them reads as one sitting.
func (m *Model) commandBlock() *turnBlock {
	if n := len(m.blocks); n > 0 && m.blocks[n-1].shell {
		return m.blocks[n-1]
	}
	b := &turnBlock{shell: true}
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
	r.live = append(r.live, v.Line)
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
	m.nav.cursor = min(m.nav.cursor, max(0, len(m.rows())-1))
	m.noteOK("undone")
}

// clearHistory forgets every block, as the engine forgot the transcript.
func (m *Model) clearHistory() {
	m.blocks, m.cur = nil, nil
	m.nav = navState{follow: true}
	m.calls, m.steps, m.errors, m.views, m.tokens = 0, 0, 0, 0, 0
	m.context = 0
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
	if id == uuid.Nil {
		return nil
	}
	for i := len(m.blocks) - 1; i >= 0; i-- {
		for _, r := range m.blocks[i].rows {
			if r.id == id {
				m.blocks[i].rev++
				return r
			}
		}
	}
	return nil
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
