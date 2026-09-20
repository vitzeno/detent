package ui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// beginGoalCmd runs BeginGoal off the update loop — probe collection
// does real work now, and running it inline would freeze the TUI.
func beginGoalCmd(ctx context.Context, sess Driver, goal string) tea.Cmd {
	return func() tea.Msg {
		res, err := sess.BeginGoal(ctx, goal)
		return beginGoalMsg{goal: goal, res: res, err: err}
	}
}

func proposeCmd(ctx context.Context, sess Driver, goal string) tea.Cmd {
	return func() tea.Msg {
		proposal, pre, used, err := sess.ProposeNext(ctx, goal)
		return proposeMsg{proposal: proposal, pre: pre, used: used, err: err}
	}
}

// chanSink adapts streamCh to StreamSink at the point where a running
// command's output crosses into Bubble Tea's message loop.
type chanSink struct {
	ch  chan<- streamMsg
	ctx context.Context
}

func (s chanSink) OnEvent(line string, stderr bool) {
	select {
	case s.ch <- streamMsg{stderr: stderr, line: line}:
	case <-s.ctx.Done():
	default:
		// Buffer full: count the drop, never block the command.
		select {
		case s.ch <- streamMsg{line: "…[live output dropped: UI lag]"}:
		default:
		}
	}
}

// Live lines go to streamCh (drops counted, never block); Msg carries
// only the final result. One ctx for both Execute and the forwarding
// select below, so /abort actually reaches the command, not just the
// UI's listener.
func execCmd(ctx context.Context, sess Driver, res *GoalResult, step StepHandle, p Proposal, pre PreJudgment, streamCh chan<- streamMsg) tea.Cmd {
	return func() tea.Msg {
		ec, err := sess.Execute(ctx, res, step, p, pre, chanSink{ch: streamCh, ctx: ctx})
		if err != nil {
			return execDoneMsg{err: err}
		}
		return execDoneMsg{ec: ec}
	}
}

// Slow judgment only delays the row upgrade; provisional row is already on screen.
func judgeCmd(ctx context.Context, sess Driver, goal, command string, result Result, row *stepRow) tea.Cmd {
	return func() tea.Msg {
		post := sess.JudgeResult(ctx, goal, command, result, row.cmd.step)
		return judgeMsg{row: row, post: post}
	}
}

// saveCmd writes content to path directly (not a shell command) and
// records it in one Driver call, so a failed write never claims a
// change that didn't happen. No ctx: local synchronous IO, nothing to cancel.
func saveCmd(sess Driver, row *stepRow, path, content string) tea.Cmd {
	return func() tea.Msg {
		diff := row.editor.Diff()
		if err := sess.SaveFile(path, diff, content); err != nil {
			return saveDoneMsg{row: row, err: err}
		}
		return saveDoneMsg{row: row, content: content}
	}
}

// Receives one live line; re-dispatched per line while running.
func streamWaitCmd(ch <-chan streamMsg, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		select {
		case m := <-ch:
			return m
		case <-ctx.Done():
			return nil
		}
	}
}
