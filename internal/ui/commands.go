package ui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/fileio"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/shell"
	"github.com/vitzeno/detent/internal/usage"
)

// beginGoalCmd runs BeginGoal off the update loop — it now does real
// work (probe collection: a Jev call plus up to a few shell execs), so
// calling it inline would freeze the TUI until it returns.
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

// Live lines go to streamCh (drops counted, never block); Msg carries
// only the final result. ctx governs both the actual Execute call and
// the stream-forwarding select below — one context, so cancelling it
// (via /abort) really does reach exec.CommandContext inside shell.Stream
// instead of only silencing the UI's live-output listener.
func execCmd(ctx context.Context, sess Driver, res *agent.GoalResult, ustep *usage.Step, p propose.Proposal, pre agent.PreJudgment, streamCh chan<- streamMsg) tea.Cmd {
	return func() tea.Msg {
		ec, err := sess.Execute(ctx, res, ustep, p, pre, func(e shell.StreamEvent) {
			select {
			case streamCh <- streamMsg{stderr: e.Stderr, line: e.Line}:
			case <-ctx.Done():
			default:
				// Buffer full: count the drop, never block the command.
				select {
				case streamCh <- streamMsg{line: "…[live output dropped: UI lag]"}:
				default:
				}
			}
		})
		if err != nil {
			return execDoneMsg{err: err}
		}
		return execDoneMsg{ec: ec}
	}
}

// Slow judgment only delays the row upgrade; provisional row is already on screen.
func judgeCmd(ctx context.Context, sess Driver, goal, command string, result shell.Result, row *stepRow) tea.Cmd {
	return func() tea.Msg {
		post := sess.JudgeResult(ctx, goal, command, result)
		return judgeMsg{row: row, post: post}
	}
}

// saveCmd writes content to path — a direct, deterministic write, not
// a shell command — and only on success records it in the transcript,
// so a failed write never claims a change that didn't happen. No ctx:
// the write is local, synchronous IO with nothing to cancel.
func saveCmd(sess Driver, row *stepRow, path, content string) tea.Cmd {
	return func() tea.Msg {
		diff := row.editor.Diff()
		if err := fileio.Write(path, content); err != nil {
			return saveDoneMsg{row: row, err: err}
		}
		sess.RecordFileSave(path, diff)
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
