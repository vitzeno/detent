// Package resolver translates between ui's own vocabulary (DTOs and the
// Driver interface) and the core harness's domain (agent, usage,
// propose, host, fileio). It's the only package that imports both
// sides — neither ui nor agent knows the other, or this package, exists.
package resolver

import (
	"context"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/viewgen"
	"github.com/vitzeno/detent/ui"
	"github.com/vitzeno/detent/viewspec"
)

// Resolver implements ui.Driver over a *agent.Session. Holds no state
// of its own beyond that reference.
type Resolver struct {
	sess *agent.Session

	// Views generates output views; nil disables them entirely.
	Views *viewgen.Generator
}

var _ ui.Driver = (*Resolver)(nil)

// New wraps sess for a ui.Driver caller.
func New(sess *agent.Session) *Resolver {
	return &Resolver{sess: sess}
}

// GenerateView authors a view for one command's output. Views are
// presentation, so this crosses here rather than in agent — the loop
// has no opinion about how its output is drawn.
//
// A nil Views generator, a kind with nothing to gain, or a model that
// fails all report ok=false, and ui keeps the render_kind fallback.
func (r *Resolver) GenerateView(ctx context.Context, command, output string, exitCode int, kind ui.RenderKind) (*viewspec.Spec, bool) {
	if r.Views == nil {
		return nil, false
	}
	req := viewgen.Request{Command: command, Output: output, ExitCode: exitCode, Kind: string(kind)}
	if got, ok := r.Views.Cached(req); ok {
		return got.Spec, true
	}
	got, err := r.Views.Generate(ctx, req)
	if err != nil {
		return nil, false
	}
	return got.Spec, true
}
