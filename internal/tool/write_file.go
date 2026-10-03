package tool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
)

// WriteFile writes a whole file. edit_file changes part of one.
type WriteFile struct{}

var _ Native = WriteFile{}

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

// Lower writes with a heredoc and shows the change with diff and awk.
func (WriteFile) Lower(a Args) (string, error) {
	path, err := writeArgs(a)
	if err != nil {
		return "", err
	}
	// A file that was there is shown as a diff. A new one is only counted:
	// the model has just written every line of it.
	created := fmt.Sprintf(`printf 'created %%s, %%s lines\n' %s "$(wc -l < %s | tr -d ' ')"`, quote(path), quote(path))
	return fmt.Sprintf("before=$(mktemp); existed=; [ -f %[1]s ] && cp -- %[1]s \"$before\" && existed=1\n%[2]s\n"+
		`rc=$?; if [ $rc -eq 0 ] && [ -z "$existed" ]; then %[3]s; rm -- "$before"; exit 0; fi; `+"%[4]s",
		quote(path), heredoc(path, a.String("content")), created, showDiff("$before", path)), nil
}

// Run writes the file here and reports it as the command does.
func (WriteFile) Run(ctx context.Context, a Args) capture.Result {
	p, err := writeArgs(a)
	if err != nil {
		return failed(2, "write_file: %v", err)
	}
	content := a.String("content")
	before, existed := "", false
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		// A FIFO or a huge file is refused, and one it cannot read is written without a diff as the command's is.
		switch b, err := readCapped(ctx, p, maxEditBytes); {
		case ctx.Err() != nil:
			return stopped("write_file", "", ctx.Err())
		case err == nil:
			before, existed = string(b), true
		case !info.Mode().IsRegular() || info.Size() > maxEditBytes:
			return failed(2, "write_file: %v", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return stopped("write_file", "", err)
	}
	if err := writeAsShell(p, content); err != nil {
		return failed(2, "write_file: %v", err) // dash's status for a redirection it cannot open
	}
	if !existed {
		return capture.Result{Stdout: fmt.Sprintf("created %s, %d lines\n", p, strings.Count(content, "\n"))}
	}
	return capture.Result{Stdout: changeShown(p, before, content)}
}

func writeArgs(a Args) (string, error) {
	p := a.String("path")
	if p == "" {
		return "", errors.New("path must not be empty")
	}
	return p, nil
}
