package ui

import (
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/logging"
	"github.com/vitzeno/detent/ui/markdown"
	"github.com/vitzeno/detent/ui/theme"
	"github.com/vitzeno/detent/views"
	"github.com/vitzeno/detent/viewspec"
)

// errHides is a view that would leave most of the output undrawn.
var errHides = errors.New("the view hides most of the output")

// maxMarkdownRenders bounds the cache. Past it, it starts over.
const maxMarkdownRenders = 16

// viewRegistry is Standard plus what only detent can provide. A widget
// registered here reaches the model's schema too.
var viewRegistry = func() *viewspec.Registry {
	r := viewspec.Standard()
	must(r.Widget("markdown", &markdownWidget{}))
	return r
}()

// The specs from views, compiled once against this registry, since
// Compile is per spec and Bind is per output.
var (
	compiledFallback = compileAll(byKind())
	compiledMarkdown = compileAll(map[string]viewspec.Spec{"m": views.Raw("markdown")})["m"]
	// compiledPlain is the floor: raw bytes, no interpretation.
	compiledPlain = compileAll(map[string]viewspec.Spec{"p": views.Raw("log")})["p"]
)

// Registry is the vocabulary detent draws with. cmd/detent hands it to
// the composer, so the judge is offered exactly what will draw.
func Registry() *viewspec.Registry { return viewRegistry }

// seedFromView puts a row's on_enter command in the prompt as
// editable text. It does not run: from there it is an ordinary prompt.
func (m Model) seedFromView(r *historyRow) (Model, bool) {
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
// row. Draw runs per frame, Bind must not.
func boundView(r *historyRow) (*viewspec.Bound, bool) {
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
			// The chain ends at raw bytes, so this is recoverable, but it
			// is the only trace that a kind's own rendering was dropped.
			logging.For(logging.UI).Debug("a built-in view could not draw this output",
				logging.KeyEvent, logging.ViewInvalid, "kind", r.kind(),
				"source", "built-in", logging.KeyReason, err.Error())
			continue
		}
		r.view, r.viewSource = b, "built-in"
		return b, true
	}
	return nil, false
}

// bindSpec binds a spec the engine sent, and binds again rather than
// trusting it: a broken spec must leave the fallback exactly as it was.
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

// fallbackChain is what a row draws with no judge involved: the spec for
// its judged shape, then raw bytes. A composed spec replaces it later.
func fallbackChain(r *historyRow, output string) []*viewspec.Compiled {
	// The model's own words are a document, not bytes a command printed.
	if r.prose != "" {
		return []*viewspec.Compiled{compiledMarkdown, compiledPlain}
	}
	var chain []*viewspec.Compiled
	// A tool that declared its shape is not guessing, so it goes first.
	switch r.renders {
	case event.RendersMarkdown:
		return []*viewspec.Compiled{compiledMarkdown, compiledPlain}
	case event.RendersDiff:
		return []*viewspec.Compiled{compiledFallback[event.RendersDiff], compiledPlain}
	default:
		// Any other declared shape defers to the judged kind.
	}
	kind := r.kind()
	if kind == event.RendersContent && markdown.Wants(r.command, output) {
		chain = append(chain, compiledMarkdown)
	}
	if c, ok := compiledFallback[kind]; ok {
		chain = append(chain, c)
	}
	return append(chain, compiledPlain)
}

// markdownWidget draws prose through glamour, which viewspec cannot import.
// Draw runs per frame and one instance serves every row, so it caches several renders.
type markdownWidget struct {
	mu    sync.Mutex
	cache map[markdownKey][]string
}

var _ viewspec.Described = (*markdownWidget)(nil)

// markdownKey is everything a render depends on, the theme included.
type markdownKey struct {
	raw, style string
	width      int
}

func (w *markdownWidget) Draw(_ viewspec.Block, d viewspec.Data, f viewspec.Frame) ([]string, error) {
	key := markdownKey{raw: d.Raw, style: theme.Current().Markdown, width: f.Width}
	w.mu.Lock()
	defer w.mu.Unlock()
	if out, ok := w.cache[key]; ok {
		return slices.Clone(out), nil
	}
	rendered, err := markdown.Render(d.Raw, key.style, f.Width)
	if err != nil {
		// Wrapped, not raw, so falling back does not overflow the pane.
		rendered = strings.Join(wrapPlain(d.Raw, f.Width), "\n")
	}
	if w.cache == nil || len(w.cache) >= maxMarkdownRenders {
		w.cache = map[markdownKey][]string{}
	}
	out := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
	w.cache[key] = out
	return slices.Clone(out), nil
}

// Describe puts markdown in the guide the judge chooses from. Without it
// the widget draws but is absent from every criteria list.
func (*markdownWidget) Describe() viewspec.Description {
	return viewspec.Description{
		Raw:      true,
		What:     "prose rendered as a document: headings, lists, emphasis, code blocks",
		NotFor:   "source code or a config file, which code draws with its lines intact",
		Examples: []string{"cat README.md", "a changelog", "generated documentation"},
	}
}

// byKind is the shipped spec per judged output shape.
func byKind() map[event.RenderKind]viewspec.Spec {
	out := map[event.RenderKind]viewspec.Spec{}
	for _, kind := range views.Kinds() {
		if spec, ok := views.ForKind(kind); ok {
			out[kind] = spec
		}
	}
	return out
}

// compileAll drops what doesn't compile, so one bad spec cannot take
// the whole map with it.
func compileAll[K comparable](in map[K]viewspec.Spec) map[K]*viewspec.Compiled {
	out := make(map[K]*viewspec.Compiled, len(in))
	for key, spec := range in {
		if c, err := viewspec.Compile(spec, viewspec.WithRegistry(viewRegistry)); err == nil {
			out[key] = c
		}
	}
	return out
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
