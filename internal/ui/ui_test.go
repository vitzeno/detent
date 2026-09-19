package ui

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/agentloop"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/shell"
)

type scriptProposer struct {
	script []propose.Proposal
	n      int
}

func (s *scriptProposer) Propose(_ context.Context, _ []propose.Message) (propose.Proposal, error) {
	p := s.script[s.n]
	if s.n < len(s.script)-1 {
		s.n++
	}
	return p, nil
}

func instantRun(_ context.Context, _ string, _ func(shell.StreamEvent)) (shell.Result, error) {
	return shell.Result{Stdout: "a\nb\n"}, nil
}

type fullJudge struct{}

func (fullJudge) Ask(_ context.Context, _ classify.State, qs classify.Questions) (classify.Answers, classify.Usage, error) {
	out := classify.Answers{}
	if _, ok := qs["mutability"]; ok {
		out["mutability"] = classify.Answer{Choice: agentloop.MutReadOnly, Confidence: 0.8}
		out["scope_risk"] = classify.Answer{Noul: 0.1}
	}
	if _, ok := qs["result_status"]; ok {
		out["result_status"] = classify.Answer{Choice: agentloop.StatusClean, Confidence: 0.9}
		out["render_kind"] = classify.Answer{Choice: agentloop.KindInline}
		out["attention"] = classify.Answer{Noul: 0.2}
		out["goal_achieved"] = classify.Answer{Noul: 0.9}
	}
	return out, classify.Usage{}, nil
}

func testSession() *agentloop.Session {
	return &agentloop.Session{
		Proposer: &scriptProposer{script: []propose.Proposal{
			{Command: "ls -la", Rationale: "list files"},
			{Done: true, Summary: "saw two files"},
		}},
		Run:   instantRun,
		Judge: fullJudge{},
	}
}

func TestUI_EndToEnd_GoalToDone(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "jev-test")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	tm.Type("what files are here?")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("ls -la")) && bytes.Contains(out, []byte("[y]"))
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(10*time.Millisecond))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})

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
	require.NotNil(t, fm.blocks[0].steps[0].post, "post-execute judgment must land on the row")
}

func TestUI_DeclineStopsGoal(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "")
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))

	tm.Type("goal")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("[y]"))
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

func TestCompletionDisagreement(t *testing.T) {
	low := agentloop.PostJudgment{FromJudge: true, GoalAchieved: 0.2}
	b := &goalBlock{steps: []*stepRow{{post: &low}}}
	require.Contains(t, completionDisagreement(b), "0.20")

	high := agentloop.PostJudgment{FromJudge: true, GoalAchieved: 0.9}
	b2 := &goalBlock{steps: []*stepRow{{post: &high}}}
	require.Empty(t, completionDisagreement(b2))

	heur := agentloop.PostJudgment{GoalAchieved: -1}
	b3 := &goalBlock{steps: []*stepRow{{post: &heur}}}
	require.Empty(t, completionDisagreement(b3), "no opinion must not warn")
}

func typeKey(s string) tea.KeyMsg {
	if s == "space" {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestUI_TypingReachesInput is the regression test for space (and q/v/j/k)
// being swallowed by navigation: with a focused input, every text key
// must land in the goal field.
func TestUI_TypingReachesInput(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "")
	m.width, m.height = 120, 40
	m.sizeViewport()

	for _, k := range []string{"w", "h", "a", "t", "space", "q", "v", "j", "k"} {
		nm, _ := m.handleKey(typeKey(k))
		m = nm.(Model)
	}
	require.Equal(t, "what qvjk", m.input.Value())
	require.Equal(t, modeInput, m.mode, "typing must not change mode or quit")
}

// TestUI_NavKeysWorkWhenInputBlurred ensures navigation still works while
// a command runs (input blurred): j/k move the cursor, space toggles.
func TestUI_NavKeysWorkWhenInputBlurred(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "")
	m.width, m.height = 120, 40
	m.sizeViewport()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{
		{command: "a"},
		{command: "b"},
	}}}
	m.input.Blur()
	m.cursor = 0
	m.follow = false

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = nm.(Model)
	require.Equal(t, 1, m.cursor)

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	m = nm.(Model)
	require.True(t, m.blocks[0].steps[1].expanded)
}

func TestUI_TabCyclesThreePanes(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "")
	m.width, m.height = 120, 40
	m.sizeViewport()
	require.Equal(t, focusInput, m.focus)

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, focusHistory, m.focus)
	require.False(t, m.input.Focused())

	// In history focus, "q" quits instead of typing.
	nm, cmd := m.handleKey(typeKey("q"))
	m = nm.(Model)
	require.NotNil(t, cmd, "q in history focus must quit")

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, focusOutput, m.focus)

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	require.Equal(t, focusInput, m.focus)
	require.True(t, m.input.Focused())
}

func TestUI_SlashCommands(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "")
	m.width, m.height = 120, 40
	m.sizeViewport()

	m.input.SetValue("/quit")
	nm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.NotNil(t, cmd, "/quit must return the quit command")
	require.Empty(t, m.blocks, "/quit must not open a goal")

	m.input.SetValue("/bogus")
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Empty(t, m.blocks)
	require.Contains(t, m.notice, "unknown command")

	m.input.SetValue("/abort")
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Equal(t, "nothing running", m.notice)
}

