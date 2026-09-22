package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/propose"
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

// confirmFunc/runFunc adapt plain functions to Confirmer/Runner for
// tests, same shape as http.HandlerFunc.
type confirmFunc func(ConfirmRequest) bool

func (f confirmFunc) Confirm(req ConfirmRequest) bool { return f(req) }

type runFunc func(context.Context, string, chan<- host.StreamEvent) (host.Result, error)

func (f runFunc) Run(ctx context.Context, command string, events chan<- host.StreamEvent) (host.Result, error) {
	return f(ctx, command, events)
}

func okRun(result host.Result) Runner {
	return runFunc(func(_ context.Context, _ string, _ chan<- host.StreamEvent) (host.Result, error) {
		return result, nil
	})
}

func TestSession_RecordFileSave(t *testing.T) {
	s := &Session{}
	s.RecordFileSave("foo.py", "-old\n+new\n")

	require.Len(t, s.Transcript, 1)
	msg := s.Transcript[0]
	assert.Equal(t, propose.RoleTool, msg.Role)
	assert.Contains(t, msg.Content, "foo.py")
	assert.Contains(t, msg.Content, "-old")
	assert.Contains(t, msg.Content, "+new")
}

func TestSession_RecordFileSave_BoundsLargeDiffs(t *testing.T) {
	s := &Session{}
	huge := strings.Repeat("x", MaxTranscriptOutputBytes+500)
	s.RecordFileSave("foo.py", huge)

	require.Len(t, s.Transcript, 1)
	assert.Contains(t, s.Transcript[0].Content, "…[truncated]")
	assert.LessOrEqual(t, len(s.Transcript[0].Content), MaxTranscriptOutputBytes+200)
}

// TestSession_RecordAbort_ClosesTranscriptAndStats covers the
// non-blocking driver path (resolver, not RunGoal): an abort during
// propose still has to close the goal turn and finish Stats.Goal.
func TestSession_RecordAbort_ClosesTranscriptAndStats(t *testing.T) {
	s := &Session{Proposer: &stubProposer{}, Runners: SingleRunner{Runner: okRun(host.Result{})}, Stats: usage.New()}
	res, err := s.BeginGoal(context.Background(), "some goal")
	require.NoError(t, err)

	s.RecordAbort(res)

	// 3 not 2: BeginGoal's own probe turn lands between the goal and
	// the abort note.
	require.Len(t, s.Transcript, 3, "aborted propose must close the goal turn it opened")
	assert.Equal(t, "some goal", s.Transcript[0].Content)
	assert.Equal(t, propose.RoleTool, s.Transcript[1].Role, "probe turn")
	assert.Equal(t, "[goal ended by human: aborted]", s.Transcript[2].Content)
	assert.False(t, res.Stats.Ended.IsZero(), "RecordAbort must finish the Stats.Goal BeginGoal started")
	assert.Equal(t, string(EndAborted), res.Stats.End)
}

// TestSession_RecordAbort_NilResOnlyClosesTranscript covers an abort
// before BeginGoal returns: no Stats.Goal to finish, but the
// transcript still needs closing.
func TestSession_RecordAbort_NilResOnlyClosesTranscript(t *testing.T) {
	s := &Session{}
	s.RecordAbort(nil)
	require.Len(t, s.Transcript, 1)
	assert.Equal(t, "[goal ended by human: aborted]", s.Transcript[0].Content)
}

