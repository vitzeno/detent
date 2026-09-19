package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/internal/agentloop"
	"github.com/vitzeno/detent/internal/ui/editor"
	"github.com/vitzeno/detent/internal/ui/slash"
	"github.com/vitzeno/detent/internal/ui/tree"
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
	// BeginGoal now does real work (probe collection: a Jev call plus up
	// to a few shell execs) so it must run off the update loop like any
	// other slow step, not inline here — an empty block shows the goal
	// immediately, filled in once beginGoalCmd resolves.
	b := &goalBlock{goal: goal}
	m.blocks = append(m.blocks, b)
	m.cur = b
	m.focus = focusHistory
	m.input.Blur()
	m.waiting = true
	m.trackNewest()
	// Cancellable like an exec: esc and /abort drop the pending goal
	// instead of leaving the spinner stuck when a probe or the model hangs.
	ctx, cancel := context.WithCancel(m.ctx)
	m.abort = cancel
	return m, tea.Batch(m.spinner.Tick, beginGoalCmd(ctx, m.sess, goal))
}

// runSlash handles input-bar commands: the always-available buttons for
// quitting and aborting that don't compete with typing, plus the tool
// invocations (/tree, /usage, /help) that show their content in the
// output pane like any other focusable entry — see openTool.
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
	case "/tree":
		return m.openTreeTool()
	case "/usage":
		return m.openTool("usage", &stepRow{command: "/usage", toolKind: "usage", usageExpand: -1})
	case "/help":
		return m.openTool("help", &stepRow{command: "/help", toolKind: "help"})
	default:
		m.notice = "unknown command " + cmd + " (try /help)"
		return m, nil
	}
}

// openTreeTool walks cwd (a harness-run, read-only filesystem action —
// same bounded-and-safe spirit as internal/probe, never confirmed since
// nothing runs and nothing's proposed) and opens it as a tool block.
func (m Model) openTreeTool() (tea.Model, tea.Cmd) {
	cwd, err := os.Getwd()
	if err != nil {
		m.notice = "tree: " + err.Error()
		return m, nil
	}
	root, truncated, err := tree.Build(cwd)
	if err != nil {
		m.notice = "tree: " + err.Error()
		return m, nil
	}
	tm := tree.New(root)
	command := cwd
	if truncated {
		command += " (truncated)"
	}
	return m.openTool("tree", &stepRow{command: command, toolKind: "tree", tree: &tm})
}

// openTreeSelection acts on the cursor row of a tree tool row: a
// directory toggles expand/collapse, a file opens as its own new
// history entry showing its editor — the same reusable component a
// command's own File field opens, so editing works identically whether
// you got to the file via a proposed command or by browsing for it.
func (m Model) openTreeSelection(r *stepRow) (tea.Model, tea.Cmd) {
	if r.tree == nil {
		return m, nil
	}
	n := r.tree.Selected()
	if n == nil {
		return m, nil
	}
	if n.Kind == tree.KindDir {
		r.tree.Toggle()
		m.refreshViewport()
		return m, nil
	}
	ed := editor.New(n.Path)
	fileRow := &stepRow{command: n.Path, editPath: n.Path, toolKind: "file", editor: &ed}
	return m.openTool("file", fileRow)
}

