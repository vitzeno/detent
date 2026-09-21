// Package resolver translates between ui's own vocabulary (DTOs and the
// Driver interface) and the core harness's domain (agent, usage,
// propose, host, fileio). It's the only package that imports both
// sides — neither ui nor agent knows the other, or this package, exists.
package resolver

import (
	"context"
	"errors"

	"github.com/vitzeno/detent/internal/agent"
	"github.com/vitzeno/detent/internal/viewgen"
	"github.com/vitzeno/detent/ui"
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
// presentation, so this crosses here rather than in agent: the loop
// has no opinion about how its output is drawn.
//
// A nil Views generator, a kind with nothing to gain, or a model that
// fails all report ok=false, and ui keeps the render_kind fallback.
func (r *Resolver) GenerateView(ctx context.Context, command, output string, exitCode int, kind ui.RenderKind) (ui.GeneratedView, bool) {
	if r.Views == nil {
		return ui.GeneratedView{}, false
	}
	req := viewgen.Request{Command: command, Output: output, ExitCode: exitCode, Kind: string(kind)}
	got, ok := r.Views.Existing(req)
	if !ok {
		var err error
		if got, err = r.Views.Generate(ctx, req); err != nil {
			// Asked and refused is worth saying; never asked is not.
			if errors.Is(err, viewgen.ErrNoneFit) {
				return ui.GeneratedView{Source: ui.ViewDeclined}, false
			}
			return ui.GeneratedView{}, false
		}
	}
	return ui.GeneratedView{Spec: got.Spec, Source: ui.ViewSource(got.Source)}, true
}
