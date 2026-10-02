package tool

import (
	"errors"
	"fmt"

	"github.com/vitzeno/detent/event"
)

// ReadFile reads one file. Sandboxed, this reads the container's
// filesystem, which is why it lowers to a command like everything else.
type ReadFile struct{}

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

func (ReadFile) Lower(a Args) (string, error) {
	p := a.String("path")
	if p == "" {
		return "", errors.New("path must not be empty")
	}
	n := a.Int("max_lines", 500)
	if n <= 0 {
		return "", fmt.Errorf("max_lines must be positive, got %d", n)
	}
	from := a.Int("offset", 1)
	if from <= 0 {
		return "", fmt.Errorf("offset counts from 1, got %d", from)
	}
	return window(from, n, "[%d more lines, read on with offset %d]", "[the file has %d lines]") + " " + path(p), nil
}
