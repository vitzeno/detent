package tui

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/vitzeno/detent/internal/classify"
)

// TestTUI_EndToEnd_RealProgram drives the actual tea.Program machinery —
// real Cmd execution, real spinner ticks, real rendering — rather than
// hand-driven Update() calls (model_test.go covers those, faster and
// more targeted). This is the one test that proves the wiring in
// commands.go and cmd/detent/main.go's tea.NewProgram call actually work
// together, not just that each piece compiles.
func TestTUI_EndToEnd_RealProgram(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{
		{
			"next_action":      choiceAnswer("test__no_args", 0.95),
			"goal_achieved":    noulAnswer(0.1),
			"goal_satisfiable": noulAnswer(0.9),
		},
		{
			"next_action":      choiceAnswer("done", 0.95),
			"goal_achieved":    noulAnswer(0.99),
			"goal_satisfiable": noulAnswer(0.9),
		},
	}}
	l := newTestLoop(t, judge, fakeConstructor(noCandidates))
	m := New(context.Background(), l)

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))

	tm.Type("run the safe test capability")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("test__no_args")) && bytes.Contains(out, []byte("done"))
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))

	// [enter] on the Finished screen loops back to input for another goal
	// — it must not quit the program.
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("enter to run"))
	}, teatest.WithDuration(2*time.Second), teatest.WithCheckInterval(10*time.Millisecond))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC}) // back on the input screen now — ctrl+c is the reliable quit here
	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))

	fm, ok := tm.FinalModel(t).(Model)
	if !ok {
		t.Fatalf("expected tui.Model, got %T", tm.FinalModel(t))
	}
	if fm.screen != screenInput {
		t.Errorf("final screen = %v, want screenInput (reset by [enter] on Finished)", fm.screen)
	}
	if fm.run != nil {
		t.Errorf("run = %v, want nil after resetForNewGoal", fm.run)
	}
}