func TestRunGoal_DonePath(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{
		{Command: "ls", Rationale: "list"},
		{Done: true, Summary: "saw the files"},
	}}
	var confirmed []string
	s := &Session{
		Proposer: stub,
		Confirm: confirmFunc(func(req ConfirmRequest) bool {
			confirmed = append(confirmed, req.Command)
			return true
		}),
		Runners: SingleRunner{Runner: okRun(host.Result{Stdout: "a\nb\n"})},
	}

	res, err := s.RunGoal(context.Background(), "what files are here?")
	require.NoError(t, err)
	assert.Equal(t, EndDone, res.End)
	assert.Equal(t, "saw the files", res.Summary)
	require.Len(t, res.Commands, 1)
	assert.Empty(t, confirmed, "ls isn't flagged Dangerous, so it runs without a confirm")

	// 5 not 4: BeginGoal's own probe turn lands between the goal and
	// the first proposed command.
	require.Len(t, s.Transcript, 5)
	assert.Equal(t, propose.RoleUser, s.Transcript[0].Role)
	assert.Equal(t, "what files are here?", s.Transcript[0].Content)
	assert.Equal(t, propose.RoleTool, s.Transcript[1].Role, "probe turn")
	assert.Equal(t, propose.RoleAssistant, s.Transcript[2].Role)
	assert.Equal(t, propose.RoleTool, s.Transcript[3].Role)
	assert.Contains(t, s.Transcript[3].Content, "a\nb")
	assert.True(t, s.GoalsDone == 1)
}

func TestRunGoal_SecondGoalSeesFirst(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{
		{Command: "echo forty-two", Rationale: "print"},
		{Done: true, Summary: "printed"},
		// Answering the second goal from the first's output: the nudge
		// sends it back, and it runs something of its own.
		{Done: true, Summary: "the last command printed forty-two"},
		{Command: "echo forty-two", Rationale: "check again"},
		{Done: true, Summary: "it printed forty-two"},
	}}
	s := &Session{
		Proposer: stub,
		Confirm:  confirmFunc(func(ConfirmRequest) bool { return true }),
		Runners:  SingleRunner{Runner: okRun(host.Result{Stdout: "forty-two\n"})},
	}

	_, err := s.RunGoal(context.Background(), "print something")
	require.NoError(t, err)
	res2, err := s.RunGoal(context.Background(), "what did the last command print?")
	require.NoError(t, err)
	assert.Equal(t, EndDone, res2.End)
	assert.Len(t, res2.Commands, 1, "a goal answered from stale transcript is sent back to run something")

	require.GreaterOrEqual(t, len(stub.seen), 3)
	secondGoalFirstCall := stub.seen[2]
	var roles []string
	for _, m := range secondGoalFirstCall {
		roles = append(roles, string(m.Role)+":"+truncateStr(m.Content, 40))
	}
	joined := strings.Join(roles, "\n")
	assert.Contains(t, joined, "forty-two", "second goal must see first goal's output")
	// The trailing entry can be a probe turn now, not the user message —
	// what matters is the most recent *user* turn is this goal's own text.
	lastUserIdx := -1
	for i, m := range secondGoalFirstCall {
		if m.Role == propose.RoleUser {
			lastUserIdx = i
		}
	}
	require.GreaterOrEqual(t, lastUserIdx, 0, "no user turn found")
	assert.Equal(t, "what did the last command print?", secondGoalFirstCall[lastUserIdx].Content)
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
		Confirm: confirmFunc(func(req ConfirmRequest) bool {
			confirmed = append(confirmed, req.Command)
			return false // decline the one command that ever reaches confirm
		}),
		Runners: SingleRunner{Runner: okRun(host.Result{})},
	}

	res, err := s.RunGoal(context.Background(), "clean up")
	require.NoError(t, err)
	assert.Equal(t, EndDeclined, res.End)
	// ls ran without confirm; only the flagged rm -rf reached Confirm.
	require.Len(t, res.Commands, 1)
	assert.Equal(t, []string{"rm -rf /tmp/x"}, confirmed)

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
		Confirm:    confirmFunc(func(ConfirmRequest) bool { return true }),
		Runners:    SingleRunner{Runner: okRun(host.Result{})},
		StepBudget: 2,
	}

	res, err := s.RunGoal(context.Background(), "keep going")
	require.NoError(t, err)
	assert.Equal(t, EndBudget, res.End)
	assert.Len(t, res.Commands, 2)
	assert.Equal(t, 2, stub.calls, "proposer called exactly budget times")
}

