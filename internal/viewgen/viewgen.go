// Package viewgen produces a viewspec.Spec for a command's output:
// the model authors it, Jev verifies it, and the result is saved
// under the command's shape so the second run costs nothing.
package viewgen

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/logging"
	"github.com/vitzeno/detent/viewspec"
)

// Generator composes specs. The Judge is what writes one, so without
// it there is no composition; a nil Store disables caching.
type Generator struct {
	Judge    Judge
	Registry *viewspec.Registry
	Store    *Store
}

// worthAsking reports whether this output earns a model call. Shape
// decides most of it; length decides the rest, because a handful of
// lines has no view worth paying for whatever shape it is.
func (g *Generator) worthAsking(req Request) bool {
	if !worthGenerating(req.Kind) {
		return false
	}
	return lines(req.Output) >= MinLinesToCompose
}

// skipReason says which gate refused, since "no view appeared" is
// otherwise the same observation whatever the cause.
func (g *Generator) skipReason(req Request) string {
	if !worthGenerating(req.Kind) {
		return "this shape draws itself"
	}
	return fmt.Sprintf("under %d lines", MinLinesToCompose)
}

func lines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

// Existing returns a spec that is already written, without calling
// anything: the store first, then what detent ships. Store first so a
// spec the human has edited beats the one we shipped, which is the
// whole reason a saved spec is a file rather than a row.
//
// This is the entirety of views: saved.
func (g *Generator) Existing(ctx context.Context, req Request) (Result, bool) {
	key := Key(req.Command, req.Kind)
	if spec, ok := g.Store.Load(key); ok && g.usable(ctx, req, spec, SourceSaved) {
		return Result{Spec: spec, Key: key, Source: SourceSaved}, true
	}
	if spec, ok := seed(req.Command); ok && g.usable(ctx, req, spec, SourceShipped) {
		return Result{Spec: spec, Key: key, Source: SourceShipped}, true
	}
	return Result{}, false
}

// usable reports whether an existing spec can draw this output, and
// says which either way. A key match is not enough: "ps" and "ps aux"
// share one and print different columns, so a spec that bound nowhere
// still blocked composition behind it, silently.
func (g *Generator) usable(ctx context.Context, req Request, spec *viewspec.Spec, source Source) bool {
	log := logging.For(logging.Viewgen)
	key := Key(req.Command, req.Kind)
	err := draws(spec, g.registry(), req.Output)
	if err != nil {
		log.InfoContext(ctx, "an existing view cannot draw this output",
			logging.KeyEvent, logging.ViewInvalid, "key", key, "source", source,
			logging.KeyReason, err.Error())
		return false
	}
	log.InfoContext(ctx, "drawing an existing view", logging.KeyEvent, logging.ViewLookup,
		"key", key, "source", source)
	return true
}

// draws is validate without the decoding, for a spec already in hand.
func draws(spec *viewspec.Spec, reg *viewspec.Registry, output string) error {
	compiled, err := viewspec.Compile(*spec, viewspec.WithRegistry(reg))
	if err != nil {
		return err
	}
	_, err = compiled.Bind(output)
	return err
}

// Request is one command's outcome, as the generator sees it.
type Request struct {
	Command  string
	Output   string
	ExitCode int
	// Kind is Jev's render_kind for this output; it prunes the
	// vocabulary before the model chooses from it.
	Kind string
}

// Result is a spec and where it came from. Source matters to the
// human: shipped is detent's own, saved is on disk and editable, and
// generated is framing a model wrote just now.
type Result struct {
	Spec   *viewspec.Spec
	Key    string
	Source Source
	Fit    float64
	Usage  event.Usage
}

// Source says who authored the spec being drawn.
type Source string

const (
	SourceShipped   Source = "shipped"
	SourceSaved     Source = "saved"
	SourceGenerated Source = "generated"
)

// MaxJudgeBytes caps the output shown to the judge. A choice about
// shape needs a sample, not the whole thing.
const MaxJudgeBytes = 4 * 1024

// head returns at most n bytes of s, marked where it was cut.
func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…[truncated]"
}

// MinLinesToCompose is the output below which no view is worth
// asking about. Length lived inside render_kind once; it belongs here,
// being a property of the output rather than of its shape.
const MinLinesToCompose = 8

// ErrNotWorth means this output has no view worth a model call: a few
// lines, or a shape already drawn well. Not a failure.
var ErrNotWorth = errors.New("viewgen: nothing to gain from a view here")

// ErrNoneFit means nothing was composed: the judge declined, or did
// not answer, or what it chose could not draw this output.
var ErrNoneFit = errors.New("viewgen: nothing composed fit the output")

func (g *Generator) registry() *viewspec.Registry {
	if g.Registry != nil {
		return g.Registry
	}
	return viewspec.Standard()
}

func add(a, b event.Usage) event.Usage {
	return event.Usage{
		PromptTokens:     a.PromptTokens + b.PromptTokens,
		CompletionTokens: a.CompletionTokens + b.CompletionTokens,
		Latency:          a.Latency + b.Latency,
		Model:            b.Model,
	}
}
