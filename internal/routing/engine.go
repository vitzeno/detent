package routing

import (
	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/sandbox"
)

// EngineSelector picks host or sandbox: a global toggle, with Risk
// threaded through so a real rule can replace the body later.
type EngineSelector struct {
	Host     engine.Runner
	Sandbox  engine.Runner
	HostOnly bool
}

func (s EngineSelector) Select(event.Risk) (engine.Runner, string) {
	if s.HostOnly || s.Sandbox == nil {
		return s.Host, "host"
	}
	return s.Sandbox, "sandbox"
}

// No adapter needed: sandbox already speaks plain-string checkpoints.
// Asserted because the engine finds Snapshotter by type assertion, so
// a rename would silently remove rollback rather than fail the build.
var (
	_ engine.RunnerSelector = EngineSelector{}
	_ engine.Runner         = (*sandbox.Container)(nil)
	_ engine.Snapshotter    = (*sandbox.Container)(nil)
)
