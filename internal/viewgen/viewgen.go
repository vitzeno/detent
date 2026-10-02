// Package viewgen produces a viewspec.Spec for a command's output by
// asking the judge closed questions, and saves the result under the
// command's shape so the second run costs nothing.
package viewgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/logging"
	"github.com/vitzeno/detent/views"
	"github.com/vitzeno/detent/viewspec"
)

// Generator composes specs. Without a Judge there is no composition,
// and a nil Store disables caching.
type Generator struct {
	Judge    Judge
	Registry *viewspec.Registry
	Store    *Store
}

// Request is one command's outcome, as the generator sees it.
type Request struct {
	Command  string
	Output   string
	ExitCode int
	// Kind is Jev's render_kind for this output, which prunes the
	// vocabulary before the judge chooses from it.
	Kind string
}

// Result is a spec and where it came from: shipped is detent's own,
// saved is on disk and editable, generated was composed just now.
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

// MinLinesToCompose is the text below which no view is worth asking about.
const MinLinesToCompose = 8

// MinRecordsToCompose is the same for records, which earn a view far
// sooner: kubectl get nodes is seven lines, and minified JSON is one.
const MinRecordsToCompose = 3

// ErrNotWorth means this output has no view worth a judge call: a few
// lines, or a shape already drawn well. Not a failure.
var ErrNotWorth = errors.New("viewgen: nothing to gain from a view here")

// ErrNoneFit means nothing was composed: the judge declined, or did
// not answer, or what it chose could not draw this output.
var ErrNoneFit = errors.New("viewgen: nothing composed fit the output")

// Existing returns a spec already written, calling nothing: the store
// first, so a human's edit beats what detent ships. All of views: saved.
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

// Shape asks the judge only which shape output has, for output nothing
// else judged. "" with no judge, or too little to draw.
func (g *Generator) Shape(ctx context.Context, command, output string) string {
	if len(nonBlank(output)) < MinRecordsToCompose && !isJSON(output) {
		return ""
	}
	answers, _, ok := classify.AskOrFallback(ctx, g.Judge,
		classify.State(map[string]any{"command": command, "output": head(output, MaxJudgeBytes)}),
		classify.Questions{"render_kind": RenderKindQuestion()})
	if !ok {
		return ""
	}
	return answers["render_kind"].Choice
}

// forKind is the spec detent ships for an output shape, when it draws.
func (g *Generator) forKind(ctx context.Context, req Request) (Result, bool) {
	spec, ok := views.ForKind(req.Kind)
	if !ok || !g.usable(ctx, req, &spec, SourceShipped) {
		return Result{}, false
	}
	return Result{Spec: &spec, Key: Key(req.Command, req.Kind), Source: SourceShipped}, true
}

// usable reports whether an existing spec can draw this output, and logs
// which. A key match is not enough: "ps" and "ps aux" share one.
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

// worthAsking reports whether this output earns a judge call. Shape
// decides most of it, length the rest.
func (g *Generator) worthAsking(req Request) bool {
	if !worthGenerating(req.Kind) {
		return false
	}
	return enough(req.Kind, req.Output)
}

// skipReason says which gate refused, since "no view appeared" is
// otherwise the same observation whatever the cause.
func (g *Generator) skipReason(req Request) string {
	if !worthGenerating(req.Kind) {
		return "this shape draws itself"
	}
	if k, ok := byName[req.Kind]; ok && k.Records {
		return fmt.Sprintf("under %d records", MinRecordsToCompose)
	}
	return fmt.Sprintf("under %d lines", MinLinesToCompose)
}

func (g *Generator) registry() *viewspec.Registry {
	if g.Registry != nil {
		return g.Registry
	}
	return viewspec.Standard()
}

// draws is validate without the decoding, for a spec already in hand.
func draws(spec *viewspec.Spec, reg *viewspec.Registry, output string) error {
	compiled, err := viewspec.Compile(*spec, viewspec.WithRegistry(reg))
	if err != nil {
		return err
	}
	bound, err := compiled.Bind(output)
	if err != nil {
		return err
	}
	if bound.Hides() {
		return errHides
	}
	return nil
}

// enough reports whether output is long enough for its shape to earn a view.
func enough(kind, output string) bool {
	if k, ok := byName[kind]; ok && k.Records {
		return len(nonBlank(output)) >= MinRecordsToCompose || isJSON(output)
	}
	return lines(output) >= MinLinesToCompose
}

// isJSON reports whether output is one JSON document, however it is laid out.
func isJSON(output string) bool {
	s := strings.TrimSpace(output)
	return (strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{")) && json.Valid([]byte(s))
}

func lines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

// head returns at most n bytes of s, marked where it was cut.
func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…[truncated]"
}

func add(a, b event.Usage) event.Usage {
	return event.Usage{
		PromptTokens:     a.PromptTokens + b.PromptTokens,
		CompletionTokens: a.CompletionTokens + b.CompletionTokens,
		Latency:          a.Latency + b.Latency,
		Model:            b.Model,
	}
}
