package ui

import (
	"errors"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/logging"
	"github.com/vitzeno/detent/ui/markdown"
	"github.com/vitzeno/detent/views"
	"github.com/vitzeno/detent/viewspec"
)

// Registry is the vocabulary detent draws with: viewspec's own plus
// what only this process can provide. cmd/detent hands it to the
// composer, so the judge is offered exactly what will draw.
func Registry() *viewspec.Registry { return viewRegistry }

// seedFromView puts a row's on_enter command in the prompt as
// editable text. It does not run: from there it is an ordinary prompt.
func (m Model) seedFromView(r *callRow) (Model, bool) {
	b, ok := boundView(r)
	if !ok {
		return m, false
	}
	command, ok := b.Action(viewspec.Frame{Cursor: r.tableCursor})
	if !ok {
		return m, false
	}
	m.prompt.SetValue(command)
	m.backToInput()
	return m, true
}

// boundView resolves a row's view, binding once and caching on the
// row. Draw runs per frame; Bind must not.
func boundView(r *callRow) (*viewspec.Bound, bool) {
	if !r.drawable() {
		return nil, false
	}
	if r.viewTried {
		return r.view, r.view != nil
	}
	r.viewTried = true
	output := r.text()
	for _, c := range fallbackChain(r, output) {
		b, err := c.Bind(output)
		if err == nil && b.Hides() {
			err = errHides
		}
		if err != nil {
			// The chain ends at raw bytes, so this is recoverable.
			// It is still the only trace that a kind's own rendering
			// was dropped, which used to leave no trace at all.
			logging.For(logging.UI).Debug("a built-in view could not draw this output",
				logging.KeyEvent, logging.ViewInvalid, "kind", string(r.kind()),
				"source", string("built-in"), logging.KeyReason, err.Error())
			continue
		}
		r.view, r.viewSource = b, "built-in"
		return b, true
	}
	return nil, false
}

// bindSpec binds a spec the engine sent. It binds again rather than
// trusting: ui accepts data it does not control, and a spec arriving
// broken must leave the fallback exactly as it was.
func bindSpec(spec viewspec.Spec, output string) (*viewspec.Bound, bool) {
	c, err := viewspec.Compile(spec, viewspec.WithRegistry(viewRegistry))
	if err == nil {
		var b *viewspec.Bound
		if b, err = c.Bind(output); err == nil && b.Hides() {
			err = errHides
		}
		if err == nil {
			return b, true
		}
	}
	logging.For(logging.UI).Warn("a view could not draw this output",
		logging.KeyEvent, logging.ViewInvalid, logging.KeyReason, err.Error())
	return nil, false
}

// fallbackChain is what a row draws with no judge involved: the spec
// for its judged shape, then raw bytes. A composed spec replaces it
// when one arrives. markdown.Wants is the one heuristic, and it runs
// only before anything has been judged.
func fallbackChain(r *callRow, output string) []*viewspec.Compiled {
	// The model's own words are a document, not bytes a command
	// printed: they wrap and scroll rather than losing their tail.
	if r.prose != "" {
		return []*viewspec.Compiled{compiledMarkdown, compiledPlain}
	}
	var chain []*viewspec.Compiled
	// A tool that declared its shape is not guessing, so it goes first.
	if r.renders == event.RendersMarkdown {
		return []*viewspec.Compiled{compiledMarkdown, compiledPlain}
	}
	kind := r.kind()
	if kind == "file_content" && markdown.Wants(r.command, output) {
		chain = append(chain, compiledMarkdown)
	}
	if c, ok := compiledFallback[kind]; ok {
		chain = append(chain, c)
	}
	return append(chain, compiledPlain)
}

// errHides is a view that would leave most of the output undrawn.
var errHides = errors.New("the view hides most of the output")

// viewRegistry is Standard plus what only detent can provide. A widget
// registered here reaches the model's schema too, since generation
// asks the registry rather than a hand-written list.
var viewRegistry = func() *viewspec.Registry {
	r := viewspec.Standard()
	must(r.Widget("markdown", &markdownWidget{}))
	return r
}()

// markdownWidget renders prose through glamour, which viewspec cannot
// cannot: it imports only the standard library. Caches its last render
// because Draw runs per frame; one entry, one goroutine, no lock.
type markdownWidget struct {
	raw   string
	width int
	out   []string
}

func (w *markdownWidget) Draw(_ viewspec.Block, d viewspec.Data, f viewspec.Frame) ([]string, error) {
	if w.out != nil && w.raw == d.Raw && w.width == f.Width {
		return w.out, nil
	}
	rendered, err := markdown.Render(d.Raw, f.Width)
	if err != nil {
		// Wrapped, not raw: falling back must not reintroduce the
		// overflow this widget is here to avoid.
		rendered = strings.Join(wrapPlain(d.Raw, f.Width), "\n")
	}
	w.raw, w.width = d.Raw, f.Width
	w.out = strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
	return w.out, nil
}

// Describe puts markdown in the guide the judge chooses from. Without
// it the widget is registered, drawable, and silently absent from
// every criteria list, since criteria are built from Described alone.
func (*markdownWidget) Describe() viewspec.Description {
	return viewspec.Description{
		What:     "prose rendered as a document: headings, lists, emphasis, code blocks",
		NotFor:   "source code or a config file, which code draws with its lines intact",
		Examples: []string{"cat README.md", "a changelog", "generated documentation"},
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// The specs themselves live in views, which viewgen reads too. These
// are them compiled once against this registry, since Compile is per
// spec and Bind is per output.
var (
	compiledFallback = compileAll(byKind())
	compiledMarkdown = compileAll(map[string]viewspec.Spec{"m": views.Raw("markdown")})["m"]
	// compiledPlain is the floor: raw bytes, no interpretation. Used
	// before anything is judged, and when a fitted spec doesn't fit.
	compiledPlain = compileAll(map[string]viewspec.Spec{"p": views.Raw("log")})["p"]
)

// byKind is the shipped spec per judged output shape.
func byKind() map[string]viewspec.Spec {
	out := map[string]viewspec.Spec{}
	for _, kind := range views.Kinds() {
		if spec, ok := views.ForKind(kind); ok {
			out[kind] = spec
		}
	}
	return out
}

// compileAll drops what doesn't compile. A bad spec here is a
// programming error, but one must not take the whole map with it.
func compileAll[K comparable](in map[K]viewspec.Spec) map[K]*viewspec.Compiled {
	out := make(map[K]*viewspec.Compiled, len(in))
	for key, spec := range in {
		if c, err := viewspec.Compile(spec, viewspec.WithRegistry(viewRegistry)); err == nil {
			out[key] = c
		}
	}
	return out
}
