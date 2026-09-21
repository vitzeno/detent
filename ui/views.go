package ui

import (
	"strings"

	"github.com/vitzeno/detent/ui/markdown"
	"github.com/vitzeno/detent/viewspec"
)

// Hand-written views, one per command shape, compiled once at init.
// Phase 4 replaces this map with generated specs on disk.

// viewRegistry is Standard plus what only detent can provide. A widget
// registered here reaches the model's schema too, since generation
// asks the registry rather than a hand-written list.
var viewRegistry = func() *viewspec.Registry {
	r := viewspec.Standard()
	must(r.Widget("markdown", &markdownWidget{}))
	return r
}()

// markdownWidget renders prose through glamour, which viewspec cannot
// — it imports only the standard library. Caches its last render
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

var handWritten = map[string]viewspec.Spec{
	"go test": {
		Version: viewspec.Version,
		Match:   "go test",
		Parse: viewspec.Parse{Kind: "lines",
			Pattern: `^(?P<status>ok|FAIL)\s+(?P<pkg>\S+)\s+(?P<secs>[\d.]+)s`},
		Blocks: []viewspec.Block{
			{Kind: "meter", Title: "passed", CountWhere: "status=ok", Of: "*"},
			{Kind: "table",
				Columns: []viewspec.Column{
					{Field: "status"}, {Field: "pkg", Title: "package"}, {Field: "secs", Title: "took"}},
				Sort:    &viewspec.Sort{Field: "secs", Numeric: true, Desc: true},
				Accent:  accentOn("status", map[string]viewspec.Role{"ok": viewspec.RoleSafe, "FAIL": viewspec.RoleDanger}),
				OnEnter: "go test -v {pkg}"},
		},
	},
	// Porcelain v1 is two status chars then the path, so the code keeps
	// its leading space. The class is what excludes --branch's "## "..
	"git status": {
		Version: viewspec.Version,
		Match:   "git status",
		Parse:   viewspec.Parse{Kind: "lines", Pattern: `^(?P<code>[ MADRCU?!]{2})\s(?P<path>.+)$`},
		Blocks: []viewspec.Block{
			{Kind: "badges", Field: "code",
				Accent: accentOn("code", gitCodes)},
			{Kind: "list", Field: "path",
				Sort:    &viewspec.Sort{Field: "path"},
				Accent:  accentOn("code", gitCodes),
				OnEnter: "git diff -- {path}"},
		},
	},
	// fixed slices at the header's own offsets, which is what reads
	// "CONTAINER ID" as one column where whitespace fields see two.
	"docker ps": {
		Version: viewspec.Version,
		Match:   "docker ps",
		Parse:   viewspec.Parse{Kind: "fixed"},
		Blocks: []viewspec.Block{
			{Kind: "table",
				Columns: []viewspec.Column{
					{Field: "names"}, {Field: "image"}, {Field: "status"}},
				OnEnter: "docker logs --tail 50 {names}"},
		},
	},
	"env": {
		Version: viewspec.Version,
		Match:   "env",
		Parse:   viewspec.Parse{Kind: "pairs", Sep: "="},
		Blocks: []viewspec.Block{
			{Kind: "keyvalue",
				Columns: []viewspec.Column{{Field: "key"}, {Field: "value"}},
				Sort:    &viewspec.Sort{Field: "key"}},
		},
	},
	"find": {
		Version: viewspec.Version,
		Match:   "find",
		Parse:   viewspec.Parse{Kind: "lines", Pattern: `^(?P<path>\S.*)$`},
		Blocks: []viewspec.Block{
			{Kind: "tree", Field: "path",
				Sort:    &viewspec.Sort{Field: "path"},
				OnEnter: "cat {path}"},
		},
	},
	"tree": {
		Version: viewspec.Version,
		Match:   "tree",
		Parse:   viewspec.Parse{Kind: "indent"},
		Blocks: []viewspec.Block{
			{Kind: "tree", Field: "text", Depth: "depth"},
		},
	},
	"ps": {
		Version: viewspec.Version,
		Match:   "ps",
		Parse:   viewspec.Parse{Kind: "columns", Header: true},
		Blocks: []viewspec.Block{
			{Kind: "table",
				Columns: []viewspec.Column{
					{Field: "pid", Width: 8}, {Field: "tty", Width: 10}, {Field: "cmd", Title: "command"}},
				OnEnter: "lsof -p {pid}"},
		},
	},
}

var gitCodes = map[string]viewspec.Role{
	" M": viewspec.RoleCaution,
	"M ": viewspec.RoleSafe,
	"A ": viewspec.RoleSafe,
	" D": viewspec.RoleDanger,
	"D ": viewspec.RoleDanger,
	"??": viewspec.RoleMuted,
}

func accentOn(field string, m map[string]viewspec.Role) *viewspec.Accent {
	return &viewspec.Accent{Field: field, Map: m}
}

var (
	compiled         = compileAll(handWritten)
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

// multiplexers are the programs whose second word names a real
// subcommand. Everything else keys on the program alone — "ps -U me"
// must not key as "ps me", the way "git status" keys as "git status".
var multiplexers = map[string]bool{
	"git": true, "go": true, "docker": true, "kubectl": true, "make": true,
	"npm": true, "yarn": true, "pnpm": true, "cargo": true, "brew": true,
	"apt": true, "pip": true, "systemctl": true,
}

// normaliseCommand keys a view by command shape, so "git status
// --porcelain=v1 --branch" and "git status -sb" share one view.
func normaliseCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	key := fields[0]
	if !multiplexers[key] {
		return key
	}
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") {
			continue
		}
		if strings.ContainsAny(f, "/.") {
			break
		}
		return key + " " + f
	}
	return key
}

// specChain is what a row's view is tried against, in order. A
// command-keyed spec that misses this output falls to the judged
// kind, not to plain: "ps aux" is a table even when "ps"'s spec isn't.
func specChain(r *stepRow, output string) []*viewspec.Compiled {
	var chain []*viewspec.Compiled
	if c, ok := compiled[normaliseCommand(r.command)]; ok {
		chain = append(chain, c)
	}
	kind := rowKind(r)
	if kind == KindContent && markdown.Wants(r.command, output) {
		chain = append(chain, compiledMarkdown)
	}
	if c, ok := compiledFallback[kind]; ok {
		chain = append(chain, c)
	}
	return append(chain, compiledPlain)
}

// boundView resolves a row's view, binding once and caching on the row
// — Draw runs per frame, Bind must not.
func boundView(r *stepRow) (*viewspec.Bound, bool) {
	if r == nil || r.cmd.running || r.cmd.ec == nil {
		return nil, false
	}
	if r.cmd.viewTried {
		return r.cmd.view, r.cmd.view != nil
	}
	r.cmd.viewTried = true
	output := commandOutput(r.cmd.ec)
	for _, c := range specChain(r, output) {
		b, err := c.Bind(output)
		if err != nil {
			continue
		}
		r.cmd.view = b
		return b, true
	}
	return nil, false
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