func TestRunGoal_OnlyDangerousCommandsNeedConfirm(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{
		{Command: "echo harmless", Rationale: "safe-looking"},
		{Command: "rm -rf /tmp/x", Rationale: "flagged by FlagDanger"},
		{Done: true, Summary: "done"},
	}}
	var calls []string
	s := &Session{
		Proposer: stub,
		Confirm: confirmFunc(func(req ConfirmRequest) bool {
			calls = append(calls, req.Command)
			return true
		}),
		Runners: SingleRunner{Runner: okRun(host.Result{})},
	}
	res, err := s.RunGoal(context.Background(), "g")
	require.NoError(t, err)
	require.Len(t, res.Commands, 2, "both ran — the safe one without confirm, the flagged one after approval")
	assert.Equal(t, []string{"rm -rf /tmp/x"}, calls, "only the command Dangerous flags reaches confirm")
}

func TestRunGoal_NoConfirmFuncStillOKForAnAllSafeGoal(t *testing.T) {
	// Confirm==nil fails closed even though this goal never needed it.
	stub := &stubProposer{script: []propose.Proposal{{Command: "echo harmless", Rationale: "safe"}}}
	s := &Session{Proposer: stub, Confirm: nil, Runners: SingleRunner{Runner: okRun(host.Result{})}}
	_, err := s.RunGoal(context.Background(), "g")
	assert.ErrorContains(t, err, "no Confirm")
}

func TestRunGoal_NoConfirmFailsClosed(t *testing.T) {
	s := &Session{Proposer: &stubProposer{}, Confirm: nil, Runners: SingleRunner{Runner: okRun(host.Result{})}}
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
		Confirm: confirmFunc(func(req ConfirmRequest) bool {
			sawDangerous = req.Dangerous
			sawNote = req.RiskNote
			return false // stop after capturing
		}),
		Runners: SingleRunner{Runner: okRun(host.Result{})},
	}
	_, err := s.RunGoal(context.Background(), "g")
	require.NoError(t, err)
	assert.True(t, sawDangerous)
	assert.Contains(t, sawNote, "recursive remove")
}

func TestRunGoal_EmptyGoalRejected(t *testing.T) {
	s := &Session{Proposer: &stubProposer{}, Confirm: confirmFunc(func(ConfirmRequest) bool { return true })}
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
		Confirm: confirmFunc(func(req ConfirmRequest) bool {
			flagged = req.Dangerous
			return false
		}),
		Runners: SingleRunner{Runner: okRun(host.Result{})},
		Judge:   &fakeJudge{noul: 0.9},
	}
	_, err := s.RunGoal(context.Background(), "g")
	require.NoError(t, err)
	assert.True(t, flagged, "high Jev Noul must escalate to Dangerous")

	for _, tc := range []struct {
		name  string
		judge Judge
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
				Confirm: confirmFunc(func(req ConfirmRequest) bool {
					got = req.Dangerous
					return false
				}),
				Runners: SingleRunner{Runner: okRun(host.Result{})},
				Judge:   tc.judge,
			}
			_, err := s.RunGoal(context.Background(), "g")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRecordStep_DwellOnlyRecordedWhenPositive(t *testing.T) {
	s := &Session{}
	res := &GoalResult{Goal: "g"}
	res.Stats = usage.New().StartGoal("g")

	declined := s.RecordStep(res, propose.Proposal{Command: "ls"}, PreJudgment{}, usage.Usage{}, 0)
	require.Len(t, res.Stats.Steps, 1)
	assert.Zero(t, declined.Dwell, "no confirm shown (dwell<=0) must not call SetDwell")

	confirmed := s.RecordStep(res, propose.Proposal{Command: "rm -rf x"}, PreJudgment{}, usage.Usage{}, 5*time.Millisecond)
	require.Len(t, res.Stats.Steps, 2)
	assert.Equal(t, 5*time.Millisecond, confirmed.Dwell)
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

// fakeSnapshotRunner is a Runner that also implements Snapshotter.
type fakeSnapshotRunner struct {
	Runner
	snapID       SnapshotID
	snapErr      error
	rollErr      error
	rolledBackTo SnapshotID
}

func (f *fakeSnapshotRunner) Snapshot(context.Context) (SnapshotID, error) {
	return f.snapID, f.snapErr
}

func (f *fakeSnapshotRunner) Rollback(_ context.Context, id SnapshotID) error {
	f.rolledBackTo = id
	return f.rollErr
}

// sandboxSelector always routes to sandbox, for testing Snapshot/Rollback.
type sandboxSelector struct{ sandbox Runner }

func (s sandboxSelector) Select(PreJudgment) (Runner, string) { return s.sandbox, RunModeSandbox }
func (s sandboxSelector) Probe() Runner                       { return s.sandbox }
func (s sandboxSelector) Sandbox() Runner                     { return s.sandbox }

func TestSession_Snapshot_NilRunners(t *testing.T) {
	s := &Session{}
	id, ok, err := s.Snapshot(context.Background())
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, id)
}

