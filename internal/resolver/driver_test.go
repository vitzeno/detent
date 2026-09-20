package resolver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/ui"
	"github.com/vitzeno/detent/internal/usage"
)

// These tests drive a real *agent.Session through Resolver — coverage
// ui's own tests can't provide, since package ui can't import this
// package back (see testutil_test.go's fakeDriver). This is what
// actually proves convert.go/driver.go/stream.go translate correctly.

type scriptProposer struct {
	script []propose.Proposal
	n      int
}

func (s *scriptProposer) Propose(_ context.Context, _ []propose.Message) (propose.Proposal, usage.Usage, error) {
	p := s.script[s.n]
	if s.n < len(s.script)-1 {
		s.n++
	}
	return p, usage.Usage{PromptTokens: 10, CompletionTokens: 5}, nil
}

type runFunc func(context.Context, string, chan<- host.StreamEvent) (host.Result, error)

func (f runFunc) Run(ctx context.Context, command string, events chan<- host.StreamEvent) (host.Result, error) {
	return f(ctx, command, events)
}

func okRun(result host.Result) agent.Runner {
	return runFunc(func(_ context.Context, _ string, _ chan<- host.StreamEvent) (host.Result, error) {
		return result, nil
	})
}

// modeSelector always reports mode, regardless of PreJudgment; used to
// prove RunMode survives the agent -> resolver -> ui round trip.
type modeSelector struct {
	runner agent.Runner
	mode   string
}

func (s modeSelector) Select(agent.PreJudgment) (agent.Runner, string) { return s.runner, s.mode }
func (s modeSelector) Probe() agent.Runner                             { return s.runner }
func (s modeSelector) Sandbox() agent.Runner                           { return s.runner }

func TestResolver_GoalToDone_RoundTrip(t *testing.T) {
	sess := &agent.Session{
		Proposer: &scriptProposer{script: []propose.Proposal{
			{Command: "ls -la", Rationale: "list files"},
			{Done: true, Summary: "saw two files"},
		}},
		Runners: modeSelector{runner: okRun(host.Result{Stdout: "a\nb\n"}), mode: agent.RunModeSandbox},
		Stats:   usage.New(),
	}
	r := New(sess)
	ctx := context.Background()

	res, err := r.BeginGoal(ctx, "what files are here?")
	require.NoError(t, err)
	require.Equal(t, "what files are here?", res.Goal)

	p, pre, used, err := r.ProposeNext(ctx, "what files are here?")
	require.NoError(t, err)
	assert.Equal(t, "ls -la", p.Command)
	assert.False(t, pre.Dangerous, "ls -la must not be flagged")
	assert.Equal(t, "sandbox", pre.RunMode, "RunMode must survive the agent -> resolver -> ui round trip")
	assert.Equal(t, 10, used.PromptTokens)

	step := r.RecordStep(res, p, pre, used, 0)

	ec, err := r.Execute(ctx, res, step, p, pre, nil)
	require.NoError(t, err)
	assert.Equal(t, "a\nb\n", ec.Result.Stdout)
	require.Len(t, res.Commands, 1, "Execute must append onto the same GoalResult the caller holds")
	assert.Same(t, ec, res.Commands[0], "Execute's return must be the same object appended, not a copy")

	post := r.JudgeResult(ctx, "what files are here?", ec.Command, ec.Result, step)
	assert.False(t, post.FromJudge, "no Judge wired: heuristic fallback")
	assert.NotEmpty(t, post.RenderKind)

	done, _, _, err := r.ProposeNext(ctx, "what files are here?")
	require.NoError(t, err)
	require.True(t, done.Done)
	r.RecordDone(res, done)

	assert.Equal(t, ui.EndDone, res.End)
	assert.Equal(t, "saw two files", res.Summary)
}

