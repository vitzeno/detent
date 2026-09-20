package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/shell"
	"github.com/vitzeno/detent/internal/usage"
)

// Shared fixtures for every *_test.go file in this package.

type scriptProposer struct {
	script []propose.Proposal
	n      int
}

func (s *scriptProposer) Propose(_ context.Context, _ []propose.Message) (propose.Proposal, usage.Usage, error) {
	p := s.script[s.n]
	if s.n < len(s.script)-1 {
		s.n++
	}
	return p, usage.Usage{}, nil
}

func instantRun(_ context.Context, _ string, _ func(shell.StreamEvent)) (shell.Result, error) {
	return shell.Result{Stdout: "a\nb\n"}, nil
}

type fullJudge struct{}

func (fullJudge) Ask(_ context.Context, _ classify.State, qs classify.Questions) (classify.Answers, classify.Usage, error) {
	out := classify.Answers{}
	if _, ok := qs["mutability"]; ok {
		out["mutability"] = classify.Answer{Choice: agent.MutReadOnly, Confidence: 0.8}
		out["scope_risk"] = classify.Answer{Noul: 0.1}
	}
	if _, ok := qs["result_status"]; ok {
		out["result_status"] = classify.Answer{Choice: agent.StatusClean, Confidence: 0.9}
		out["render_kind"] = classify.Answer{Choice: agent.KindInline}
		out["attention"] = classify.Answer{Noul: 0.2}
		out["goal_achieved"] = classify.Answer{Noul: 0.9}
	}
	return out, classify.Usage{}, nil
}

func testSession() *agent.Session {
	return &agent.Session{
		Proposer: &scriptProposer{script: []propose.Proposal{
			{Command: "ls -la", Rationale: "list files"},
			{Done: true, Summary: "saw two files"},
		}},
		Run:   instantRun,
		Judge: fullJudge{},
	}
}

func testUIModel() Model {
	m := New(context.Background(), testSession(), "test-model", "")
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	return m
}

func typeKey(s string) tea.KeyMsg {
	if s == "space" {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func busyUIModel() Model {
	m := testUIModel()
	m.waiting = true
	m.nav.focus = focusInput
	m.input.Focus()
	return m
}

func tableRow() *stepRow {
	return &stepRow{
		command: "ps aux",
		cmd: cmdState{ec: &agent.ExecutedCommand{
			Result: shell.Result{Stdout: "USER PID COMMAND\nroot 1 init\nmo 4821 node server.js\n"},
			Post:   &agent.PostJudgment{FromJudge: true, Status: agent.StatusClean, RenderKind: agent.KindTable},
		}},
	}
}

func tableBlock() *goalBlock {
	mkrow := func(cmd, out, kind string) *stepRow {
		return &stepRow{command: cmd, cmd: cmdState{ec: &agent.ExecutedCommand{
			Result: shell.Result{Stdout: out},
			Post:   &agent.PostJudgment{FromJudge: true, Status: agent.StatusClean, RenderKind: kind},
		}}}
	}
	steps := []*stepRow{mkrow("ps aux", "USER PID COMMAND\nroot 1 init\na 2 x\nb 3 y\nc 4 z\nd 5 w\ne 6 v\n", agent.KindTable)}
	for range 30 {
		steps = append(steps, mkrow("echo x", "x\n", agent.KindInline))
	}
	return &goalBlock{goal: "g", steps: steps}
}

func usageModel() (Model, *usage.Tracker) {
	tr := &usage.Tracker{}
	g := tr.StartGoal("find it")
	s := g.AddStep("ls -la")
	s.SetPropose(usage.Usage{PromptTokens: 100, CompletionTokens: 20, Latency: 200 * time.Millisecond, Model: "m"})
	s.SetDwell(1500 * time.Millisecond)
	s.SetExec(90*time.Millisecond, 0, 12)
	s.SetJudgePost(usage.Usage{PromptTokens: 60, Latency: 120 * time.Millisecond}, 0.2, 0.9)
	g.Finish("done", "found", false)
	sess := testSession()
	sess.Stats = tr
	m := New(context.Background(), sess, "test-model", "")
	m.layout.width, m.layout.height = 120, 40
	m.sizeViewport()
	return m, tr
}
