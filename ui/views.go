package ui

import (
	"strings"

	"github.com/vitzeno/detent/ui/markdown"
	"github.com/vitzeno/detent/viewspec"
)

// Hand-written views, one per command shape, compiled once at init.
// Phase 4 replaces this map with generated specs on disk; the point of
// writing them by hand first is to find out whether the vocabulary is
// expressive enough before anything depends on it.

// viewRegistry is Standard plus what only detent can provide. A widget
// registered here reaches the model's schema too, since generation
// asks the registry rather than a hand-written list.
var viewRegistry = func() *viewspec.Registry {
	r := viewspec.Standard()
	must(r.Widget("markdown", viewspec.WidgetFunc(drawMarkdown)))
	return r
}()

// drawMarkdown renders prose through glamour, which viewspec can never
// do itself — it is a heavy dependency, and the package imports only
// the standard library.
func drawMarkdown(_ viewspec.Block, d viewspec.Data, f viewspec.Frame) ([]string, error) {
	out, err := markdown.Render(d.Raw, f.Width)
	if err != nil {
		return strings.Split(d.Raw, "\n"), nil
	}
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n"), nil
}

func must(err error) {
	if err != nil {
		panic(err)
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
	// Porcelain v1 is two status characters then the path, so the code
	// keeps its leading space: " M" is modified-in-worktree, "M " is
	// staged. The character class is what excludes --branch's "## main"
	// header, which a bare ".." would happily match as a file.
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

// compiled holds every hand-written spec that compiles. A spec that
// doesn't is a programming error, but one bad entry must not take the
// whole map with it.
var compiled = func() map[string]*viewspec.Compiled {
	out := map[string]*viewspec.Compiled{}
	for key, spec := range handWritten {
		if c, err := viewspec.Compile(spec, viewspec.WithRegistry(viewRegistry)); err == nil {
			out[key] = c
		}
	}
	return out
}()

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

// boundView resolves a row's view, binding once and caching on the row
// — Draw runs per frame, Bind must not. A failure is remembered as a
// miss so a spec that doesn't fit this output is tried only once.
func boundView(r *stepRow) (*viewspec.Bound, bool) {
	if r == nil || r.cmd.running || r.cmd.ec == nil {
		return nil, false
	}
	if r.cmd.viewTried {
		return r.cmd.view, r.cmd.view != nil
	}
	r.cmd.viewTried = true
	c, ok := compiled[normaliseCommand(r.command)]
	if !ok {
		return nil, false
	}
	b, err := c.Bind(commandOutput(r.cmd.ec))
	if err != nil {
		return nil, false
	}
	r.cmd.view = b
	return b, true
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
