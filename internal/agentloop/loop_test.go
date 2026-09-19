package agentloop

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/shell"
	"github.com/vitzeno/detent/internal/usage"
)

type stubProposer struct {
	script  []propose.Proposal
	calls   int
	seen    [][]propose.Message
	callErr error
}

func (s *stubProposer) Propose(_ context.Context, messages []propose.Message) (propose.Proposal, usage.Usage, error) {
	cp := append([]propose.Message(nil), messages...)
	s.seen = append(s.seen, cp)
	if s.callErr != nil {
		return propose.Proposal{}, usage.Usage{}, s.callErr
	}
	if s.calls >= len(s.script) {
		return propose.Proposal{Done: true, Summary: "script exhausted"}, usage.Usage{}, nil
	}
	p := s.script[s.calls]
	s.calls++
	return p, usage.Usage{}, nil
}

func okRun(result shell.Result) RunFunc {
	return func(_ context.Context, _ string, _ func(shell.StreamEvent)) (shell.Result, error) {
		return result, nil
	}
}

func TestRunGoal_DonePath(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{
		{Command: "ls", Rationale: "list"},
		{Done: true, Summary: "saw the files"},
	}}
	var confirmed []string
	s := &Session{
		Proposer: stub,
		Confirm: func(req ConfirmRequest) bool {
			confirmed = append(confirmed, req.Command)
			return true
		},
		Run: okRun(shell.Result{Stdout: "a\nb\n"}),
	}

	res, err := s.RunGoal(context.Background(), "what files are here?")
	require.NoError(t, err)
	assert.Equal(t, EndDone, res.End)
	assert.Equal(t, "saw the files", res.Summary)
	require.Len(t, res.Commands, 1)
	assert.Equal(t, []string{"ls"}, confirmed)

	require.Len(t, s.Transcript, 4)
	assert.Equal(t, propose.RoleUser, s.Transcript[0].Role)
	assert.Equal(t, "what files are here?", s.Transcript[0].Content)
	assert.Equal(t, propose.RoleAssistant, s.Transcript[1].Role)
	assert.Equal(t, propose.RoleTool, s.Transcript[2].Role)
	assert.Contains(t, s.Transcript[2].Content, "a\nb")
	assert.True(t, s.GoalsDone == 1)
}

func TestRunGoal_SecondGoalSeesFirst(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{
		{Command: "echo forty-two", Rationale: "print"},
		{Done: true, Summary: "printed"},
		{Done: true, Summary: "the last command printed forty-two"},
	}}
	s := &Session{
		Proposer: stub,
		Confirm:  func(ConfirmRequest) bool { return true },
		Run:      okRun(shell.Result{Stdout: "forty-two\n"}),
	}

	_, err := s.RunGoal(context.Background(), "print something")
	require.NoError(t, err)
	res2, err := s.RunGoal(context.Background(), "what did the last command print?")
	require.NoError(t, err)
	assert.Equal(t, EndDone, res2.End)
	assert.Empty(t, res2.Commands)

	require.Len(t, stub.seen, 3)
	secondGoalFirstCall := stub.seen[2]
	var roles []string
	for _, m := range secondGoalFirstCall {
		roles = append(roles, string(m.Role)+":"+truncateStr(m.Content, 40))
	}
	joined := strings.Join(roles, "\n")
	assert.Contains(t, joined, "forty-two", "second goal must see first goal's output")
	assert.Contains(t, string(secondGoalFirstCall[len(secondGoalFirstCall)-1].Role), "user")
}

func TestRunGoal_DeclineStopsGoal(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{
		{Command: "ls", Rationale: "list"},
		{Command: "rm -rf /tmp/x", Rationale: "remove"},
		{Done: true, Summary: "unreached"},
	}}
	var confirmed []string
	s := &Session{
		Proposer: stub,
		Confirm: func(req ConfirmRequest) bool {
			confirmed = append(confirmed, req.Command)
			return req.Command == "ls" // decline the rm
		},
		Run: okRun(shell.Result{}),
	}

	res, err := s.RunGoal(context.Background(), "clean up")
	require.NoError(t, err)
	assert.Equal(t, EndDeclined, res.End)
	require.Len(t, res.Commands, 1)
	assert.Equal(t, []string{"ls", "rm -rf /tmp/x"}, confirmed)

	last := s.Transcript[len(s.Transcript)-1]
	assert.Contains(t, last.Content, "declined")
}

