package tool

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vitzeno/detent/event"
)

// Grep searches file contents. Declared read-only, it runs beside other
// reads and skips the checks a bash grep would pay for.
type Grep struct{}

func (Grep) Name() string { return "grep" }

func (Grep) Describe() Spec {
	return Spec{
		Description: "Search file contents for an extended regular expression, recursively. " +
			"Prints path:line:text for each match, skipping binary files and .git.",
		Params: []Param{
			{Name: "pattern", Type: TypeString, Desc: "extended regular expression to search for", Required: true},
			{Name: "path", Type: TypeString, Desc: "file or directory to search (default .)"},
			{Name: "include", Type: TypeString, Desc: "only files whose name matches this glob, such as *.go"},
			{Name: "ignore_case", Type: TypeBool, Desc: "match case-insensitively"},
			{Name: "max_results", Type: TypeInt, Desc: "stop after this many matching lines (default 200)"},
		},
		Mutability: event.MutRead,
	}
}

func (Grep) Lower(a Args) (string, error) {
	pattern := a.String("pattern")
	if pattern == "" {
		return "", errors.New("pattern must not be empty")
	}
	n := a.Int("max_results", 200)
	if n <= 0 {
		return "", fmt.Errorf("max_results must be positive, got %d", n)
	}
	where := a.String("path")
	if where == "" {
		where = "."
	}
	flags := []string{"-rnIE", "--exclude-dir=.git"}
	if a.Bool("ignore_case", false) {
		flags = append(flags, "-i")
	}
	if g := a.String("include"); g != "" {
		flags = append(flags, "--include="+quote(g))
	}
	// grep exits 1 for no match and 2 for an error, which only the latter should report.
	return keepStatus(fmt.Sprintf("grep %s -e %s -- %s", strings.Join(flags, " "), quote(pattern), quote(where)),
		"sort -t: -k1,1 -k2,2n | "+window(1, n, "[%d more matches, narrow the pattern or the path]", "[no matches]"), 1), nil
}
