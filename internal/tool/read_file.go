package tool

import (
	"context"
	"errors"
	"fmt"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
)

// read_file's footers.
const (
	readMore  = "[%d more lines, read on with offset %d]"
	readEmpty = "[the file has %d lines]"
)

// ReadFile reads one file. Sandboxed, this reads the container's
// filesystem, which is why it lowers to a command like everything else.
type ReadFile struct{}

var _ Native = ReadFile{}

func (ReadFile) Name() string { return "read_file" }

func (ReadFile) Describe() Spec {
	return Spec{
		Description: "Read a file's contents, a window of lines at a time. A footer says how many lines " +
			"are left and the offset to read on from.",
		Params: []Param{
			{Name: "path", Type: TypeString, Desc: "path to the file", Required: true},
			{Name: "offset", Type: TypeInt, Desc: "first line to read, counting from 1 (default 1)"},
			{Name: "max_lines", Type: TypeInt, Desc: "stop after this many lines (default 500)"},
		},
		Mutability: event.MutRead,
	}
}

// Lower is awk over the file, through window.
func (ReadFile) Lower(a Args) (string, error) {
	p, from, n, err := readArgs(a)
	if err != nil {
		return "", err
	}
	return window(from, n, readMore, readEmpty) + " " + path(p), nil
}

// Run reads the file here, in the same window and with the same footers.
func (ReadFile) Run(ctx context.Context, a Args) capture.Result {
	p, from, n, err := readArgs(a)
	if err != nil {
		return failed(2, "read_file: %v", err)
	}
	f, err := openRegular(p)
	if err != nil {
		return failed(1, "read_file: %v", err)
	}
	defer func() { _ = f.Close() }() // read only, so closing cannot lose anything
	out, err := windowLines(ctx, f, from, n, readMore, readEmpty)
	if ctx.Err() != nil {
		return stopped("read_file", out, ctx.Err())
	}
	if err != nil {
		return capture.Result{ExitCode: 1, Stdout: out, Stderr: "read_file: " + err.Error() + "\n"}
	}
	return capture.Result{Stdout: out}
}

// readArgs is what both ways of reading accept.
func readArgs(a Args) (p string, from, n int, err error) {
	p = a.String("path")
	if p == "" {
		return "", 0, 0, errors.New("path must not be empty")
	}
	n = a.Int("max_lines", 500)
	if n <= 0 {
		return "", 0, 0, fmt.Errorf("max_lines must be positive, got %d", n)
	}
	from = a.Int("offset", 1)
	if from <= 0 {
		return "", 0, 0, fmt.Errorf("offset counts from 1, got %d", from)
	}
	return p, from, n, nil
}
