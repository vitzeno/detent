package routing

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/propose"
	"github.com/vitzeno/detent/internal/sandbox"
	"github.com/vitzeno/detent/internal/usage"
)

// End-to-end cover for what /rollback N means, through a real
// container: agent's Session driving a real sandbox.Container via the
// Selector, which is the only place all three meet. Skipped without a
// reachable daemon, like sandbox's own tests.

var testSocket = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".colima", "default", "containerd.sock")
}()

type stubProposer struct{}

func (stubProposer) Propose(context.Context, []propose.Message) (propose.Proposal, usage.Usage, error) {
	return propose.Proposal{Done: true}, usage.Usage{}, nil
}

// quietRunner stands in for the host runner so BeginGoal's probes cost
// nothing and never touch the real machine.
type quietRunner struct{}

func (quietRunner) Run(context.Context, string, chan<- host.StreamEvent) (host.Result, error) {
	return host.Result{}, nil
}

func TestRollback_UndoesTheNamedStepOnward(t *testing.T) {
	if testSocket == "" {
		t.Skip("no home dir")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := sandbox.Preflight(ctx, testSocket); err != nil {
		t.Skip("containerd not reachable:", err)
	}

	c := sandbox.NewContainer(sandbox.WithSocket(testSocket), sandbox.WithNamespace("detent-test"))
	require.NoError(t, c.Start(context.Background(), strings.ToLower(t.Name())))
	t.Cleanup(func() { _ = c.Close(context.Background()) })

	sess := agent.New(stubProposer{}, nil,
		agent.WithRunners(Selector{Host: quietRunner{}, SandboxRunner: WrapSandbox(c)}),
		agent.WithStats(usage.New()))

	bg := context.Background()
	res, err := sess.BeginGoal(bg, "write three lines")
	require.NoError(t, err)
	require.NotEmpty(t, res.Baseline, "BeginGoal must checkpoint before step 1 so step 1 is undoable")

	pre := agent.PreJudgment{RunMode: agent.RunModeSandbox}
	for _, word := range []string{"one", "two", "three"} {
		p := propose.Proposal{Command: "echo " + word + " >> /log.txt"}
		ustep := sess.RecordStep(res, p, pre, usage.Usage{}, 0)
		_, err := sess.Execute(bg, res, ustep, p, pre, nil)
		require.NoError(t, err)
	}

	out, err := c.Run(bg, "cat /log.txt", nil)
	require.NoError(t, err)
	require.Equal(t, "one\ntwo\nthree\n", out.Stdout)

	// Undo step 2 onward: step 1 survives, steps 2 and 3 are gone.
	ok, err := sess.Rollback(bg, res, 2, false)
	require.NoError(t, err)
	require.True(t, ok)
	out, err = c.Run(bg, "cat /log.txt", nil)
	require.NoError(t, err)
	assert.Equal(t, "one\n", out.Stdout, "/rollback 2 undoes step 2 and everything after it")
	assert.Len(t, res.Commands, 1, "history drops the undone steps")

	// Undo step 1 as well: back to the goal's baseline, empty file.
	ok, err = sess.Rollback(bg, res, 1, false)
	require.NoError(t, err)
	require.True(t, ok)
	out, err = c.Run(bg, "cat /log.txt 2>/dev/null; echo done", nil)
	require.NoError(t, err)
	assert.Equal(t, "done\n", out.Stdout, "/rollback 1 undoes the whole goal")
	assert.Empty(t, res.Commands)
}
