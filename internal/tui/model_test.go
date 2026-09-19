package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capabilities"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/extract"
	"github.com/vitzeno/detent/internal/gate"
	"github.com/vitzeno/detent/internal/loop"
)

// asModel is a small helper: Update returns tea.Model, but every test
// here wants the concrete Model back to inspect its fields.
func asModel(t *testing.T, tm tea.Model) Model {
	t.Helper()
	m, ok := tm.(Model)
	require.True(t, ok, "expected tui.Model, got %T", tm)
	return m
}

func TestModel_SubmitGoal_TransitionsToRunning(t *testing.T) {
	l := newTestLoop(t, &scriptedJudge{t: t}, fakeConstructor(noCandidates))
	m := New(context.Background(), l)
	m.input.SetValue("do the thing")

	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, tm)

	assert.Equal(t, screenRunning, m.screen)
	assert.True(t, m.waiting)
	assert.NotNil(t, m.run)
	assert.Equal(t, "do the thing", m.run.State().Goal)
	assert.NotNil(t, cmd, "must dispatch a Cmd — the API call never runs synchronously inside Update")
}

func TestModel_EmptyGoal_DoesNotSubmit(t *testing.T) {
	l := newTestLoop(t, &scriptedJudge{t: t}, fakeConstructor(noCandidates))
	m := New(context.Background(), l)

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, tm)

	assert.Equal(t, screenInput, m.screen)
	assert.Nil(t, m.run)
}

func TestModel_SafeCapability_RunsThenTerminates(t *testing.T) {
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
	m.run = l.NewRun("do it")
	m.screen = screenRunning
	m.waiting = true

	prepared, term, err := m.run.Prepare(context.Background())
	require.NoError(t, err)
	require.Nil(t, term)
	tm, _ := m.Update(preparedMsg{prepared: prepared})
	m = asModel(t, tm)
	assert.Equal(t, screenRunning, m.screen)
	assert.True(t, m.waiting)

	finding, err := m.run.Commit(context.Background(), prepared)
	require.NoError(t, err)
	tm, _ = m.Update(committedMsg{finding: finding})
	m = asModel(t, tm)
	assert.Equal(t, screenRunning, m.screen)
	assert.Len(t, m.run.State().Findings, 1)

	prepared2, term2, err := m.run.Prepare(context.Background())
	require.NoError(t, err)
	require.Nil(t, prepared2)
	require.NotNil(t, term2)
	tm, _ = m.Update(preparedMsg{termination: term2})
	m = asModel(t, tm)
	assert.Equal(t, screenFinished, m.screen)
	assert.Equal(t, loop.ReasonGoalAchieved, m.termination.Reason)

	view := m.View()
	assert.Contains(t, view, "done")
}

func TestModel_Mutation_ApproveFlow(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":      choiceAnswer("test__mutate", 0.95),
		"goal_achieved":    noulAnswer(0.1),
		"goal_satisfiable": noulAnswer(0.9),
	}}}
	l := newTestLoop(t, judge, fakeConstructor(noCandidates))
	m := New(context.Background(), l)
	m.run = l.NewRun("mutate it")
	m.screen = screenRunning

	prepared, _, err := m.run.Prepare(context.Background())
	require.NoError(t, err)
	require.NotNil(t, prepared.Confirm)

	tm, _ := m.Update(preparedMsg{prepared: prepared})
	m = asModel(t, tm)
	require.Equal(t, screenConfirm, m.screen)
	require.NotNil(t, m.pending)

	view := m.View()
	assert.Contains(t, view, "DESTRUCTIVE")
	assert.Contains(t, view, "test__mutate")

	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = asModel(t, tm)
	assert.Equal(t, screenRunning, m.screen)
	assert.True(t, m.waiting)
	assert.NotNil(t, cmd)
	assert.Nil(t, m.pending)
}

func TestModel_Mutation_DeclineFlow(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":      choiceAnswer("test__mutate", 0.95),
		"goal_achieved":    noulAnswer(0.1),
		"goal_satisfiable": noulAnswer(0.9),
	}}}
	l := newTestLoop(t, judge, fakeConstructor(noCandidates))
	m := New(context.Background(), l)
	m.run = l.NewRun("mutate it")
	m.screen = screenRunning

	prepared, _, err := m.run.Prepare(context.Background())
	require.NoError(t, err)
	tm, _ := m.Update(preparedMsg{prepared: prepared})
	m = asModel(t, tm)
	require.Equal(t, screenConfirm, m.screen)

	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = asModel(t, tm)
	assert.Equal(t, screenFinished, m.screen)
	require.NotNil(t, m.termination)
	assert.Equal(t, loop.ReasonDeclined, m.termination.Reason)

	view := m.View()
	assert.Contains(t, view, "declined")
}

