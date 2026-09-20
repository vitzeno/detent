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

// Live lines go to streamCh; Msg carries only the final result. One ctx
// for both Execute and the read side, so /abort reaches the command.
func execCmd(ctx context.Context, sess Driver, res *GoalResult, step StepHandle, p Proposal, pre PreJudgment, streamCh chan<- StreamEvent) tea.Cmd {
	return func() tea.Msg {
		ec, err := sess.Execute(ctx, res, step, p, pre, streamCh)
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

// rollbackCmd runs off the update loop: it's a real containerd RPC
// (Prepare against the checkpoint), not free.
func rollbackCmd(ctx context.Context, sess Driver, target *goalBlock, step int) tea.Cmd {
	return func() tea.Msg {
		ok, err := sess.Rollback(ctx, target.res, step)
		return rollbackDoneMsg{target: target, step: step, ok: ok, err: err}
	}
}

// Receives one live line; re-dispatched per line while running.
func streamWaitCmd(ch <-chan StreamEvent, ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		select {
		case m := <-ch:
			return m
		case <-ctx.Done():
			return nil
		}
	}
}