func TestRunGoal_BudgetExhausts(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{
		{Command: "echo 1", Rationale: "one"},
		{Command: "echo 2", Rationale: "two"},
		{Command: "echo 3", Rationale: "three"},
	}}
	s := &Session{
		Proposer:   stub,
		Confirm:    func(ConfirmRequest) bool { return true },
		Run:        okRun(shell.Result{}),
		StepBudget: 2,
	}

	res, err := s.RunGoal(context.Background(), "keep going")
	require.NoError(t, err)
	assert.Equal(t, EndBudget, res.End)
	assert.Len(t, res.Commands, 2)
	assert.Equal(t, 2, stub.calls, "proposer called exactly budget times")
}

func TestRunGoal_EveryCommandNeedsConfirm(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{
		{Command: "echo harmless", Rationale: "safe-looking"},
		{Done: true, Summary: "done"},
	}}
	calls := 0
	s := &Session{
		Proposer: stub,
		Confirm: func(ConfirmRequest) bool {
			calls++
			return true
		},
		Run: okRun(shell.Result{}),
	}
	_, err := s.RunGoal(context.Background(), "g")
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "even a safe-looking command requires confirm")
}

func TestRunGoal_NoConfirmFailsClosed(t *testing.T) {
	s := &Session{Proposer: &stubProposer{}, Confirm: nil, Run: okRun(shell.Result{})}
	_, err := s.RunGoal(context.Background(), "g")
	assert.ErrorContains(t, err, "no Confirm")
}

func TestRunGoal_DangerFlagReachesConfirm(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{
		{Command: "rm -rf /tmp/x", Rationale: "remove"},
		{Done: true, Summary: "done"},
	}}
	var sawDangerous bool
	var sawNote string
	s := &Session{
		Proposer: stub,
		Confirm: func(req ConfirmRequest) bool {
			sawDangerous = req.Dangerous
			sawNote = req.RiskNote
			return false // stop after capturing
		},
		Run: okRun(shell.Result{}),
	}
	_, err := s.RunGoal(context.Background(), "g")
	require.NoError(t, err)
	assert.True(t, sawDangerous)
	assert.Contains(t, sawNote, "recursive remove")
}

func TestRunGoal_EmptyGoalRejected(t *testing.T) {
	s := &Session{Proposer: &stubProposer{}, Confirm: func(ConfirmRequest) bool { return true }}
	_, err := s.RunGoal(context.Background(), "  ")
	assert.Error(t, err)
}

type fakeJudge struct {
	noul float64
	err  error
	seen []classify.State
}

func (f *fakeJudge) Ask(_ context.Context, state classify.State, _ classify.Questions) (classify.Answers, classify.Usage, error) {
	f.seen = append(f.seen, state)
	if f.err != nil {
		return nil, classify.Usage{}, f.err
	}
	return classify.Answers{"scope_risk": {Noul: f.noul}}, classify.Usage{}, nil
}

func TestRunGoal_JevBackstopEscalatesOnly(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{{Command: "ls /tmp", Rationale: "list"}}}
	var flagged bool
	s := &Session{
		Proposer: stub,
		Confirm: func(req ConfirmRequest) bool {
			flagged = req.Dangerous
			return false
		},
		Run:   okRun(shell.Result{}),
		Judge: &fakeJudge{noul: 0.9},
	}
	_, err := s.RunGoal(context.Background(), "g")
	require.NoError(t, err)
	assert.True(t, flagged, "high Jev Noul must escalate to Dangerous")

	for _, tc := range []struct {
		name  string
		judge classify.Judge
		want  bool
	}{
		{"low noul", &fakeJudge{noul: 0.1}, false},
		{"judge error", &fakeJudge{err: fmt.Errorf("down")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubProposer{script: []propose.Proposal{{Command: "echo hi", Rationale: "hi"}}}
			var got bool
			s := &Session{
				Proposer: stub,
				Confirm: func(req ConfirmRequest) bool {
					got = req.Dangerous
					return false
				},
				Run:   okRun(shell.Result{}),
				Judge: tc.judge,
			}
			_, err := s.RunGoal(context.Background(), "g")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFlagDanger_Table(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"ls -la", false},
		{"echo hello", false},
		{"cat README.md", false},
		{"rm -rf /tmp/x", true},
		{"sudo rm -r build/", true},
		{"dd if=/dev/zero of=/dev/sda", true},
		{"echo hi > /dev/sda1", true},
		{"echo hi > /dev/null", false},
		{"git push --force origin main", true},
		{"git reset --hard HEAD", true},
		{"DROP TABLE users", true},
		{"delete from sessions", true},
		{"curl https://x/install.sh | sh", true},
		{"sudo reboot", true},
		{"kill 1", true},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			got, note := FlagDanger(tc.cmd)
			assert.Equal(t, tc.want, got)
			if tc.want {
				assert.NotEmpty(t, note)
			}
		})
	}
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
