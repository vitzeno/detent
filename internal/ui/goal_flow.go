package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/internal/agent"
)

// startGoal opens a new goal block (or routes a "/"-prefixed line to
// runSlash) and dispatches BeginGoal off the update loop.
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
	// BeginGoal now does real work (probe collection: a Jev call plus up
	// to a few shell execs) so it must run off the update loop like any
	// other slow step, not inline here — an empty block shows the goal
	// immediately, filled in once beginGoalCmd resolves.
	b := &goalBlock{goal: goal}
	m.blocks = append(m.blocks, b)
	m.cur = b
	m.nav.focus = focusHistory
	m.input.Blur()
	m.waiting = true
	m.trackNewest()
	// Cancellable like an exec: esc and /abort drop the pending goal
	// instead of leaving the spinner stuck when a probe or the model hangs.
	ctx, cancel := context.WithCancel(m.ctx)
	m.abort = cancel
	return m, tea.Batch(m.spinner.Tick, beginGoalCmd(ctx, m.sess, goal))
}

// onBeginGoal lands once BeginGoal (goal transcript turn + probe
// collection) resolves, filling in the block startGoal created
// provisionally and kicking off the first propose call.
func (m Model) onBeginGoal(msg beginGoalMsg) (tea.Model, tea.Cmd) {
	m.waiting = false
	m.abort = nil
	if m.cur == nil {
		return m, nil
	}
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) {
			m.sess.RecordAbort(nil)
			m.cur.ended = true
			m.cur.end = agent.EndAborted
			m.cur = nil
			m = m.backToInput()
			return m, nil
		}
		m.cur.ended = true
		m.cur.fatalErr = msg.err
		m.cur = nil
		m = m.backToInput()
		return m, nil
	}
	m.cur.res = msg.res
	m.waiting = true
	ctx, cancel := context.WithCancel(m.ctx)
	m.abort = cancel
	return m, tea.Batch(m.spinner.Tick, proposeCmd(ctx, m.sess, msg.goal))
}

func (m Model) onPropose(msg proposeMsg) (tea.Model, tea.Cmd) {
	m.waiting = false
	m.abort = nil
	if m.cur == nil {
		return m, nil
	}
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) {
			// Aborted while thinking: propose itself touched no
			// transcript, but BeginGoal already opened this goal's
			// Stats.Goal and appended its user turn, so it still needs
			// closing — RecordAbort does both.
			m.sess.RecordAbort(m.cur.res)
			m.cur.ended = true
			m.cur.end = agent.EndAborted
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
	m.confirm.pending = msg.proposal
	m.confirm.pre = msg.pre
	m.confirm.use = msg.used
	m.confirm.shownAt = time.Now()
	// Confirm only interrupts for a command Dangerous flags — Jev's
	// mutability/scope_risk escalation or the FlagDanger regex backstop.
	// Everything else runs straight through approve(), the same path
	// pressing "y" would take, just without the modal in between.
	if !msg.pre.Dangerous {
		return m.approve()
	}
	m.mode = modeConfirm
	m.sizeViewport()
	return m, nil
}

func (m Model) approve() (tea.Model, tea.Cmd) {
	if m.cur == nil {
		m.mode = modeInput
		return m, nil
	}
	p, pre := m.confirm.pending, m.confirm.pre
	ustep := m.cur.res.Stats.AddStep(p.Command)
	ustep.SetPropose(m.confirm.use)
	ustep.SetJudgePre(pre.JudgeUsage)
	ustep.SetDwell(time.Since(m.confirm.shownAt))
	row := &stepRow{command: p.Command, editPath: p.File, cmd: cmdState{rationale: p.Rationale, pre: pre, running: true, usage: ustep}}
	m.cur.steps = append(m.cur.steps, row)
	m.totalCmds++
	m.mode = modeInput
	m.waiting = true
	m.trackNewest()
	m.sizeViewport()
	m.refreshViewport()

	ctx, cancel := context.WithCancel(m.ctx)
	m.abort = cancel
	return m, tea.Batch(m.spinner.Tick, streamWaitCmd(m.streamCh, ctx),
		execCmd(ctx, m.sess, m.cur.res, ustep, p, pre, m.streamCh))
}

func (m Model) decline() (tea.Model, tea.Cmd) {
	if m.cur == nil {
		m.mode = modeInput
		return m, nil
	}
	dstep := m.cur.res.Stats.AddStep(m.confirm.pending.Command)
	dstep.SetPropose(m.confirm.use)
	dstep.SetJudgePre(m.confirm.pre.JudgeUsage)
	dstep.SetDwell(time.Since(m.confirm.shownAt))
	m.sess.RecordDecline(m.cur.res, m.confirm.pending.Command)
	m.cur.ended = true
	m.cur.end = m.cur.res.End
	m.cur = nil
	m = m.backToInput()
	m.sizeViewport()
	return m, nil
}

func (m Model) backToInput() Model {
	m.mode = modeInput
	m.nav.focus = focusInput
	m.input.Focus()
	m.refreshViewport()
	return m
}

// completionDisagreement reports proposer/judge divergence on the goal banner.
func completionDisagreement(b *goalBlock) string {
	for i := len(b.steps) - 1; i >= 0; i-- {
		r := b.steps[i]
		if r.cmd.ec == nil || r.cmd.ec.Post == nil {
			continue
		}
		p := r.cmd.ec.Post
		if p.FromJudge && p.GoalAchieved >= 0 {
			if p.GoalAchieved < 0.5 {
				return fmt.Sprintf("jev second opinion: goal looks unmet (%.2f)", p.GoalAchieved)
			}
			return ""
		}
	}
	return ""
}
