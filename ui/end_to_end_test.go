package ui

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Full-pipeline tests: goal -> propose -> confirm (or not) -> execute ->
// judge -> done/declined, driven through a real Bubble Tea program
// against a fakeDriver. This exercises ui's own state machine;
// resolver's translation has its own coverage in internal/resolver.

func TestUI_EndToEnd_GoalToDone(t *testing.T) {
	// newFakeDriver's default script never flags Dangerous, so this
	// proves execute/judge/done work without a confirm step.
	m := New(context.Background(), newFakeDriver(), SessionInfo{Proposer: "test-model", Judge: "jev-test"})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	tm.Type("what files are here?")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("saw two files"))
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	fm, ok := tm.FinalModel(t).(Model)
	if !ok {
		t.Fatalf("expected ui.Model, got %T", tm.FinalModel(t))
	}
	require.Len(t, fm.blocks, 1)
	require.True(t, fm.blocks[0].ended)
	// The command, then what the model said about it: prose gets a row
	// of its own so it can be selected and drawn.
	require.Len(t, fm.blocks[0].steps, 2)
	row := fm.blocks[0].steps[0]
	require.NotNil(t, row.cmd.ec, "post-execute judgment must land on the row")
	require.NotNil(t, row.cmd.ec.Post, "post-execute judgment must land on the row")
	// Same object as GoalResult.Commands sees — not a UI-only copy.
	require.Same(t, row.cmd.ec, fm.blocks[0].res.Commands[0])

	said := fm.blocks[0].steps[1]
	assert.NotEmpty(t, said.prose, "the model's closing words")
	assert.Nil(t, said.cmd.ec, "nothing ran to produce them")
	assert.NotNil(t, said.verdict(), "and they were judged like any output")
}

func TestUI_DeclineStopsGoal(t *testing.T) {
	// Flagged Dangerous so confirm actually shows — can't decline a
	// screen that never appeared.
	drv := newFakeDriver()
	drv.proposals = []Proposal{{Command: "rm -rf /tmp/x", Rationale: "remove"}}
	drv.pre.Dangerous = true
	m := New(context.Background(), drv, SessionInfo{Proposer: "test-model"})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	tm.Type("goal")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("[y/enter]"))
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))

	tm.Send(tea.KeyPressMsg{Code: 'n', Text: "n"})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("declined"))
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	fm := tm.FinalModel(t).(Model)
	require.Len(t, fm.blocks, 1)
	require.Empty(t, fm.blocks[0].steps, "declined command must never run")
}