func TestModel_AmbiguousTarget_PickAndResolve(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "report-v3.md")
	pathB := filepath.Join(dir, "report-old.md")
	require.NoError(t, os.WriteFile(pathA, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(pathB, []byte("x"), 0o644))

	candidates := []extract.Candidate{
		{ID: "c1", Desc: "drafts/report-v3.md", Fields: map[string]any{"path": pathA}},
		{ID: "c2", Desc: "drafts/report-old.md", Fields: map[string]any{"path": pathB}},
	}
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":            choiceAnswer("test__with_path", 0.95),
		"goal_achieved":          noulAnswer(0.1),
		"goal_satisfiable":       noulAnswer(0.9),
		"path_target":            classify.Answer{Choice: "c1", Probabilities: map[string]float64{"c1": 0.55, "c2": 0.35, "no_match": 0.1}},
		"path_target_resolvable": noulAnswer(0.2),
	}}}
	l := newTestLoop(t, judge, fakeConstructor(func(argType capabilities.ArgType) ([]extract.Candidate, error) {
		if argType != capabilities.ArgPath {
			return nil, nil
		}
		return candidates, nil
	}))
	l.PathRules = gate.PathRules{AllowedRoots: []string{dir}}
	m := New(context.Background(), l)
	m.run = l.NewRun("read the draft report")
	m.screen = screenRunning

	prepared, _, err := m.run.Prepare(context.Background())
	require.NoError(t, err)
	require.NotNil(t, prepared.Ambiguous)

	tm, _ := m.Update(preparedMsg{prepared: prepared})
	m = asModel(t, tm)
	require.Equal(t, screenAmbiguous, m.screen)
	assert.Equal(t, 0, m.ambiguousCursor)

	view := m.View()
	assert.Contains(t, view, "drafts/report-v3.md")
	assert.Contains(t, view, "drafts/report-old.md")

	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = asModel(t, tm)
	assert.Equal(t, 1, m.ambiguousCursor)

	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, tm)
	assert.True(t, m.waiting)
	require.NotNil(t, cmd)

	// Run the real resolution the Cmd would have triggered, mirroring
	// what commands.go's resolveCmd does, to verify the follow-on
	// resolvedMsg handling.
	resolved, err := m.run.ResolveAmbiguous(prepared, "c2")
	require.NoError(t, err)
	tm, _ = m.Update(resolvedMsg{prepared: resolved})
	m = asModel(t, tm)
	assert.Equal(t, screenRunning, m.screen)
	require.Len(t, m.run.State().Resolved, 1)
	assert.Equal(t, "c2", m.run.State().Resolved[0].CandidateID)
}

func TestModel_Abort_DuringRunning(t *testing.T) {
	l := newTestLoop(t, &scriptedJudge{t: t}, fakeConstructor(noCandidates))
	m := New(context.Background(), l)
	m.run = l.NewRun("goal")
	m.screen = screenRunning
	m.waiting = true

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(t, tm)
	assert.Equal(t, screenFinished, m.screen)
	assert.True(t, m.aborted)

	view := m.View()
	assert.Contains(t, view, "aborted")
}

func TestModel_ViewInput_RendersWithoutPanicking(t *testing.T) {
	l := newTestLoop(t, &scriptedJudge{t: t}, fakeConstructor(noCandidates))
	m := New(context.Background(), l)
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = asModel(t, tm)

	view := m.View()
	assert.Contains(t, view, "detent")
}

func TestModel_Finished_EnterLoopsBackToInput_QuitOnlyOnQ(t *testing.T) {
	judge := &scriptedJudge{t: t, responses: []classify.Answers{{
		"next_action":      choiceAnswer("done", 0.95),
		"goal_achieved":    noulAnswer(0.99),
		"goal_satisfiable": noulAnswer(0.9),
	}}}
	l := newTestLoop(t, judge, fakeConstructor(noCandidates))
	m := New(context.Background(), l)
	m.run = l.NewRun("first goal")
	m.screen = screenFinished
	term := loop.Termination{Reason: loop.ReasonDone}
	m.termination = &term
	m.showFindings = true // must be cleared by the reset too

	tm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, tm)
	assert.Equal(t, screenInput, m.screen, "[enter] on Finished must return to input, not quit")
	assert.Nil(t, m.run)
	assert.Nil(t, m.termination)
	assert.False(t, m.showFindings)
	assert.Equal(t, "", m.input.Value())
	assert.NotNil(t, cmd, "should restart the cursor blink")

	// A fresh goal must work normally after the reset.
	m.input.SetValue("second goal")
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, tm)
	assert.Equal(t, screenRunning, m.screen)
	require.NotNil(t, m.run)
	assert.Equal(t, "second goal", m.run.State().Goal)
}

func TestModel_Finished_QKeyQuits(t *testing.T) {
	l := newTestLoop(t, &scriptedJudge{t: t}, fakeConstructor(noCandidates))
	m := New(context.Background(), l)
	m.screen = screenFinished

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.Quit(), cmd())
}

func TestView_CentersHorizontallyWithinTerminalWidth(t *testing.T) {
	l := newTestLoop(t, &scriptedJudge{t: t}, fakeConstructor(noCandidates))
	m := New(context.Background(), l)
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
	m = asModel(t, tm)

	view := m.View()
	lines := strings.Split(view, "\n")
	var sawIndentedContent bool
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(line, "                              ") { // ~30+ leading spaces on a 200-wide term
			sawIndentedContent = true
			break
		}
	}
	assert.True(t, sawIndentedContent, "content should be padded away from the left edge on a wide terminal")
}
