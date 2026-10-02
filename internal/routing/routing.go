// Package routing picks the Runner that executes a Call.
package routing

import (
	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/sandbox"
)

// Selector picks host or sandbox: a global toggle, with Risk
// threaded through so a real rule can replace the body later.
type Selector struct {
	Host     engine.Runner
	Sandbox  engine.Runner
	HostOnly bool
}

// Select returns the sandbox unless HostOnly is set or there is none.
func (s Selector) Select(event.Risk) (engine.Runner, string) {
	if s.HostOnly || s.Sandbox == nil {
		return s.Host, "host"
	}
	return s.Sandbox, "sandbox"
}

// Asserted because the engine finds Snapshotter by type assertion, so
// a rename would silently remove rollback rather than fail the build.
var (
	_ engine.RunnerSelector = Selector{}
	_ engine.Runner         = (*sandbox.Container)(nil)
	_ engine.Snapshotter    = (*sandbox.Container)(nil)
)