// snapshotRunner is a Runner that also implements agent.Snapshotter,
// for proving Rollback/SnapshotID survive the agent -> resolver -> ui
// round trip (agent's own tests already cover the edge cases).
type snapshotRunner struct {
	agent.Runner
	rolledBackTo agent.SnapshotID
}

func (f *snapshotRunner) Snapshot(context.Context) (agent.SnapshotID, error) {
	return "snap-1", nil
}

func (f *snapshotRunner) Rollback(_ context.Context, id agent.SnapshotID) error {
	f.rolledBackTo = id
	return nil
}

func TestResolver_Rollback_RoundTrip(t *testing.T) {
	fake := &snapshotRunner{Runner: okRun(host.Result{Stdout: "ok\n"})}
	sess := &agent.Session{
		Proposer: &scriptProposer{script: []propose.Proposal{{Command: "echo ok"}}},
		Runners:  modeSelector{runner: fake, mode: agent.RunModeSandbox},
		Stats:    usage.New(),
	}
	r := New(sess)
	ctx := context.Background()

	res, err := r.BeginGoal(ctx, "goal")
	require.NoError(t, err)
	p, pre, used, err := r.ProposeNext(ctx, "goal")
	require.NoError(t, err)
	step := r.RecordStep(res, p, pre, used, 0)
	ec, err := r.Execute(ctx, res, step, p, pre, nil)
	require.NoError(t, err)
	assert.Equal(t, "snap-1", ec.SnapshotID, "SnapshotID must survive the agent -> resolver -> ui round trip")

	ok, err := r.Rollback(ctx, res, 1)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, agent.SnapshotID("snap-1"), fake.rolledBackTo)
	assert.Len(t, res.Commands, 1, "step 1 itself must survive rolling back to step 1")
}

func TestResolver_DeclineNeverExecutes(t *testing.T) {
	sess := &agent.Session{
		Proposer: &scriptProposer{script: []propose.Proposal{
			{Command: "rm -rf /tmp/x", Rationale: "remove"},
		}},
		Runners: agent.SingleRunner{Runner: okRun(host.Result{})},
		Stats:   usage.New(),
	}
	r := New(sess)
	ctx := context.Background()

	res, err := r.BeginGoal(ctx, "clean up")
	require.NoError(t, err)
	p, pre, used, err := r.ProposeNext(ctx, "clean up")
	require.NoError(t, err)
	require.True(t, pre.Dangerous, "rm -rf must be flagged by the regex backstop")

	r.RecordStep(res, p, pre, used, 50*time.Millisecond)
	r.RecordDecline(res, p.Command)

	assert.Equal(t, ui.EndDeclined, res.End)
	assert.Empty(t, res.Commands, "declined command must never execute")
}

func TestResolver_ReadWriteFile(t *testing.T) {
	r := New(&agent.Session{})
	path := filepath.Join(t.TempDir(), "f.txt")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644))

	content, truncated, maxBytes, err := r.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "old\n", content)
	assert.False(t, truncated)
	assert.Positive(t, maxBytes)

	require.NoError(t, r.SaveFile(path, "-old\n+new\n", "new\n"))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(got))
}

func TestResolver_RecordAbort_NilResSafe(t *testing.T) {
	r := New(&agent.Session{})
	r.RecordAbort(nil) // must not panic
}

func TestResolver_TrackerAndSnapshot(t *testing.T) {
	sess := &agent.Session{Stats: usage.New()}
	g := sess.Stats.StartGoal("g")
	s := g.AddStep("ls")
	s.SetPropose(usage.Usage{PromptTokens: 5})
	g.Finish("done", "", false)

	r := New(sess)
	goals := r.Tracker()
	require.Len(t, goals, 1)
	assert.Equal(t, "g", goals[0].Text)
	require.Len(t, goals[0].Steps, 1)
	assert.Equal(t, "ls", goals[0].Steps[0].Command)

	snap := r.UsageSnapshot()
	assert.Equal(t, 1, snap.Goals)
}
