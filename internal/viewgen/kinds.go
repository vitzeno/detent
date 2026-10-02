package viewgen

import (
	"github.com/vitzeno/detent/internal/classify"
	"github.com/vitzeno/detent/viewspec"
)

// Kind* name the shapes a command's output can take. One table defines
// each one's criteria, widgets and worth, so the three cannot drift.
const (
	// KindText is any unstructured text read top to bottom, whatever its
	// length: the viewport scrolls, so length never decides how to draw it.
	KindText    = "plain_text"
	KindTable   = "table"
	KindFiles   = "file_listing"
	KindContent = "file_content"
	KindError   = "error_text"
	KindDiff    = "diff"
	KindJSON    = "structured_json"
)

// meter, stat, text and dots are in no list: each needs a filter, a
// title or an accent map that a field choice cannot supply.

// Kind describes one output shape.
type Kind struct {
	Name string
	// What, NotFor and Examples are Jev's criteria. A many-way choice
	// loses its calibration without a stated counter-case.
	What     string
	NotFor   string
	Examples []string
	// Widgets is what a composed view for this shape may use. A short
	// list is what makes a small judge reliable.
	Widgets []string
	// Generate is false where there is nothing to gain: a diff and a
	// stack trace already draw themselves.
	Generate bool
	// Records marks a shape made of rows, worth a view from a few of them.
	Records bool
}

// Kinds lists every output shape, in the order they are described.
func Kinds() []Kind { return kinds }

// RenderKindCriteria is what Jev classifies output against, built from the
// table that prunes the vocabulary, so every judged kind can be drawn.
func RenderKindCriteria() map[string]any {
	out := make(map[string]any, len(kinds))
	for _, k := range kinds {
		out[k.Name] = map[string]any{
			"what": k.What, "not_for": k.NotFor, "examples": k.Examples,
		}
	}
	return out
}

// RenderKindQuestion asks which shape an output has. The judge asks it
// of a Call and viewgen of a Shell, so the wording lives in one place.
func RenderKindQuestion() classify.Question {
	return classify.Question{
		Instructions: "What shape is this output? Pick how a human should read it.",
		Choice:       &classify.ChoiceQuestion{Criteria: RenderKindCriteria()},
	}
}

var kinds = []Kind{
	{
		Name:     KindText,
		What:     "unstructured text, read top to bottom: logs, build output, a few lines of status",
		NotFor:   "text with a shape worth drawing: aligned columns, a listing, a diff, JSON, or a failure",
		Examples: []string{"go test ./... output", "a docker build log", "pwd", "npm install"},
		// keyvalue and tree too: curl -I and kubectl describe are judged
		// text, and without them drew as a log after parsing correctly.
		Widgets: []string{"log", "errors", "table", "keyvalue", "tree", "list", "histogram",
			"gantt", "badges", viewspec.PanelKind, viewspec.RowKind},
		Generate: true,
	},
	{
		Name:     KindTable,
		What:     "aligned columns with a header row, one record per row",
		NotFor:   "a bare list of paths or names with no header or columns, which is file_listing",
		Examples: []string{"ps aux", "df -h", "ls -la", "docker ps"},
		Widgets: []string{"table", "bar", "gauge", "stack", "diverge", "delta", "histogram",
			"boxplot", "series", "heatmap", "scatter", "gantt", "timeline",
			"keyvalue", "badges", viewspec.PanelKind, viewspec.RowKind},
		Generate: true,
		Records:  true,
	},
	{
		Name:     KindFiles,
		What:     "a list of paths or items to pick from, one per line, with no header or aligned columns",
		NotFor:   "the same listing with a header row and aligned columns, which is table",
		Examples: []string{"find . -name '*.go'", "git diff --name-only", "plain ls"},
		Widgets: []string{"list", "tree", "flow", "badges", "histogram",
			viewspec.PanelKind, viewspec.RowKind},
		Generate: true,
		Records:  true,
	},
	{
		Name:     KindContent,
		What:     "a file's own prose or code body, read in full like a document",
		NotFor:   "well-formed JSON even when it came from cat, which is structured_json",
		Examples: []string{"cat main.go", "cat README.md"},
		// A cat of /etc/hosts or go.sum is content with rows in it.
		Widgets:  []string{"code", "markdown", "log", "table", "keyvalue", "list", "tree"},
		Generate: true,
	},
	{
		Name:     KindError,
		What:     "an error, traceback or compiler complaint that is the command's whole point",
		NotFor:   "text that merely contains some warnings among normal output, which is plain_text",
		Examples: []string{"a failed build's compiler error", "a stack trace", "command not found"},
		Widgets:  []string{"errors", "log"},
		Generate: true,
	},
	{
		Name:     KindDiff,
		What:     "a unified diff: +/- lines with @@ hunk headers",
		NotFor:   "output that merely describes changes in prose; this needs the literal diff format",
		Examples: []string{"git diff", "diff -u a.txt b.txt"},
		Widgets:  []string{"diff"},
	},
	{
		Name:     KindJSON,
		What:     "JSON or other structured data, read as data",
		NotFor:   "a file's prose or code body that merely happens not to be JSON",
		Examples: []string{"curl returning a JSON body", "kubectl get pod -o json"},
		Widgets: []string{"keyvalue", "table", "json", "histogram", "delta", "badges",
			viewspec.PanelKind, viewspec.RowKind},
		Generate: true,
		Records:  true,
	},
}

var byName = func() map[string]Kind {
	out := make(map[string]Kind, len(kinds))
	for _, k := range kinds {
		out[k.Name] = k
	}
	return out
}()

// worthGenerating reports whether this shape earns asking. A kind nobody
// recognises is worth asking about, since the zero Kind's false means nothing.
func worthGenerating(kind string) bool {
	k, ok := byName[kind]
	return !ok || k.Generate
}

// prune returns the registry a view for this shape may be built from. An
// unknown kind gets the whole vocabulary, since the zero Kind has no widgets.
func prune(reg *viewspec.Registry, kind string) *viewspec.Registry {
	k, ok := byName[kind]
	if !ok || len(k.Widgets) == 0 {
		return reg
	}
	return reg.Subset(k.Widgets...)
}
