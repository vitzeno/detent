package ui

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/propose"
)

// Full-pipeline tests: goal -> propose -> confirm (or not) -> execute ->
// judge -> done/declined, driven through the real Bubble Tea program
// rather than by calling internal methods directly.

func TestUI_EndToEnd_GoalToDone(t *testing.T) {
	// ls -la is never flagged Dangerous (fullJudge reports MutReadOnly,
	// low scope_risk), so it runs straight through with no confirm step —
	// this proves the rest of the pipeline (execute, judge, done) still
	// works end to end without one.
	m := New(context.Background(), testSession(), "test-model", "jev-test")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	tm.Type("what files are here?")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("saw two files"))
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	fm, ok := tm.FinalModel(t).(Model)
	if !ok {
		t.Fatalf("expected ui.Model, got %T", tm.FinalModel(t))
	}
	require.Len(t, fm.blocks, 1)
	require.True(t, fm.blocks[0].ended)
	require.Len(t, fm.blocks[0].steps, 1)
	row := fm.blocks[0].steps[0]
	require.NotNil(t, row.cmd.ec, "post-execute judgment must land on the row")
	require.NotNil(t, row.cmd.ec.Post, "post-execute judgment must land on the row")
	// Same object as GoalResult.Commands sees — not a UI-only copy.
	require.Same(t, row.cmd.ec, fm.blocks[0].res.Commands[0])
}

func TestUI_DeclineStopsGoal(t *testing.T) {
	// A command flagged Dangerous by FlagDanger's regex backstop, so
	// confirm actually shows — declining a command that's never shown a
	// confirm screen isn't something a human can do.
	sess := &agent.Session{
		Proposer: &scriptProposer{script: []propose.Proposal{
			{Command: "rm -rf /tmp/x", Rationale: "remove"},
		}},
		Run: instantRun,
	}
	m := New(context.Background(), sess, "test-model", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	tm.Type("goal")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("[y/enter]"))
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("declined"))
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	fm := tm.FinalModel(t).(Model)
	require.Len(t, fm.blocks, 1)
	require.Empty(t, fm.blocks[0].steps, "declined command must never run")
}
