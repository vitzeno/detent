package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/vitzeno/detent/ui/markdown"
	"github.com/vitzeno/detent/viewspec"
)

// Registry is the vocabulary detent draws with: viewspec's own plus
// what only this process can provide. cmd/detent hands it to the
// generator, so the model is offered exactly what will draw.
func Registry() *viewspec.Registry { return viewRegistry }

// generateView asks the Driver for a view, off the Update loop. Fired
// once per row, after judging, because render_kind is what prunes the
// vocabulary the model may draw from.
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
		if !ok {
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
			continue
		}
		r.cmd.view = b
		return b, true
	}
	return nil, false
}

// applyView swaps in a generated spec. It must compile against this
// registry and bind against this output before it replaces anything.
// A spec that arrives broken leaves the fallback exactly as it was.
func applyView(r *stepRow, got GeneratedView) bool {
	if r == nil || got.Spec == nil || r.cmd.ec == nil {
		return false
	}
	c, err := viewspec.Compile(*got.Spec, viewspec.WithRegistry(viewRegistry))
	if err != nil {
		return false
	}
	b, err := c.Bind(commandOutput(r.cmd.ec))
	if err != nil {
		return false
	}
	r.cmd.view, r.cmd.viewTried = b, true
	r.cmd.viewSource = got.Source
	return true
}

// fallbackChain is what a row's view is tried against with no model
// involved: the built-in for whatever the output was judged to be,
// then the raw bytes. A generated spec arrives later and replaces it.
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

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// fallbackSpecs is what render_kind chooses from when no view is keyed
// to the command. One block each: the nine kinds were always specs,
// they just used to be a switch.
var fallbackSpecs = map[RenderKind]viewspec.Spec{
	KindInline:  rawSpec("log"),
	KindQuiet:   rawSpec("log"),
	KindLog:     rawSpec("log"),
	KindError:   rawSpec("errors"),
	KindDiff:    rawSpec("diff"),
	KindJSON:    rawSpec("json"),
	KindContent: rawSpec("code"),
	KindTable: {
		Version: viewspec.Version,
		Parse:   viewspec.Parse{Kind: "columns", Header: true},
		Blocks:  []viewspec.Block{{Kind: "table"}},
	},
	KindFiles: {
		Version: viewspec.Version,
		Parse:   viewspec.Parse{Kind: "lines", Pattern: `^(?P<path>\S.*)$`},
		Blocks:  []viewspec.Block{{Kind: "list", Field: "path"}},
	},
}

func rawSpec(kind string) viewspec.Spec {
	return viewspec.Spec{
		Version: viewspec.Version,
		Parse:   viewspec.Parse{Kind: "none"},
		Blocks:  []viewspec.Block{{Kind: kind}},
	}
}

var (
	compiledFallback = compileAll(fallbackSpecs)
	compiledMarkdown = compileAll(map[string]viewspec.Spec{"m": rawSpec("markdown")})["m"]
	// compiledPlain is the floor: raw bytes, no interpretation. Used
	// before anything is judged, and when a fitted spec doesn't fit.
	compiledPlain = compileAll(map[string]viewspec.Spec{"p": rawSpec("log")})["p"]
)

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
