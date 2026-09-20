package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// startGoal opens a new goal block (or routes a "/"-prefixed line to
// runSlash) and dispatches BeginGoal off the update loop.
func (m Model) startGoal() (tea.Model, tea.Cmd) {
	goal := strings.TrimSpace(m.prompt.Value())
	if goal == "" {
		return m, nil
	}
	m.prompt.Clear()
	m.notice = ""
	if strings.HasPrefix(goal, "/") {
		return m.runSlash(goal)
	}
	// BeginGoal does real work (probe collection), so it runs off the
	// update loop; an empty block shows the goal immediately.
	b := &goalBlock{goal: goal}
	m.blocks = append(m.blocks, b)
	m.cur = b
	m.nav.focus = focusHistory
	m.prompt.Blur()
	m.waiting = true
	m.trackNewest()
	ctx, cancel := context.WithCancel(m.ctx)
	m.abort = cancel
	return m, tea.Batch(m.spinner.Tick, beginGoalCmd(ctx, m.sess, goal))
}

// onBeginGoal fills in the block startGoal created provisionally and
// kicks off the first propose call.
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
			m.cur.end = EndAborted
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
			// BeginGoal already opened this goal's turn; RecordAbort
			// closes it.
			m.sess.RecordAbort(m.cur.res)
			m.cur.ended = true
			m.cur.end = EndAborted
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
	// Non-dangerous commands take the same approve() path "y" would,
	// just without the modal.
	if !msg.pre.Dangerous {
		return m.approve()
	}
	m.mode = modeConfirm
	return m, nil
}

func (m Model) approve() (tea.Model, tea.Cmd) {
	if m.cur == nil {
		m.mode = modeInput
		return m, nil
	}
	p, pre := m.confirm.pending, m.confirm.pre
	step := m.sess.RecordStep(m.cur.res, p, pre, m.confirm.use, time.Since(m.confirm.shownAt))
	row := &stepRow{command: p.Command, editPath: p.File, cmd: cmdState{rationale: p.Rationale, pre: pre, running: true, step: step}}
	m.cur.steps = append(m.cur.steps, row)
	m.totalCmds++
	m.mode = modeInput
	m.waiting = true
	m.trackNewest()

	ctx, cancel := context.WithCancel(m.ctx)
	m.abort = cancel
	return m, tea.Batch(m.spinner.Tick, streamWaitCmd(m.streamCh, ctx),
		execCmd(ctx, m.sess, m.cur.res, step, p, pre, m.streamCh))
}

func (m Model) decline() (tea.Model, tea.Cmd) {
	if m.cur == nil {
		m.mode = modeInput
		return m, nil
	}
	m.sess.RecordStep(m.cur.res, m.confirm.pending, m.confirm.pre, m.confirm.use, time.Since(m.confirm.shownAt))
	m.sess.RecordDecline(m.cur.res, m.confirm.pending.Command)
	m.cur.ended = true
	m.cur.end = m.cur.res.End
	m.cur = nil
	m = m.backToInput()
	return m, nil
}

func (m Model) backToInput() Model {
	m.mode = modeInput
	m.nav.focus = focusInput
	m.prompt.Focus()
	return m
}

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
