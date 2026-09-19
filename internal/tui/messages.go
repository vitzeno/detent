package tui

import "github.com/vitzeno/detent/internal/loop"

// preparedMsg is what a prepareCmd produces: exactly one of Prepared,
// Termination, or Err is non-nil (mirrors Run.Prepare's own three-way
// return — the message boundary doesn't collapse that distinction).
type preparedMsg struct {
	prepared    *loop.Prepared
	termination *loop.Termination
	err         error
}

// committedMsg is what a commitCmd produces after Run.Commit executes,
// reduces, and appends a step to state.
type committedMsg struct {
	finding loop.Finding
	err     error
}

// resolvedMsg is what a resolveCmd produces after Run.ResolveAmbiguous
// applies a human's pick and gates it.
type resolvedMsg struct {
	prepared *loop.Prepared
	err      error
}
