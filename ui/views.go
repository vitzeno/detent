package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/logging"
	"github.com/vitzeno/detent/ui/markdown"
	"github.com/vitzeno/detent/views"
	"github.com/vitzeno/detent/viewspec"
)

// Registry is the vocabulary detent draws with: viewspec's own plus
// what only this process can provide. cmd/detent hands it to the
// composer, so the judge is offered exactly what will draw.
func Registry() *viewspec.Registry { return viewRegistry }

// generateView asks the Driver for a view, off the Update loop. Fired
// once per row, after judging, because render_kind is what prunes the
// vocabulary the judge chooses from.
func (m Model) generateView(r *stepRow) tea.Cmd {
	if r == nil || r.cmd.ec == nil || r.cmd.generated {
		return nil
	}
	r.cmd.generated = true
	sess, ctx := m.sess, m.ctx
	command, output, kind := r.command, commandOutput(r.cmd.ec), rowKind(r)
	exit := r.cmd.ec.Result.ExitCode
	return func() tea.Msg {
		got, ok := sess.GenerateView(ctx, command, output, exit, kind)
		if !ok && got.Source != ViewDeclined {
			return nil
		}
		return viewMsg{row: r, view: got}
	}
}

// seedFromView puts a row's on_enter command in the prompt as
// editable text. It does not run: from there it is an ordinary goal.
func (m Model) seedFromView(r *stepRow) (Model, bool) {
	b, ok := boundView(r)
	if !ok {
		return m, false
	}
	command, ok := b.Action(viewspec.Frame{Cursor: r.cmd.tableCursor})
	if !ok {
		return m, false
	}
	m.prompt.SetValue(command)
	return m.backToInput(), true
}

// boundView resolves a row's view, binding once and caching on the
// row. Draw runs per frame; Bind must not.
func boundView(r *stepRow) (*viewspec.Bound, bool) {
	if r == nil || r.cmd.running || r.cmd.ec == nil {
		return nil, false
	}
	if r.cmd.viewTried {
		return r.cmd.view, r.cmd.view != nil
	}
	r.cmd.viewTried = true
	output := commandOutput(r.cmd.ec)
	for _, c := range fallbackChain(r, output) {
		b, err := c.Bind(output)
		if err != nil {
			// The chain ends at raw bytes, so this is recoverable.
			// It is still the only trace that a kind's own rendering
			// was dropped, which used to leave no trace at all.
			logging.For(logging.UI).Debug("a built-in view could not draw this output",
				logging.KeyEvent, logging.ViewInvalid, "kind", string(rowKind(r)),
				"source", string(ViewBuiltin), logging.KeyReason, err.Error())
			continue
		}
		r.cmd.view, r.cmd.viewSource = b, ViewBuiltin
		return b, true
	}
	return nil, false
}

// applyView swaps in a composed spec. A Driver hands over a Spec and
// not a Bound, so this binds it again: ui accepts data from a Driver
// it does not control, and a spec that arrives broken must leave the
// fallback exactly as it was.
func applyView(r *stepRow, got GeneratedView) bool {
	if r == nil || got.Spec == nil || r.cmd.ec == nil {
		return false
	}
	c, err := viewspec.Compile(*got.Spec, viewspec.WithRegistry(viewRegistry))
	if err == nil {
		var b *viewspec.Bound
		if b, err = c.Bind(commandOutput(r.cmd.ec)); err == nil {
			r.cmd.view, r.cmd.viewTried = b, true
			r.cmd.viewSource = got.Source
			return true
		}
	}
	// cmd/detent hands the composer this very registry, and it binds
	// every spec before handing one over, so this should be
	// unreachable. It is warned rather than dropped because reaching
	// it means something is genuinely wrong, not merely unlucky.
	logging.For(logging.UI).Warn("a fitted view could not draw this output",
		logging.KeyEvent, logging.ViewInvalid, "source", string(got.Source),
		logging.KeyReason, err.Error())
	return false
}

// fallbackChain is what a row draws from with no judge involved: the
// spec for whatever shape the output was judged to be, then the raw
// bytes. A composed spec arrives later and replaces it.
//
// markdown.Wants is the one heuristic here, and it is ui's own layer
// rather than a rule the judge never learned: this chain runs before
// anything is judged and with no Driver at all, the same way
// heuristicPost classifies when Jev is absent. The judge is offered
// markdown too, under file_content.
func fallbackChain(r *stepRow, output string) []*viewspec.Compiled {
	var chain []*viewspec.Compiled
	kind := rowKind(r)
	if kind == KindContent && markdown.Wants(r.command, output) {
		chain = append(chain, compiledMarkdown)
	}
	if c, ok := compiledFallback[kind]; ok {
		chain = append(chain, c)
	}
	return append(chain, compiledPlain)
}

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
		rendered = d.Raw
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

// byKind reads the shipped shape specs under ui's own RenderKind,
// which mirrors viewgen's strings the way every other DTO here does.
func byKind() map[RenderKind]viewspec.Spec {
	out := map[RenderKind]viewspec.Spec{}
	for _, kind := range views.Kinds() {
		if spec, ok := views.ForKind(kind); ok {
			out[RenderKind(kind)] = spec
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

func commandOutput(ec *ExecutedCommand) string {
	out := ec.Result.Stdout
	if ec.Result.Stderr != "" {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += ec.Result.Stderr
	}
	return out
}
