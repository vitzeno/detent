// Package routing picks the Runner that executes a Call.
package routing

import (
	"context"
	"errors"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
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

// ErrNoSandbox is what a command gets when the sandbox was asked for and
// never wired: failing loudly beats running it on the host instead.
var ErrNoSandbox = errors.New("routing: no sandbox is configured")

// Select returns the sandbox unless HostOnly is set.
func (s Selector) Select(_ event.Risk) (engine.Runner, string) {
	switch {
	case s.HostOnly:
		return s.Host, "host"
	case s.Sandbox == nil:
		return missing{}, "sandbox"
	}
	return s.Sandbox, "sandbox"
}

// missing is the sandbox nobody wired.
type missing struct{}

func (missing) Run(context.Context, string, chan<- capture.StreamEvent) (capture.Result, error) {
	return capture.Result{}, ErrNoSandbox
}

// Asserted because the engine finds Snapshotter by type assertion, so
// a rename would silently remove rollback rather than fail the build.
var _ engine.Snapshotter = (*sandbox.Container)(nil)
