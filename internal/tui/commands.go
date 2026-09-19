package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/internal/loop"
)

// prepareCmd runs Run.Prepare — one batched Judge call plus argument
// resolution and gating — off the UI goroutine (§8: "The API call happens
// in a tea.Cmd returning a tea.Msg, never synchronously inside Update").
func prepareCmd(ctx context.Context, run *loop.Run) tea.Cmd {
	return func() tea.Msg {
		prepared, term, err := run.Prepare(ctx)
		return preparedMsg{prepared: prepared, termination: term, err: err}
	}
}

// commitCmd runs Run.Commit — dispatch, reduce, append — off the UI
// goroutine. Only called after an unconditional step or an approved one.
func commitCmd(ctx context.Context, run *loop.Run, prepared *loop.Prepared) tea.Cmd {
	return func() tea.Msg {
		finding, err := run.Commit(ctx, prepared)
		return committedMsg{finding: finding, err: err}
	}
}

// resolveCmd runs Run.ResolveAmbiguous — gating a human's pick — off the
// UI goroutine. Cheap (no network call: §8.2, "no extra Jev call"), but
// still real work (a filesystem/process gate check) that shouldn't block
// a redraw.
func resolveCmd(run *loop.Run, prepared *loop.Prepared, candidateID string) tea.Cmd {
	return func() tea.Msg {
		resolved, err := run.ResolveAmbiguous(prepared, candidateID)
		return resolvedMsg{prepared: resolved, err: err}
	}
}
