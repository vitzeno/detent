// Package routing picks the Runner that executes a tool call.
package routing

import (
	"context"
	"errors"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/engine"
	"github.com/vitzeno/detent/internal/sandbox"
)

// ErrNoSandbox is what a command gets when the sandbox was asked for and
// never wired: failing loudly beats running it on the host instead.
var ErrNoSandbox = errors.New("routing: no sandbox is configured")

// Selector runs every tool call in one place, this machine or the sandbox.
// The zero Selector is a sandbox nobody wired, which refuses every call.
type Selector struct {
	host    engine.Runner
	sandbox engine.Runner
}

// Host runs every tool call on this machine through r.
func Host(r engine.Runner) Selector { return Selector{host: r} }

// Sandbox runs every tool call in r, the session's container.
func Sandbox(r engine.Runner) Selector { return Selector{sandbox: r} }

// Select returns the one runner, and says which place it is.
func (s Selector) Select(_ event.Risk) (engine.Runner, string) {
	switch {
	case s.host != nil:
		return s.host, "host"
	case s.sandbox == nil:
		return missing{}, "sandbox"
	}
	return s.sandbox, "sandbox"
}

// missing is the sandbox nobody wired.
type missing struct{}

func (missing) Run(context.Context, string, chan<- capture.StreamEvent) (capture.Result, error) {
	return capture.Result{}, ErrNoSandbox
}

// Asserted because the engine finds Snapshotter by type assertion, so
// a rename would silently remove rollback rather than fail the build.
var _ engine.Snapshotter = (*sandbox.Container)(nil)
