// Package routing decides which agent.Runner handles a proposed
// command. Deliberately dumb for v1 (a global host/sandbox toggle);
// PreJudgment is already threaded through Select so a Jev-informed
// rule can replace its body later without changing any caller.
package routing

import (
	"context"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/sandbox"
)

// Selector picks host vs. sandbox per command.
type Selector struct {
	Host          agent.Runner
	SandboxRunner agent.Runner
	HostOnly      bool
}

func (s Selector) Select(agent.PreJudgment) (agent.Runner, string) {
	if s.HostOnly || s.SandboxRunner == nil {
		return s.Host, agent.RunModeHost
	}
	return s.SandboxRunner, agent.RunModeSandbox
}

func (s Selector) Probe() agent.Runner   { return s.Host }
func (s Selector) Sandbox() agent.Runner { return s.SandboxRunner }

// WrapSandbox adapts c's plain-string Snapshot/Rollback to
// agent.Snapshotter's SnapshotID, so sandbox itself never imports agent.
func WrapSandbox(c *sandbox.Container) agent.Runner {
	return sandboxRunner{c}
}

type sandboxRunner struct{ *sandbox.Container }

// agent finds Snapshotter by type assertion, so a rename here would
// not fail the build: rollback would simply report that no snapshotter
// is wired and the feature would vanish.
var (
	_ agent.Runner      = sandboxRunner{}
	_ agent.Snapshotter = sandboxRunner{}
)

func (r sandboxRunner) Snapshot(ctx context.Context) (agent.SnapshotID, error) {
	id, err := r.Container.Snapshot(ctx)
	return agent.SnapshotID(id), err
}

func (r sandboxRunner) Rollback(ctx context.Context, id agent.SnapshotID) error {
	return r.Container.Rollback(ctx, string(id))
}
