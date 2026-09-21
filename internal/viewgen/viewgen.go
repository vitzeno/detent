// Package viewgen produces a viewspec.Spec for a command's output:
// the model authors it, Jev verifies it, and the result is saved
// under the command's shape so the second run costs nothing.
package viewgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/internal/usage"
	"github.com/vitzeno/detent/viewspec"
)

// Generator authors specs. A nil Judge skips verification and keeps
// the first candidate that validates; a nil Store disables caching.
type Generator struct {
	Model    Structurer
	Judge    Judge
	Registry *viewspec.Registry
	Store    *Store

	Candidates   int
	FitThreshold float64
}

// Generate authors a spec for req, verifies it against the output that
// actually came out, and caches it.
//
// Order matters: disk, then the model, then what detent ships. A
// shipped spec is the floor and not the ceiling, so a seeded command
// still gets a model's attempt. Checking seeds first made those
// commands permanently ungeneratable, and the only way to override one
// was to hand-write a file at a hashed key.
func (g *Generator) Generate(ctx context.Context, req Request) (Result, error) {
	if g.Model == nil {
		return Result{}, errors.New("viewgen: no Model wired")
	}
	key := Key(req.Command, req.Kind)
	if spec, ok := g.Store.Load(key); ok {
		return Result{Spec: spec, Key: key, Source: SourceSaved}, nil
	}

	total, best, bestFit := usage.Usage{}, (*viewspec.Spec)(nil), -1.0
	if g.worthAsking(req) {
		reg := prune(g.registry(), req.Kind)
		schema := reg.Schema()
		user := userPrompt(req)
		for range g.candidates() {
			raw, used, err := g.Model.Structured(ctx, systemPrompt, user, schema)
			total = add(total, used)
			if err != nil {
				continue
			}
			spec, bound, err := validate(raw, reg, req.Output)
			if err != nil {
				continue
			}
			fit := 1.0
			if g.Judge != nil {
				fit, used = g.fit(ctx, req, spec, bound)
				total = add(total, used)
			}
			if fit > bestFit {
				best, bestFit = spec, fit
			}
		}
	}
	if best != nil && bestFit >= g.threshold() {
		best.Version = viewspec.Version
		best.Match = Normalise(req.Command)
		if err := g.Store.Save(key, best); err != nil {
			return Result{Usage: total}, err
		}
		return Result{Spec: best, Key: key, Source: SourceGenerated, Fit: bestFit, Usage: total}, nil
	}
	if spec, ok := seed(req.Command); ok {
		return Result{Spec: spec, Key: key, Source: SourceShipped, Usage: total}, nil
	}
	if !g.worthAsking(req) {
		return Result{}, ErrNotWorth
	}
	return Result{Usage: total}, ErrNoneFit
}

// worthAsking reports whether this output earns a model call. Shape
// decides most of it; length decides the rest, because a handful of
// lines has no view worth paying for whatever shape it is.
func (g *Generator) worthAsking(req Request) bool {
	if !worthGenerating(req.Kind) {
		return false
	}
	return strings.Count(strings.TrimSuffix(req.Output, "\n"), "\n")+1 >= MinLinesToGenerate
}

// Existing returns a spec that is already written, without calling
// anything: the store first, then what detent ships. Store first so a
// spec the human has edited beats the one we shipped, which is the
// whole reason a saved spec is a file rather than a row.
//
// This is the entirety of views: saved.
func (g *Generator) Existing(req Request) (Result, bool) {
	key := Key(req.Command, req.Kind)
	if spec, ok := g.Store.Load(key); ok {
		return Result{Spec: spec, Key: key, Source: SourceSaved}, true
	}
	if spec, ok := seed(req.Command); ok {
		return Result{Spec: spec, Key: key, Source: SourceShipped}, true
	}
	return Result{}, false
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
	Usage  usage.Usage
}

// Source says who authored the spec being drawn.
type Source string

const (
	SourceShipped   Source = "shipped"
	SourceSaved     Source = "saved"
	SourceGenerated Source = "generated"
)

// DefaultCandidates is how many specs are generated and ranked.
const DefaultCandidates = 2

// DefaultFitThreshold is the Jev score a spec must reach to be kept.
const DefaultFitThreshold = 0.5

// MinLinesToGenerate is the output below which no view is worth a
// model call. Length used to live inside render_kind as inline_short;
// it belongs here, because it is a property of the output rather than
// of its shape.
const MinLinesToGenerate = 8

// ErrNotWorth means this output has no view worth a model call: a few
// lines, or a shape already drawn well. Not a failure.
var ErrNotWorth = errors.New("viewgen: nothing to gain from a view here")

// ErrNoneFit means nothing generated survived validation or judging.
var ErrNoneFit = errors.New("viewgen: no candidate fit the output")

// validate is the whole defence against a spec that reads well and
// draws nothing: it must decode, compile against the pruned
// vocabulary, and bind against the output that actually came out.
func validate(raw []byte, reg *viewspec.Registry, output string) (*viewspec.Spec, *viewspec.Bound, error) {
	var spec viewspec.Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, nil, fmt.Errorf("viewgen: decoding spec: %w", err)
	}
	compiled, err := viewspec.Compile(spec, viewspec.WithRegistry(reg))
	if err != nil {
		return nil, nil, err
	}
	bound, err := compiled.Bind(output)
	if err != nil {
		return nil, nil, err
	}
	return &spec, bound, nil
}

// fit asks Jev whether the view reads well, over the fields the parse
// actually produced. A closed question about real data is what Jev is
// for; authoring a spec is not.
func (g *Generator) fit(ctx context.Context, req Request, spec *viewspec.Spec, bound *viewspec.Bound) (float64, usage.Usage) {
	kinds := make([]string, 0, len(spec.Blocks))
	for _, b := range spec.Blocks {
		kinds = append(kinds, b.Kind)
	}
	answers, u, ok := classify.AskOrFallback(ctx, g.Judge, classify.State(map[string]any{
		"command":       req.Command,
		"output":        req.Output,
		"parse_kind":    spec.Parse.Kind,
		"fields_found":  bound.Fields(),
		"blocks_chosen": kinds,
	}), classify.Questions{
		"view_fit": {
			Instructions: "This view was generated to draw the command's output. " +
				"Given the fields the parse actually found and the blocks chosen, " +
				"would a human read this output better this way than as plain text?",
			Noul: &classify.NoulQuestion{},
		},
	})
	if !ok {
		return 1, usage.Usage{}
	}
	return answers["view_fit"].Noul, usage.Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		Model:            u.Model,
	}
}

func (g *Generator) registry() *viewspec.Registry {
	if g.Registry != nil {
		return g.Registry
	}
	return viewspec.Standard()
}

func (g *Generator) candidates() int {
	if g.Candidates > 0 {
		return g.Candidates
	}
	return DefaultCandidates
}

func (g *Generator) threshold() float64 {
	if g.FitThreshold > 0 {
		return g.FitThreshold
	}
	return DefaultFitThreshold
}

func add(a, b usage.Usage) usage.Usage {
	return usage.Usage{
		PromptTokens:     a.PromptTokens + b.PromptTokens,
		CompletionTokens: a.CompletionTokens + b.CompletionTokens,
		Latency:          a.Latency + b.Latency,
		Model:            b.Model,
	}
}
