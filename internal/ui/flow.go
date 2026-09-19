package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/internal/agentloop"
	"github.com/vitzeno/detent/internal/ui/slash"
)

// updateSlash refreshes prefix matches after the input changes.
func (m *Model) updateSlash() {
	m.slash = slash.Match(m.input.Value())
	if m.slashCursor >= len(m.slash) {
		m.slashCursor = 0
	}
	m.sizeViewport()
}

// acceptSlash completes the highlighted entry into the input bar.
func (m Model) acceptSlash() (tea.Model, tea.Cmd) {
	if len(m.slash) == 0 {
		return m, nil
	}
	m.input.SetValue(m.slash[m.slashCursor].Name + " ")
	m.slash = nil
	m.slashCursor = 0
	m.sizeViewport()
	return m, nil
}

func (m Model) startGoal() (tea.Model, tea.Cmd) {
	goal := strings.TrimSpace(m.input.Value())
	if goal == "" {
		return m, nil
	}
	m.input.SetValue("")
	m.notice = ""
	if strings.HasPrefix(goal, "/") {
		return m.runSlash(goal)
	}
	res, err := m.sess.BeginGoal(goal)
	if err != nil {
		b := &goalBlock{goal: goal, ended: true, fatalErr: err}
		m.blocks = append(m.blocks, b)
		m.cur = nil
		m.trackNewest()
		return m, nil
	}
	b := &goalBlock{goal: goal, res: res}
	m.blocks = append(m.blocks, b)
	m.cur = b
	m.focus = focusHistory
	m.input.Blur()
	m.waiting = true
	m.trackNewest()
	// Cancellable like an exec: esc and /abort drop the pending propose
	// instead of leaving the spinner stuck when the model hangs.
	ctx, cancel := context.WithCancel(m.ctx)
	m.abort = cancel
	return m, tea.Batch(m.spinner.Tick, proposeCmd(ctx, m.sess, goal))
}

// runSlash handles input-bar commands: the always-available buttons for
// quitting and aborting that don't compete with typing.
func (m Model) runSlash(cmd string) (tea.Model, tea.Cmd) {
	switch strings.ToLower(strings.Fields(cmd)[0]) {
	case "/q", "/quit":
		return m, tea.Quit
	case "/abort":
		if m.abort != nil {
			m.abort()
			m.abort = nil
			m.notice = "abort sent"
		} else {
			m.notice = "nothing running"
		}
		return m, nil
	case "/help":
		m.notice = "/quit · /abort · tab switches input/history"
		return m, nil
	default:
		m.notice = "unknown command " + cmd + " (try /help)"
		return m, nil
	}
}

func (m Model) onPropose(msg proposeMsg) (tea.Model, tea.Cmd) {
	m.waiting = false
	m.abort = nil
	if m.cur == nil {
		return m, nil
	}
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) {
			// Aborted while thinking: propose touches no transcript,
			// so just close the still-empty block.
			m.cur.ended = true
			m.cur.end = agentloop.EndAborted
			m.cur = nil
			m = m.backToInput()
			return m, nil
		}
		if m.ctx.Err() != nil {
			return m, nil
		}
		m.cur.ended = true
		m.cur.fatalErr = msg.err
		m.sess.RecordProposerError(m.cur.res, msg.err)
		m.cur.end = m.cur.res.End
		m.cur = nil
		m = m.backToInput()
		return m, nil
	}
	if msg.proposal.Done {
		m.sess.RecordDone(m.cur.res, msg.proposal)
		m.cur.ended = true
		m.cur.end = m.cur.res.End
		m.cur.summary = msg.proposal.Summary
		m.cur.judgeNote = completionDisagreement(m.cur)
		m.cur = nil
		m = m.backToInput()
		return m, nil
	}
	m.pending = msg.proposal
	m.pendingPre = msg.pre
	m.mode = modeConfirm
	return m, nil
}

func (m Model) approve() (tea.Model, tea.Cmd) {
	if m.cur == nil {
		m.mode = modeInput
		return m, nil
	}
	p, pre := m.pending, m.pendingPre
	row := &stepRow{command: p.Command, rationale: p.Rationale, pre: pre, running: true}
	m.cur.steps = append(m.cur.steps, row)
	m.totalCmds++
	m.mode = modeInput
	m.waiting = true
	m.trackNewest()
	m.refreshViewport()

	ctx, cancel := context.WithCancel(m.ctx)
	m.abort = cancel
	return m, tea.Batch(m.spinner.Tick, streamWaitCmd(m.streamCh, ctx),
		execCmd(m.ctx, m.sess, m.cur.res, p, pre, m.streamCh, ctx))
}

func (m Model) decline() (tea.Model, tea.Cmd) {
	if m.cur == nil {
		m.mode = modeInput
		return m, nil
	}
	m.sess.RecordDecline(m.cur.res, m.pending.Command)
	m.cur.ended = true
	m.cur.end = m.cur.res.End
	m.cur = nil
	m = m.backToInput()
	return m, nil
}

func (m Model) onStream(msg streamMsg) (tea.Model, tea.Cmd) {
	for _, b := range m.blocks {
		for _, r := range b.steps {
			if r.running {
				if len(r.live) < maxLiveLines {
					prefix := ""
					if msg.stderr {
						prefix = "(stderr) "
					}
					r.live = append(r.live, prefix+msg.line)
				} else {
					r.dropped++
				}
				m.refreshViewport()
				// Keep waiting on the channel while a command runs.
				if m.abort != nil {
					return m, streamWaitCmd(m.streamCh, m.ctx)
				}
				return m, nil
			}
		}
	}
	return m, nil
}

func (m Model) onExecDone(msg execDoneMsg) (tea.Model, tea.Cmd) {
	m.waiting = false
	m.abort = nil
	if m.cur == nil {
		return m, nil
	}
	row := m.cur.steps[len(m.cur.steps)-1]
	row.running = false
	if msg.err != nil {
		m.cur.ended = true
		m.cur.end = m.cur.res.End
		m.cur.fatalErr = msg.err
		m.cur = nil
		m = m.backToInput()
		m.refreshViewport()
		return m, nil
	}
	row.result = &msg.result
	cmds := []tea.Cmd{
		judgeCmd(m.ctx, m.sess, m.cur.goal, row.command, msg.result, m.cur, row),
		proposeCmd(m.ctx, m.sess, m.cur.goal),
	}
	m.waiting = true
	m.refreshViewport()
	return m, tea.Batch(append(cmds, m.spinner.Tick)...)
}

func (m Model) backToInput() Model {
	m.mode = modeInput
	m.focus = focusInput
	m.input.Focus()
	m.refreshViewport()
	return m
}

// Reports proposer/judge divergence on the goal banner.
func completionDisagreement(b *goalBlock) string {
	for i := len(b.steps) - 1; i >= 0; i-- {
		p := b.steps[i].post
		if p != nil && p.FromJudge && p.GoalAchieved >= 0 {
			if p.GoalAchieved < 0.5 {
				return fmt.Sprintf("jev second opinion: goal looks unmet (%.2f)", p.GoalAchieved)
			}
			return ""
		}
	}
	return ""
}
