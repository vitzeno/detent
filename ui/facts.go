package ui

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/ui/status"
)

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
	// Every fact may change content, so the caches go stale here
	// rather than at a dozen mutation sites.
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
		m.cur = b
		m.waiting = true
		m.trackNewest()

	case event.CheckpointTaken:
		if b := m.block(v.Turn); b != nil {
			b.undoable = true
		}

	case event.TurnEnded:
		m.endTurn(v)

	case event.BoundReached:
		m.bound = &v
		m.waiting = false
		m.raise(modeBound)

	case event.ModelText:
		m.addProse(v.Text)

	case event.CallProposed:
		m.addCall(v)

	case event.CallAssessed:
		if r := m.row(v.Call); r != nil {
			r.risk = v.Risk
		}

	case event.ApprovalAsked:
		m.asking = &v
		m.confirm = confirmState{}
		m.waiting = false
		m.raise(modeConfirm)

	case event.CallStarted:
		if r := m.row(v.Call); r != nil {
			r.running = true
		}

	case event.OutputChunk:
		m.addLine(v)

	case event.CallEnded:
		if r := m.row(v.Call); r != nil {
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

	case event.ShellStarted:
		m.addShell(v)

	case event.ShellEnded:
		if r := m.row(v.Shell); r != nil {
			r.running = false
			result := v.Result
			r.result = &result
		}

	case event.CallJudged:
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
	m.cur, m.asking, m.bound = nil, nil, nil
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
	m.cur.rows = append(m.cur.rows, &callRow{prose: text})
	m.trackNewest()
}

func (m *Model) addCall(v event.CallProposed) {
	if m.cur == nil {
		return
	}
	m.cur.rows = append(m.cur.rows, &callRow{
		id: v.Call, command: event.Command(v.Tool, v.Args),
		renders: v.Renders, executor: v.Executor,
	})
	m.trackNewest()
}

// addShell puts a command in the Turn it interrupted, or its own
// block between Turns: anywhere else draws it out of order.
func (m *Model) addShell(v event.ShellStarted) {
	b := m.cur
	if b == nil {
		b = m.shellBlock()
	}
	b.rows = append(b.rows, &callRow{
		id: v.Shell, command: v.Command, human: true, running: true,
	})
	m.trackNewest()
}

// shellBlock is where commands land between Turns. Reused while it is
// the newest, so a burst of them reads as one sitting.
func (m *Model) shellBlock() *turnBlock {
	if n := len(m.blocks); n > 0 && m.blocks[n-1].shell {
		return m.blocks[n-1]
	}
	b := &turnBlock{shell: true}
	m.blocks = append(m.blocks, b)
	return b
}

// maxLiveLines bounds what one running call keeps on screen. The rest
// is counted away and the full output arrives with CallEnded.
const maxLiveLines = 200

func (m *Model) addLine(v event.OutputChunk) {
	r := m.row(v.Call)
	if r == nil {
		return
	}
	if len(r.live) >= maxLiveLines {
		r.dropped++
		r.live = r.live[1:]
	}
	r.live = append(r.live, v.Line)
}

func (m *Model) judged(v event.CallJudged) {
	r := m.row(v.Call)
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
	r := m.row(v.Call)
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

// block and row find what an event is about. Linear: a map would have
// to be kept in step with the slice that draws them.
func (m *Model) block(id uuid.UUID) *turnBlock {
	for _, b := range m.blocks {
		if b.id == id {
			return b
		}
	}
	return nil
}

func (m *Model) row(id uuid.UUID) *callRow {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		for _, r := range m.blocks[i].rows {
			if r.id == id {
				return r
			}
		}
	}
	return nil
}
