package tool

import (
	"errors"

	"github.com/vitzeno/detent/event"
)

// WriteFile writes a whole file. With no editor, this and a shell
// redirect are the only ways one changes.
type WriteFile struct{}

func (WriteFile) Name() string { return "write_file" }

func (WriteFile) Describe() Spec {
	return Spec{
		Description: "Write a file, replacing it if it exists. Give the whole contents.",
		Params: []Param{
			{Name: "path", Type: TypeString, Desc: "path to write", Required: true},
			{Name: "content", Type: TypeString, Desc: "the file's full contents", Required: true},
		},
		Mutability: event.MutWorkspace,
	}
}

func (WriteFile) Lower(a Args) (string, error) {
	path := a.String("path")
	if path == "" {
		return "", errors.New("path must not be empty")
	}
	return heredoc(path, a.String("content")), nil
}
