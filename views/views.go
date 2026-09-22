// Package views holds every spec detent ships: the ones chosen by a
// command's name, and the ones chosen by the shape its output turned
// out to have.
//
// They live together because they had started to overlap. The
// render-kind spec for a table and the shipped spec for ps are nearly
// the same thing, written twice in two packages that could not see
// each other, and the ps one had already drifted: written for plain
// ps, it silently drew nothing for ps aux.
//
// It imports viewspec and nothing else, which is what lets both ui and
// viewgen use it without either importing the other.
package views

import "github.com/vitzeno/detent/viewspec"

// ForCommand is the spec detent ships for a command, by its
// normalised name. A seed rather than a default: a saved spec is read
// first, and composition is tried before falling back here, so this is
// a floor and not a ceiling.
func ForCommand(name string) (*viewspec.Spec, bool) {
	spec, ok := byCommand[name]
	if !ok {
		return nil, false
	}
	return &spec, true
}

// ForKind is the spec for output judged to have a given shape, used
// when nothing is keyed to the command. Kinds are the strings
// internal/viewgen/kinds.go defines; they are spelled out here rather
// than imported, since ui reads this package and imports nothing under
// internal.
func ForKind(kind string) (viewspec.Spec, bool) {
	spec, ok := byKind[kind]
	return spec, ok
}

// Kinds lists every shape with a spec of its own.
func Kinds() []string {
	out := make([]string, 0, len(byKind))
	for k := range byKind {
		out = append(out, k)
	}
	return out
}

// Commands lists every command with a shipped spec.
func Commands() []string {
	out := make([]string, 0, len(byCommand))
	for c := range byCommand {
		out = append(out, c)
	}
	return out
}

// Raw draws the output as it came, through one widget. The floor of
// every fallback chain, and what most shapes want.
func Raw(widget string) viewspec.Spec {
	return viewspec.Spec{
		Version: viewspec.Version,
		Parse:   viewspec.Parse{Kind: "none"},
		Blocks:  []viewspec.Block{{Kind: widget}},
	}
}

// byKind is what a render kind draws with when no spec is keyed to the
// command. One block each: these were a switch statement once.
var byKind = map[string]viewspec.Spec{
	"plain_text":      Raw("log"),
	"error_text":      Raw("errors"),
	"diff":            Raw("diff"),
	"structured_json": Raw("json"),
	"file_content":    Raw("code"),
	"table": {
		Version: viewspec.Version,
		Parse:   viewspec.Parse{Kind: "columns", Header: true},
		Blocks:  []viewspec.Block{{Kind: "table"}},
	},
	"file_listing": {
		Version: viewspec.Version,
		Parse:   viewspec.Parse{Kind: "lines", Pattern: `^(?P<path>\S.*)$`},
		Blocks:  []viewspec.Block{{Kind: "list", Field: "path"}},
	},
}

// byCommand is keyed by the normalised command name.
var byCommand = map[string]viewspec.Spec{
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
				Accent:  accent("status", map[string]viewspec.Role{"ok": viewspec.RoleSafe, "FAIL": viewspec.RoleDanger}),
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
				Accent: accent("code", gitCodes)},
			{Kind: "list", Field: "path",
				Sort:    &viewspec.Sort{Field: "path"},
				Accent:  accent("code", gitCodes),
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

func accent(field string, m map[string]viewspec.Role) *viewspec.Accent {
	return &viewspec.Accent{Field: field, Map: m}
}
