package routing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/host"
	"github.com/vitzeno/detent/internal/sandbox"
)

type fakeRunner struct{ id string }

func (f fakeRunner) Run(context.Context, string, chan<- host.StreamEvent) (host.Result, error) {
	return host.Result{}, nil
}

func TestSelector_HostOnly(t *testing.T) {
	h, s := fakeRunner{id: "host"}, fakeRunner{id: "sandbox"}
	sel := Selector{Host: h, SandboxRunner: s, HostOnly: true}
	runner, mode := sel.Select(agent.PreJudgment{})
	assert.Equal(t, agent.RunModeHost, mode)
	assert.Equal(t, h, runner)
}

func TestSelector_NoSandboxWiredFallsBackToHost(t *testing.T) {
	h := fakeRunner{id: "host"}
	sel := Selector{Host: h}
	runner, mode := sel.Select(agent.PreJudgment{})
	assert.Equal(t, agent.RunModeHost, mode)
	assert.Equal(t, h, runner)
}

func TestSelector_DefaultsToSandbox(t *testing.T) {
	h, s := fakeRunner{id: "host"}, fakeRunner{id: "sandbox"}
	sel := Selector{Host: h, SandboxRunner: s}
	runner, mode := sel.Select(agent.PreJudgment{Dangerous: true})
	assert.Equal(t, agent.RunModeSandbox, mode)
	assert.Equal(t, s, runner)
}

func TestSelector_ProbeAlwaysHost(t *testing.T) {
	h, s := fakeRunner{id: "host"}, fakeRunner{id: "sandbox"}
	sel := Selector{Host: h, SandboxRunner: s}
	assert.Equal(t, h, sel.Probe())
}

func TestSelector_Sandbox(t *testing.T) {
	s := fakeRunner{id: "sandbox"}
	sel := Selector{SandboxRunner: s}
	assert.Equal(t, s, sel.Sandbox())
}

// WrapSandbox's conversion is exercised against an unstarted
// Container (fails fast, no daemon needed); sandbox's own tests
// cover the real thing.

func TestWrapSandbox_SatisfiesSnapshotter(t *testing.T) {
	runner := WrapSandbox(sandbox.NewContainer())
	_, ok := runner.(agent.Snapshotter)
	require.True(t, ok)
}

func TestWrapSandbox_SnapshotForwardsToContainer(t *testing.T) {
	snap := WrapSandbox(sandbox.NewContainer()).(agent.Snapshotter)
	_, err := snap.Snapshot(context.Background())
	assert.ErrorContains(t, err, "Start not called")
}

func TestWrapSandbox_RollbackForwardsToContainer(t *testing.T) {
	snap := WrapSandbox(sandbox.NewContainer()).(agent.Snapshotter)
	err := snap.Rollback(context.Background(), "some-id")
	assert.ErrorContains(t, err, "Start not called")
}
