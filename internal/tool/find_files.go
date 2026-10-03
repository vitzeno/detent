package tool

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vitzeno/detent/event"
)

// FindFiles lists files by name, sorted, skipping .git.
type FindFiles struct{}

func (FindFiles) Name() string { return "find_files" }

func (FindFiles) Describe() Spec {
	return Spec{
		Description: "Find files whose name matches a glob, such as *_test.go. A pattern with a slash " +
			"matches the whole path instead, such as */internal/*.go.",
		Params: []Param{
			{Name: "pattern", Type: TypeString, Desc: "glob to match against names, or paths if it has a /", Required: true},
			{Name: "path", Type: TypeString, Desc: "directory to search (default .)"},
			{Name: "max_results", Type: TypeInt, Desc: "stop after this many files (default 200)"},
		},
		Mutability: event.MutRead,
	}
}

func (FindFiles) Lower(a Args) (string, error) {
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
	match := "-name"
	if strings.Contains(pattern, "/") {
		match = "-path"
	}
	return keepStatus(fmt.Sprintf("find %s -name .git -prune -o -type f %s %s -print", path(where), match, quote(pattern)),
		"sort | "+window(1, n, "[%d more files, narrow the pattern or the path]", "[no files match]"), 0), nil
}