// openTool appends a new tool block — a slash command's own content,
// never a goal driven by propose/confirm/execute — and jumps straight
// to viewing it: unlike trackNewest's "don't yank a reader's view"
// default for background events, this is an explicit action the human
// just took and expects to see the result of immediately.
func (m Model) openTool(kind string, row *stepRow) (tea.Model, tea.Cmd) {
	m.blocks = append(m.blocks, &goalBlock{tool: kind, ended: true, steps: []*stepRow{row}})
	m.cursor = len(m.rows()) - 1
	m.focus = focusOutput
	m.sizeViewport()
	m.refreshViewport()
	return m, nil
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
			m.cur.ended = true
			m.cur.end = agentloop.EndAborted
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
	m.pendingUse = msg.used
	m.confirmShownAt = time.Now()
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
	p, pre := m.pending, m.pendingPre
	ustep := m.cur.res.Stats.AddStep(p.Command)
	ustep.SetPropose(m.pendingUse)
	ustep.SetJudgePre(pre.JudgeUsage)
	ustep.SetDwell(time.Since(m.confirmShownAt))
	row := &stepRow{command: p.Command, rationale: p.Rationale, pre: pre, running: true, usage: ustep, editPath: p.File}
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
		execCmd(m.ctx, m.sess, m.cur.res, ustep, p, pre, m.streamCh, ctx))
}

func (m Model) decline() (tea.Model, tea.Cmd) {
	if m.cur == nil {
		m.mode = modeInput
		return m, nil
	}
	dstep := m.cur.res.Stats.AddStep(m.pending.Command)
	dstep.SetPropose(m.pendingUse)
	dstep.SetJudgePre(m.pendingPre.JudgeUsage)
	dstep.SetDwell(time.Since(m.confirmShownAt))
	m.sess.RecordDecline(m.cur.res, m.pending.Command)
	m.cur.ended = true
	m.cur.end = m.cur.res.End
	m.cur = nil
	m = m.backToInput()
	m.sizeViewport()
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
	row.ec = msg.ec
	if row.editPath != "" {
		// Read from disk, not msg.ec.Result.Stdout: a file-writing
		// command (a heredoc, a redirect) typically prints nothing —
		// the content lives on disk, never in captured output.
		ed := editor.New(row.editPath)
		row.editor = &ed
	}
	cmds := []tea.Cmd{
		judgeCmd(m.ctx, m.sess, m.cur.goal, row.command, msg.ec.Result, row),
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

// startSave opens the diff confirm for the focused row's editor. A
// no-op (with a notice, not a confirm nobody needs) when there's
// nothing to save.
func (m Model) startSave() (tea.Model, tea.Cmd) {
	r := m.focused()
	if r == nil || r.editor == nil || !r.editor.Dirty() {
		m.notice = "nothing to save"
		return m, nil
	}
	m.saveRow = r
	m.mode = modeSaveConfirm
	m.sizeViewport()
	return m, nil
}

func (m Model) confirmSave() (tea.Model, tea.Cmd) {
	m.mode = modeInput
	r := m.saveRow
	m.saveRow = nil
	m.sizeViewport()
	if r == nil || r.editor == nil {
		return m, nil
	}
	return m, saveCmd(m.ctx, m.sess, r, r.editor.Path, r.editor.Value())
}

func (m Model) cancelSave() (tea.Model, tea.Cmd) {
	m.mode = modeInput
	m.saveRow = nil
	m.sizeViewport()
	return m, nil
}

// onSaveDone lands once the write (and its transcript note) finish.
// The buffer itself is untouched either way — a failed save leaves the
// edit exactly as it was, free to retry.
func (m Model) onSaveDone(msg saveDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.notice = fmt.Sprintf("save failed: %v", msg.err)
		return m, nil
	}
	if msg.row != nil && msg.row.editor != nil {
		msg.row.editor.MarkSaved(msg.content)
	}
	m.notice = "saved"
	m.refreshViewport()
	return m, nil
}

// Reports proposer/judge divergence on the goal banner.
func completionDisagreement(b *goalBlock) string {
	for i := len(b.steps) - 1; i >= 0; i-- {
		r := b.steps[i]
		if r.ec == nil || r.ec.Post == nil {
			continue
		}
		p := r.ec.Post
		if p.FromJudge && p.GoalAchieved >= 0 {
			if p.GoalAchieved < 0.5 {
				return fmt.Sprintf("jev second opinion: goal looks unmet (%.2f)", p.GoalAchieved)
			}
			return ""
		}
	}
	return ""
}
