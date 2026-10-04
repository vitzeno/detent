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
	return fmt.Sprintf("before=$(mktemp); [ -f %[1]s ] && cp -- %[1]s \"$before\"\n%[2]s\n"+
		`rc=$?; %[3]s`,
		quote(path), heredoc(path, a.String("content")), showDiff("$before", path)), nil
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
		// A FIFO or a huge file is refused, and an unreadable one is written without a
		// diff, as the command does.
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
	if existed {
		content = keepCRLF(before, content)
	}
	if err := writeAsShell(p, content); err != nil {
		return failed(2, "write_file: %v", err) // dash's status for a redirection it cannot open
	}
	return capture.Result{Stdout: changeShown(p, before, content)}
}

// keepCRLF gives content CRLF endings when it replaces a file that was all CRLF,
// since read_file showed that file without its \r. Mixed or chosen endings stand.
func keepCRLF(before, content string) string {
	crlf := strings.Count(before, "\r\n")
	if crlf == 0 || crlf != strings.Count(before, "\n") || strings.Contains(content, "\r") {
		return content
	}
	return strings.ReplaceAll(content, "\n", "\r\n")
}

func writeArgs(a Args) (string, error) {
	p := a.String("path")
	if p == "" {
		return "", errors.New("path must not be empty")
	}
	return p, nil
}
