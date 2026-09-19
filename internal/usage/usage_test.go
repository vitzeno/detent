package usage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNilSafe(t *testing.T) {
	var tr *Tracker
	require.NotPanics(t, func() {
		g := tr.StartGoal("g")
		require.Nil(t, g)
		s := g.AddStep("ls")
		require.Nil(t, s)
		s.SetPropose(Usage{PromptTokens: 1})
		s.SetJudgePre(Usage{})
		s.SetDwell(time.Second)
		s.SetExec(time.Second, 0, 10)
		s.SetJudgePost(Usage{}, 0.1, 0.9)
		g.Finish("done", "s", false)
		assert.Equal(t, Snapshot{}, tr.Snapshot())
		assert.Empty(t, tr.Goals())
		assert.Zero(t, g.Duration())
		assert.Zero(t, g.MachineTime())
	})
}

func TestRollups(t *testing.T) {
	tr := &Tracker{}
	g := tr.StartGoal("find it")
	s := g.AddStep("ls")
	s.SetPropose(Usage{PromptTokens: 100, CompletionTokens: 20, Latency: 200 * time.Millisecond, Model: "m"})
	s.SetJudgePre(Usage{PromptTokens: 50, Latency: 100 * time.Millisecond, Model: "j"})
	s.SetDwell(1500 * time.Millisecond)
	s.SetExec(90*time.Millisecond, 0, 12)
	s.SetJudgePost(Usage{PromptTokens: 60, CompletionTokens: 0, Latency: 120 * time.Millisecond}, 0.2, 0.9)
	d := g.AddStep("rm x")
	d.SetDwell(300 * time.Millisecond)
	g.Finish("declined", "", true)

	snap := tr.Snapshot()
	assert.Equal(t, 1, snap.Goals)
	assert.Equal(t, 1, snap.Commands)
	assert.Equal(t, 1, snap.Declined)
	assert.Equal(t, 200*time.Millisecond, snap.Propose)
	assert.Equal(t, 220*time.Millisecond, snap.Judge)
	assert.Equal(t, 1800*time.Millisecond, snap.Dwell)
	assert.Equal(t, 90*time.Millisecond, snap.Exec)
	assert.Equal(t, 120, snap.ProposerTokens)
	assert.Equal(t, 110, snap.JudgeTokens)
	assert.Equal(t, 510*time.Millisecond, snap.MachineTime())

	require.Len(t, tr.Goals(), 1)
	assert.Len(t, tr.Goals()[0].Steps, 2)
	assert.True(t, tr.Goals()[0].Steps[0].HasPost)
	assert.Equal(t, 0.9, tr.Goals()[0].Steps[0].GoalAchieved)
	assert.Equal(t, "declined", tr.Goals()[0].End)
	assert.Less(t, g.Duration(), time.Minute, "wall clock, not span sums")
	assert.Equal(t, 510*time.Millisecond, g.MachineTime())
}