func TestSession_Snapshot_NoSandboxWired(t *testing.T) {
	s := &Session{Runners: SingleRunner{Runner: okRun(host.Result{})}}
	id, ok, err := s.Snapshot(context.Background())
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, id)
}

func TestSession_Snapshot_UsesSandboxSnapshotter(t *testing.T) {
	fake := &fakeSnapshotRunner{snapID: "snap-1"}
	s := &Session{Runners: sandboxSelector{sandbox: fake}}
	id, ok, err := s.Snapshot(context.Background())
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, SnapshotID("snap-1"), id)
}

func TestSession_Rollback_NoSandboxWired(t *testing.T) {
	s := &Session{Runners: SingleRunner{Runner: okRun(host.Result{})}}
	res := &GoalResult{Commands: []*ExecutedCommand{{SnapshotID: "snap-1", TranscriptMark: 2}}}
	ok, err := s.Rollback(context.Background(), res, 1, false)
	require.NoError(t, err)
	assert.False(t, ok)
}

// Undoing step 1 restores the goal's baseline: the checkpoint taken
// before any step ran.
func TestSession_Rollback_FirstStepUsesBaseline(t *testing.T) {
	fake := &fakeSnapshotRunner{}
	s := &Session{Runners: sandboxSelector{sandbox: fake}}
	res := &GoalResult{
		Baseline: "snap-0",
		Commands: []*ExecutedCommand{{SnapshotID: "snap-1"}},
	}
	ok, err := s.Rollback(context.Background(), res, 1, false)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, SnapshotID("snap-0"), fake.rolledBackTo)
	assert.Empty(t, res.Commands, "step 1 itself is undone")
}

func TestSession_Rollback_NoBaselineIsAnError(t *testing.T) {
	fake := &fakeSnapshotRunner{}
	s := &Session{Runners: sandboxSelector{sandbox: fake}}
	res := &GoalResult{Commands: []*ExecutedCommand{{SnapshotID: "snap-1"}}}

	ok, err := s.Rollback(context.Background(), res, 1, false)
	assert.True(t, ok)
	assert.Error(t, err, "nothing captured before step 1 to restore")
	assert.Len(t, res.Commands, 1, "a failed rollback must not truncate")
}

func TestSession_Rollback_TruncatesCommandsAndTranscript(t *testing.T) {
	fake := &fakeSnapshotRunner{}
	s := &Session{Runners: sandboxSelector{sandbox: fake}, Transcript: make([]propose.Message, 6)}
	res := &GoalResult{Commands: []*ExecutedCommand{
		{Command: "one", SnapshotID: "snap-1", TranscriptMark: 2},
		{Command: "two", SnapshotID: "snap-2", TranscriptMark: 4},
		{Command: "three", SnapshotID: "snap-3", TranscriptMark: 6},
	}}

	// Undo step 2 onward: restores the checkpoint from after step 1.
	ok, err := s.Rollback(context.Background(), res, 2, false)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, SnapshotID("snap-1"), fake.rolledBackTo)
	require.Len(t, res.Commands, 1, "steps 2 and 3 are both undone")
	assert.Equal(t, "one", res.Commands[0].Command)
	assert.Len(t, s.Transcript, 2, "transcript truncates to step 1's mark")
}

