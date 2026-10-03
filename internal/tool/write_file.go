package tool

import (
	"errors"
	"fmt"

	"github.com/vitzeno/detent/event"
)

// WriteFile writes a whole file. edit_file changes part of one.
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
		Renders:    event.RendersDiff,
	}
}

func (WriteFile) Lower(a Args) (string, error) {
	path := a.String("path")
	if path == "" {
		return "", errors.New("path must not be empty")
	}
	// A file that was there is shown as a diff. A new one is only counted:
	// the model has just written every line of it.
	created := fmt.Sprintf(`printf 'created %%s, %%s lines\n' %s "$(wc -l < %s | tr -d ' ')"`, quote(path), quote(path))
	return fmt.Sprintf("before=$(mktemp); existed=; [ -f %[1]s ] && cp -- %[1]s \"$before\" && existed=1\n%[2]s\n"+
		`rc=$?; if [ $rc -eq 0 ] && [ -z "$existed" ]; then %[3]s; rm -- "$before"; exit 0; fi; `+"%[4]s",
		quote(path), heredoc(path, a.String("content")), created, showDiff("$before", path)), nil
}
