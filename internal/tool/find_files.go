package tool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
)

// FindFiles lists files by name, sorted, skipping .git.
type FindFiles struct{}

var _ Native = FindFiles{}

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
	f, err := findArgs(a)
	if err != nil {
		return "", err
	}
	match := "-name"
	if f.byPath() {
		match = "-path"
	}
	// -H follows a path that is itself a symlink, as grep does.
	return keepStatus(fmt.Sprintf("find -H %s -name .git -prune -o -type f %s %s -print", path(f.where), match, quote(f.pattern)),
		"sort | "+window(1, f.limit, findMore, findEmpty), 0), nil
}

// Run walks here, matching with fnmatch's rules as find does.
func (FindFiles) Run(ctx context.Context, a Args) capture.Result {
	f, err := findArgs(a)
	if err != nil {
		return failed(1, "find_files: %v", err)
	}
	root := guard(f.where)
	files := topLines(f.limit, strings.Compare)
	var stderr strings.Builder
	fail := func(p string, err error) { stderr.WriteString("find: " + osReason(p, err) + "\n") }
	visit := func(p string) {
		name := filepath.Base(p)
		if name == ".git" {
			return
		}
		if f.byPath() && fnmatch(f.pattern, p) || !f.byPath() && fnmatch(f.pattern, name) {
			files.add(p)
		}
	}

	switch info, err := os.Stat(root); {
	case err != nil:
		fail(root, err)
	case info.IsDir():
		if filepath.Base(root) != ".git" {
			if err := walkFiles(ctx, root, func(name string) bool { return name == ".git" }, visit, fail); err != nil {
				return stopped("find_files", windowTop(files.kept, files.total, f.limit, findMore, findEmpty), err)
			}
		}
	case info.Mode().IsRegular():
		visit(root)
	}

	res := capture.Result{Stdout: windowTop(files.kept, files.total, f.limit, findMore, findEmpty), Stderr: stderr.String()}
	if res.Stderr != "" {
		res.ExitCode = 1
	}
	return res
}

const (
	findMore  = "[%d more files, narrow the pattern or the path]"
	findEmpty = "[no files match]"
)

type findQuery struct {
	pattern, where string
	limit          int
}

// byPath is whether the pattern names a path rather than a file name.
func (f findQuery) byPath() bool { return strings.Contains(f.pattern, "/") }

// findArgs is what both ways of finding accept.
func findArgs(a Args) (findQuery, error) {
	f := findQuery{pattern: a.String("pattern"), where: a.String("path"), limit: a.Int("max_results", 200)}
	if f.pattern == "" {
		return f, errors.New("pattern must not be empty")
	}
	if f.limit <= 0 {
		return f, fmt.Errorf("max_results must be positive, got %d", f.limit)
	}
	if f.where == "" {
		f.where = "."
	}
	return f, nil
}