func TestSession_Rollback_StepOutOfRange(t *testing.T) {
	fake := &fakeSnapshotRunner{}
	s := &Session{Runners: sandboxSelector{sandbox: fake}}
	res := &GoalResult{Commands: []*ExecutedCommand{{SnapshotID: "snap-1", TranscriptMark: 2}}}

	ok, err := s.Rollback(context.Background(), res, 5, false)
	assert.True(t, ok, "a sandbox is wired; this is a real error, not a silent no-op")
	assert.Error(t, err)
}

func TestSession_Rollback_PredecessorNotSandboxed(t *testing.T) {
	fake := &fakeSnapshotRunner{}
	s := &Session{Runners: sandboxSelector{sandbox: fake}}
	res := &GoalResult{Baseline: "snap-0", Commands: []*ExecutedCommand{
		{Command: "ran on host"}, // no checkpoint to restore step 2 to
		{Command: "sandboxed", SnapshotID: "snap-2"},
	}}

	ok, err := s.Rollback(context.Background(), res, 2, false)
	assert.True(t, ok)
	assert.Error(t, err)
	assert.Len(t, res.Commands, 2, "a failed rollback must not truncate")
}

func TestExecute_SnapshotsAfterSandboxedCommand(t *testing.T) {
	fake := &fakeSnapshotRunner{snapID: "snap-x", Runner: okRun(host.Result{Stdout: "ok\n"})}
	s := &Session{Runners: sandboxSelector{sandbox: fake}, Stats: usage.New()}
	res := &GoalResult{Goal: "g"}
	res.Stats = s.Stats.StartGoal("g")
	ustep := res.Stats.AddStep("echo ok")

	ec, err := s.Execute(context.Background(), res, ustep, propose.Proposal{Command: "echo ok"}, PreJudgment{RunMode: RunModeSandbox}, nil)
	require.NoError(t, err)
	assert.Equal(t, SnapshotID("snap-x"), ec.SnapshotID)
}

func TestExecute_NoSnapshotWhenNotSandboxed(t *testing.T) {
	fake := &fakeSnapshotRunner{snapID: "snap-x", Runner: okRun(host.Result{Stdout: "ok\n"})}
	s := &Session{Runners: sandboxSelector{sandbox: fake}, Stats: usage.New()}
	res := &GoalResult{Goal: "g"}
	res.Stats = s.Stats.StartGoal("g")
	ustep := res.Stats.AddStep("echo ok")

	ec, err := s.Execute(context.Background(), res, ustep, propose.Proposal{Command: "echo ok"}, PreJudgment{RunMode: RunModeHost}, nil)
	require.NoError(t, err)
	assert.Empty(t, ec.SnapshotID)
}

// A goal that ends with nothing run is almost always the model
// answering from an earlier goal's output: the transcript spans the
// whole session, and that output describes the past. ProposeNext sends
// it back once with a nudge — but takes no for an answer, so a model
// that insists can't be looped forever.
func TestProposeNext_EmptyDoneIsSentBackOnce(t *testing.T) {
	for _, tc := range []struct {
		name      string
		second    propose.Proposal
		wantCmd   string
		wantDone  bool
		wantCalls int
	}{
		{
			name:      "reconsiders and runs something",
			second:    propose.Proposal{Command: "ls -la", Rationale: "look now"},
			wantCmd:   "ls -la",
			wantCalls: 2,
		},
		{
			name:      "insists, and is believed",
			second:    propose.Proposal{Done: true, Summary: "nothing to run"},
			wantDone:  true,
			wantCalls: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &stubProposer{script: []propose.Proposal{
				{Done: true, Summary: "already know the answer"},
				tc.second,
			}}
			s := &Session{Proposer: stub, Runners: SingleRunner{Runner: okRun(host.Result{})}}
			_, err := s.BeginGoal(context.Background(), "list the files")
			require.NoError(t, err)

			p, _, _, err := s.ProposeNext(context.Background(), "list the files")
			require.NoError(t, err)
			assert.Equal(t, tc.wantDone, p.Done)
			assert.Equal(t, tc.wantCmd, p.Command)
			require.Len(t, stub.seen, tc.wantCalls)

			// The nudge steers this one call and never becomes history.
			last := stub.seen[len(stub.seen)-1]
			assert.Contains(t, last[len(last)-1].Content, "Nothing has run for this goal yet")
			for _, m := range s.Transcript {
				assert.NotContains(t, m.Content, "Nothing has run for this goal yet",
					"the nudge must not be appended to the transcript")
			}
		})
	}
}

