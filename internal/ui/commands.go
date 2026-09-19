package ui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/internal/agentloop"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/shell"
)

func proposeCmd(ctx context.Context, sess Driver, goal string) tea.Cmd {
	return func() tea.Msg {
		proposal, pre, err := sess.ProposeNext(ctx, goal)
		return proposeMsg{proposal: proposal, pre: pre, err: err}
	}
}

// Live lines go to streamCh (drops counted, never block); Msg carries only the final result.
func execCmd(ctx context.Context, sess Driver, res *agentloop.GoalResult, p propose.Proposal, pre agentloop.PreJudgment, streamCh chan<- streamMsg, runCtx context.Context) tea.Cmd {
	return func() tea.Msg {
		_, err := sess.Execute(ctx, res, p, pre, func(e shell.StreamEvent) {
			select {
			case streamCh <- streamMsg{stderr: e.Stderr, line: e.Line}:
			case <-runCtx.Done():
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
		last := res.Commands[len(res.Commands)-1]
		return execDoneMsg{result: last.Result}
	}
}

// Slow judgment only delays the row upgrade; provisional row is already on screen.
func judgeCmd(ctx context.Context, sess Driver, goal, command string, result shell.Result, block *goalBlock, row *stepRow) tea.Cmd {
	return func() tea.Msg {
		post := sess.JudgeResult(ctx, goal, command, result)
		return judgeMsg{block: block, row: row, post: post}
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
