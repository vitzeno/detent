package viewgen

import "github.com/vitzeno/detent/viewspec"

// Kind* name the shapes a command's output can take. One table below
// defines all three things a kind is for, so they cannot drift:
// the criteria Jev classifies by, the widgets a generated view may
// draw from, and whether a view is worth generating at all.
const (
	// KindText is any unstructured text read top to bottom. It covers
	// what used to be three kinds split by length: the viewport scrolls
	// whatever it is given, so how long the output runs never decided
	// how to draw it, and asking Jev to tell three of those apart cost
	// calibration on the distinctions that matter.
	KindText    = "plain_text"
	KindTable   = "table"
	KindFiles   = "file_listing"
	KindContent = "file_content"
	KindError   = "error_text"
	KindDiff    = "diff"
	KindJSON    = "structured_json"
)

// Kind describes one output shape.
type Kind struct {
	Name string
	// What, NotFor and Examples are Jev's criteria. Structured rather
	// than flat strings because a nine-way choice loses its
	// calibration without a stated counter-case.
	What     string
	NotFor   string
	Examples []string
	// Widgets is what a generated view for this shape may use.
	// Offering a model four kinds instead of fifteen is the single
	// biggest thing that makes a small one reliable.
	Widgets []string
	// Generate is false where there is nothing to gain: a few lines of
	// output has no view worth a model call, and most steps are that.
	Generate bool
}

var kinds = []Kind{
	{
		Name:     KindText,
		What:     "unstructured text, read top to bottom: logs, build output, a few lines of status",
		NotFor:   "text with a shape worth drawing: aligned columns, a listing, a diff, JSON, or a failure",
		Examples: []string{"go test ./... output", "a docker build log", "pwd", "npm install"},
		Widgets:  []string{"log", "errors", "table", "meter", "badges", "text", viewspec.RowKind},
		Generate: true,
	},
	{
		Name:     KindTable,
		What:     "aligned columns with a header row, one record per row",
		NotFor:   "a bare list of paths or names with no header or columns, which is file_listing",
		Examples: []string{"ps aux", "df -h", "ls -la", "docker ps"},
		Widgets:  []string{"table", "bar", "keyvalue", "meter", "badges", "text", viewspec.RowKind},
		Generate: true,
	},
	{
		Name:     KindFiles,
		What:     "a list of paths or items to pick from, one per line, with no header or aligned columns",
		NotFor:   "the same listing with a header row and aligned columns, which is table",
		Examples: []string{"find . -name '*.go'", "git diff --name-only", "plain ls"},
		Widgets:  []string{"list", "tree", "badges", "meter", "text", viewspec.RowKind},
		Generate: true,
	},
	{
		Name:     KindContent,
		What:     "a file's own prose or code body, read in full like a document",
		NotFor:   "well-formed JSON even when it came from cat, which is structured_json",
		Examples: []string{"cat main.go", "cat README.md"},
		Widgets:  []string{"code", "log", "text"},
		Generate: true,
	},
	{
		Name:     KindError,
		What:     "an error, traceback or compiler complaint that is the command's whole point",
		NotFor:   "text that merely contains some warnings among normal output, which is plain_text",
		Examples: []string{"a failed build's compiler error", "a stack trace", "command not found"},
		Widgets:  []string{"errors", "log", "text"},
		Generate: true,
	},
	{
		Name:     KindDiff,
		What:     "a unified diff: +/- lines with @@ hunk headers",
		NotFor:   "output that merely describes changes in prose; this needs the literal diff format",
		Examples: []string{"git diff", "diff -u a.txt b.txt"},
		Widgets:  []string{"diff", "text"},
	},
	{
		Name:     KindJSON,
		What:     "JSON or other structured data, read as data",
		NotFor:   "a file's prose or code body that merely happens not to be JSON",
		Examples: []string{"curl returning a JSON body", "kubectl get pod -o json"},
		Widgets:  []string{"keyvalue", "table", "json", "text", viewspec.RowKind},
		Generate: true,
	},
}

var byName = func() map[string]Kind {
	out := make(map[string]Kind, len(kinds))
	for _, k := range kinds {
		out[k.Name] = k
	}
	return out
}()

// Kinds lists every output shape, in the order they are described.
func Kinds() []Kind { return kinds }

// RenderKindCriteria is what Jev classifies output against. Built from
// the same table that prunes the vocabulary, so a kind cannot be
// judged into existence and then have nothing able to draw it.
func RenderKindCriteria() map[string]any {
	out := make(map[string]any, len(kinds))
	for _, k := range kinds {
		out[k.Name] = map[string]any{
			"what": k.What, "not_for": k.NotFor, "examples": k.Examples,
		}
	}
	return out
}

// worthGenerating reports whether this shape earns a model call.
func worthGenerating(kind string) bool { return byName[kind].Generate }

// prune returns the registry a view for this shape may be built from.
// An unknown kind gets the whole vocabulary rather than none.
func prune(reg *viewspec.Registry, kind string) *viewspec.Registry {
	k, ok := byName[kind]
	if !ok || len(k.Widgets) == 0 {
		return reg
	}
	return reg.Subset(k.Widgets...)
}