// Once a command has run for the goal, done is taken at face value —
// the backstop must not re-ask on every completion.
func TestProposeNext_DoneAfterACommandIsAccepted(t *testing.T) {
	stub := &stubProposer{script: []propose.Proposal{
		{Command: "ls", Rationale: "list"},
		{Done: true, Summary: "saw the files"},
	}}
	s := &Session{
		Proposer: stub,
		Confirm:  confirmFunc(func(ConfirmRequest) bool { return true }),
		Runners:  SingleRunner{Runner: okRun(host.Result{Stdout: "a\n"})},
	}
	res, err := s.RunGoal(context.Background(), "what files are here?")
	require.NoError(t, err)
	assert.Equal(t, EndDone, res.End)
	assert.Equal(t, "saw the files", res.Summary)
	assert.Equal(t, 2, stub.calls, "no extra propose call once a command has run")
}

// A model thrown by a failed command often answers in prose about it
// rather than proposing the next step. That used to end the goal with
// a proposer error; now the shape is asked for once before giving up.
func TestProposeNext_ProseIsAskedToTryAgain(t *testing.T) {
	for _, tc := range []struct {
		name    string
		second  propose.Proposal
		secErr  error
		wantCmd string
		wantErr bool
	}{
		{
			name:    "recovers",
			second:  propose.Proposal{Command: "ls -la", Rationale: "look"},
			wantCmd: "ls -la",
		},
		{
			name:    "still prose, so the goal ends",
			secErr:  fmt.Errorf("%w: no JSON object in %q", propose.ErrNotAProposal, "still talking"),
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &proseProposer{
				first:  fmt.Errorf("%w: no JSON object in %q", propose.ErrNotAProposal, "The command failed."),
				second: tc.second, secondErr: tc.secErr,
			}
			s := &Session{Proposer: stub, Runners: SingleRunner{Runner: okRun(host.Result{})}}
			_, err := s.BeginGoal(context.Background(), "fix it")
			require.NoError(t, err)

			p, _, _, err := s.ProposeNext(context.Background(), "fix it")
			require.Equal(t, 2, stub.calls, "asked exactly once more")
			if tc.wantErr {
				require.Error(t, err, "a model that keeps talking ends the goal")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantCmd, p.Command)
			for _, m := range s.Transcript {
				assert.NotContains(t, m.Content, "prose, not a proposal",
					"the nudge must not become history")
			}
		})
	}
}

// proseProposer fails the first call the way a chatty model does, then
// answers however the case wants.
type proseProposer struct {
	first     error
	second    propose.Proposal
	secondErr error
	calls     int
}

func (p *proseProposer) Propose(context.Context, []propose.Message) (propose.Proposal, usage.Usage, error) {
	p.calls++
	if p.calls == 1 {
		return propose.Proposal{}, usage.Usage{}, p.first
	}
	return p.second, usage.Usage{}, p.secondErr
}

// Probe output went into the transcript unbounded while every other
// path was capped. One ps on a busy machine is 200KB, twice the whole
// transcript budget, resent on every later propose until compaction
// threw it away.
func TestBeginGoal_ProbeOutputIsBounded(t *testing.T) {
	huge := strings.Repeat("a process line that goes on\n", 20_000)
	s := &Session{
		Proposer: &stubProposer{script: []propose.Proposal{{Done: true}}},
		Confirm:  confirmFunc(func(ConfirmRequest) bool { return true }),
		Runners:  SingleRunner{Runner: okRun(host.Result{Stdout: huge})},
	}

	_, err := s.BeginGoal(context.Background(), "look around")
	require.NoError(t, err)

	for i, m := range s.Transcript {
		assert.LessOrEqual(t, len(m.Content), MaxTranscriptOutputBytes+64,
			"message %d is %d bytes", i, len(m.Content))
	}
}
