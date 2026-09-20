// Package resolver translates between ui's own vocabulary (DTOs and the
// Driver interface) and the core harness's domain (agent, usage,
// propose, shell, fileio). It's the only package that imports both
// sides — neither ui nor agent knows the other, or this package, exists.
package resolver

import (
	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/ui"
)

// Resolver implements ui.Driver over a *agent.Session. Holds no state
// of its own beyond that reference.
type Resolver struct {
	sess *agent.Session
}

var _ ui.Driver = (*Resolver)(nil)

// New wraps sess for a ui.Driver caller.
func New(sess *agent.Session) *Resolver {
	return &Resolver{sess: sess}
}
