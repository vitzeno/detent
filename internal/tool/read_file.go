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
		Description: "Read a file's contents.",
		Params: []Param{
			{Name: "path", Type: TypeString, Desc: "path to the file", Required: true},
			{Name: "max_lines", Type: TypeInt, Desc: "stop after this many lines (default 500)"},
		},
		Mutability: event.MutRead,
	}
}

func (ReadFile) Lower(a Args) (string, error) {
	path := a.String("path")
	if path == "" {
		return "", errors.New("path must not be empty")
	}
	n := a.Int("max_lines", 500)
	if n <= 0 {
		return "", fmt.Errorf("max_lines must be positive, got %d", n)
	}
	return fmt.Sprintf("head -n %d -- %s", n, quote(path)), nil
}
