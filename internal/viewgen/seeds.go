package viewgen

import "github.com/vitzeno/detent/viewspec"

// Specs that ship with detent, keyed by command shape. They are seeds
// rather than defaults: the store is read first, so a spec the human
// has edited wins, and Generate asks the model before falling back
// here, so a shipped spec is a floor rather than a ceiling.
var seeds = map[string]viewspec.Spec{
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

// seed returns the shipped spec for a command shape, if there is one.
func seed(command string) (*viewspec.Spec, bool) {
	spec, ok := seeds[Normalise(command)]
	if !ok {
		return nil, false
	}
	return &spec, true
}
