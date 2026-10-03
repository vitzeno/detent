package tool

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
)

// Grep searches file contents. Declared read-only, it runs beside other
// reads and skips the checks a bash grep would pay for.
type Grep struct{}

var _ Native = Grep{}

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
	g, err := grepArgs(a)
	if err != nil {
		return "", err
	}
	flags := []string{"-rnIHE", "--exclude-dir=.git"}
	if g.ignoreCase {
		flags = append(flags, "-i")
	}
	if g.include != "" {
		flags = append(flags, "--include="+quote(g.include))
	}
	// grep exits 1 for no match and 2 for an error, which only the latter should report.
	return keepStatus(fmt.Sprintf("grep %s -e %s -- %s", strings.Join(flags, " "), quote(g.pattern), quote(g.where)),
		"sort -t: -k1,1 -k2,2n | "+window(1, g.limit, grepMore, grepEmpty), 1), nil
}

// Run searches here with Go's regexp, which takes the ERE most patterns
// are written in but not a backreference.
func (Grep) Run(ctx context.Context, a Args) capture.Result {
	g, err := grepArgs(a)
	if err != nil {
		return failed(2, "grep: %v", err)
	}
	re, err := regexp.Compile(g.regexp())
	if err != nil {
		return capture.Result{ExitCode: 2, Stdout: footer(grepEmpty, 0), Stderr: "grep: " + err.Error() + "\n"}
	}
	var hits []string
	var stderr strings.Builder
	fail := func(p string, err error) { stderr.WriteString("grep: " + osReason(p, err) + "\n") }
	search := func(p string) {
		if g.include != "" && !fnmatch(g.include, filepath.Base(p)) {
			return
		}
		h, err := grepFile(p, re)
		if err != nil {
			fail(p, err)
		}
		hits = append(hits, h...)
	}

	switch info, err := os.Stat(g.where); {
	case err != nil:
		fail(g.where, err)
	case info.IsDir():
		if filepath.Base(g.where) != ".git" {
			if err := walkFiles(ctx, g.where, func(name string) bool { return name == ".git" }, search, fail); err != nil {
				return failed(1, "grep: %v", err)
			}
		}
	case info.Mode().IsRegular():
		search(g.where)
	}

	slices.SortFunc(hits, sortedByPathThenLine)
	res := capture.Result{Stdout: windowOf(hits, g.limit, grepMore, grepEmpty), Stderr: stderr.String()}
	if res.Stderr != "" {
		res.ExitCode = 2
	}
	return res
}

const (
	grepMore  = "[%d more matches, narrow the pattern or the path]"
	grepEmpty = "[no matches]"
)

type grepQuery struct {
	pattern, where, include string
	ignoreCase              bool
	limit                   int
}

// grepArgs is what both ways of searching accept.
func grepArgs(a Args) (grepQuery, error) {
	g := grepQuery{
		pattern: a.String("pattern"), where: a.String("path"), include: a.String("include"),
		ignoreCase: a.Bool("ignore_case", false), limit: a.Int("max_results", 200),
	}
	if g.pattern == "" {
		return g, errors.New("pattern must not be empty")
	}
	if g.limit <= 0 {
		return g, fmt.Errorf("max_results must be positive, got %d", g.limit)
	}
	if g.where == "" {
		g.where = "."
	}
	return g, nil
}

// regexp is the pattern in Go's syntax, with grep's word edges as \b.
func (g grepQuery) regexp() string {
	var b strings.Builder
	if g.ignoreCase {
		b.WriteString("(?i)")
	}
	for i := 0; i < len(g.pattern); i++ {
		c := g.pattern[i]
		if c == '\\' && i+1 < len(g.pattern) {
			i++
			if n := g.pattern[i]; n == '<' || n == '>' {
				b.WriteString(`\b`)
			} else {
				b.WriteByte(c)
				b.WriteByte(n)
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// grepFile is the lines of p that re matches, as path:line:text, and none
// at all for a file holding a NUL, which grep -I calls binary.
func grepFile(p string, re *regexp.Regexp) ([]string, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read only, so closing cannot lose anything
	var hits []string
	r := bufio.NewReader(f)
	for n := 1; ; n++ {
		line, err := r.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			if errors.Is(err, io.EOF) {
				return hits, nil
			}
			return hits, err
		}
		line = bytes.TrimSuffix(line, []byte("\n"))
		if bytes.IndexByte(line, 0) >= 0 {
			return nil, nil
		}
		if re.Match(line) {
			// The window cuts a line past outputBudget anyway, so more is never shown.
			hit := p + ":" + strconv.Itoa(n) + ":" + string(line)
			hits = append(hits, hit[:min(len(hit), outputBudget+1)])
		}
	}
}

// sortedByPathThenLine orders grep's output as sort -t: -k1,1 -k2,2n does
// in the C locale: path, then line number, then the whole line.
func sortedByPathThenLine(a, b string) int {
	pa, ra, _ := strings.Cut(a, ":")
	pb, rb, _ := strings.Cut(b, ":")
	if c := strings.Compare(pa, pb); c != 0 {
		return c
	}
	if c := cmp.Compare(leadingInt(ra), leadingInt(rb)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// leadingInt is the number s starts with, or 0, as sort -n reads a field.
func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end]) // all digits, so only an overflow fails, at 0
	return n
}