func TestUI_GoalSubmitMovesFocusToHistory(t *testing.T) {
	m := New(context.Background(), testSession(), "test-model", "")
	m.width, m.height = 120, 40
	m.sizeViewport()
	m.input.SetValue("real goal here")
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Len(t, m.blocks, 1)
	require.Equal(t, "real goal here", m.blocks[0].goal)
	require.Equal(t, focusHistory, m.focus)
}

func busyUIModel() Model {
	m := testUIModel()
	m.waiting = true
	m.focus = focusInput
	m.input.Focus()
	return m
}

func TestUI_SlashEntryWhileBusy(t *testing.T) {
	m := busyUIModel()

	// Plain text is ignored while busy — no goal can start mid-run.
	nm, _ := m.handleKey(typeKey("x"))
	m = nm.(Model)
	require.Empty(t, m.input.Value())
	require.Empty(t, m.blocks)

	// "/" opens slash entry; further keys complete the dropdown.
	nm, _ = m.handleKey(typeKey("/"))
	m = nm.(Model)
	require.Equal(t, "/", m.input.Value())
	require.Len(t, m.slash, 3)

	nm, _ = m.handleKey(typeKey("a"))
	m = nm.(Model)
	require.Equal(t, "/a", m.input.Value())

	// Partial + enter completes instead of submitting.
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Equal(t, "/abort ", m.input.Value())
	require.Empty(t, m.blocks)
}

func TestUI_AbortSlashWhileBusy(t *testing.T) {
	m := busyUIModel()
	aborted := false
	m.abort = func() { aborted = true }
	m.input.SetValue("/abort")
	m.updateSlash()

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.True(t, aborted)
	require.Equal(t, "abort sent", m.notice)
}

func TestUI_StartGoalIsCancellable(t *testing.T) {
	m := testUIModel()
	m.input.SetValue("some goal")
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.NotNil(t, m.abort, "pending propose must be abortable")
}

func TestUI_AbortedProposeClosesBlock(t *testing.T) {
	sess := testSession()
	m := New(context.Background(), sess, "test-model", "")
	m.width, m.height = 120, 40
	m.sizeViewport()
	m.input.SetValue("some goal")
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	require.Len(t, m.blocks, 1)

	nm, _ = m.Update(proposeMsg{err: context.Canceled})
	m = nm.(Model)
	require.True(t, m.blocks[0].ended)
	require.Equal(t, agentloop.EndAborted, m.blocks[0].end)
	require.Equal(t, focusInput, m.focus)
	require.Len(t, sess.Transcript, 1, "aborted propose adds nothing beyond the goal turn")
	require.Equal(t, "some goal", sess.Transcript[0].Content)
}

func tableRow() *stepRow {
	return &stepRow{
		command: "ps aux",
		result:  &shell.Result{Stdout: "USER PID COMMAND\nroot 1 init\nmo 4821 node server.js\n"},
		post:    &agentloop.PostJudgment{FromJudge: true, Status: agentloop.StatusClean, RenderKind: agentloop.KindTable},
	}
}

func TestUI_TableRendersInDetailZone(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{tableRow()}}}
	m.cursor = 0
	m.refreshViewport()
	v := m.View()
	assert.Contains(t, v, "USER")
	assert.Contains(t, v, "4821")
	assert.Contains(t, v, "table")
}

func TestUI_OutputNavMovesTableCursor(t *testing.T) {
	m := testUIModel()
	m.blocks = []*goalBlock{{goal: "g", steps: []*stepRow{tableRow()}}}
	m.cursor = 0
	m.focus = focusOutput

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = nm.(Model)
	require.Equal(t, 1, m.blocks[0].steps[0].tableCursor)

	// Clamped at the last row.
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = nm.(Model)
	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = nm.(Model)
	require.Equal(t, 1, m.blocks[0].steps[0].tableCursor)

	nm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = nm.(Model)
	require.Equal(t, 0, m.blocks[0].steps[0].tableCursor)
}

func TestUI_EscFromOutputReturnsToHistory(t *testing.T) {
	m := testUIModel()
	m.focus = focusOutput
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	require.Equal(t, focusHistory, m.focus)
}

func TestUI_StyledBodyKinds(t *testing.T) {
	m := testUIModel()
	mk := func(kind, out string) *stepRow {
		return &stepRow{command: "cmd", result: &shell.Result{Stdout: out},
			post: &agentloop.PostJudgment{RenderKind: kind}}
	}
	assert.Contains(t, m.styledBody(mk(agentloop.KindJSON, `{"b":2,"a":1}`)), "\n")
	assert.Contains(t, m.styledBody(mk(agentloop.KindContent, "package main\n")), "1")
	got := m.styledBody(mk(agentloop.KindDiff, "+a\n-b\n ctx\n"))
	assert.Contains(t, got, "+a")
	raw := m.styledBody(mk("unknown-kind", "plain\n"))
	assert.Equal(t, "plain", raw)
}
