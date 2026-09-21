package viewgen

import "github.com/vitzeno/detent/viewspec"

// Jev's render_kind values, duplicated as literals rather than
// imported: agent imports this package. Same choice ui/status makes
// for the same reason.
const (
	kindInline  = "inline_short"
	kindLog     = "scrollable_log"
	kindTable   = "table"
	kindFiles   = "file_listing"
	kindContent = "file_content"
	kindError   = "error_text"
	kindDiff    = "diff"
	kindJSON    = "structured_json"
	kindQuiet   = "quiet_progress"
)

// candidates narrows the vocabulary by what Jev already said the
// output is. Offering a model four kinds instead of fifteen is the
// single biggest thing that makes a small one reliable here, and
// render_kind is already paid for whether or not a view is generated.
var candidates = map[string][]string{
	kindTable:   {"table", "bar", "keyvalue", "meter", "badges", "text", viewspec.RowKind},
	kindFiles:   {"list", "tree", "badges", "meter", "text", viewspec.RowKind},
	kindJSON:    {"keyvalue", "table", "json", "text", viewspec.RowKind},
	kindError:   {"errors", "log", "text"},
	kindDiff:    {"diff", "text"},
	kindContent: {"code", "log", "text"},
	kindLog:     {"log", "errors", "table", "meter", "badges", "text", viewspec.RowKind},
	kindInline:  {"log", "keyvalue", "text"},
	kindQuiet:   {"log", "meter", "text"},
}

// worthGenerating is false where there is nothing to gain: a few lines
// of output has no view worth a model call, and most steps are that.
func worthGenerating(kind string) bool {
	switch kind {
	case kindInline, kindQuiet, kindDiff:
		return false
	}
	_, known := candidates[kind]
	return known
}

// prune returns the registry a spec for this kind may be built from.
// An unknown kind gets the whole vocabulary rather than none.
func prune(reg *viewspec.Registry, kind string) *viewspec.Registry {
	allowed, ok := candidates[kind]
	if !ok {
		return reg
	}
	return reg.Subset(allowed...)
}
