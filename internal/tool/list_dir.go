package tool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
)

// listEntries is the most entries a listing shows.
const listEntries = 1000

// ListDir lists a directory. It lowers to a command so a sandboxed
// session sees the container's filesystem, not the host's.
type ListDir struct{}

var _ Native = ListDir{}

func (ListDir) Name() string { return "list_dir" }

func (ListDir) Describe() Spec {
	return Spec{
		Description: "List a directory's contents.",
		Params: []Param{
			{Name: "path", Type: TypeString, Desc: "directory to list (default .)"},
			{Name: "all", Type: TypeBool, Desc: "include dotfiles"},
		},
		Mutability: event.MutRead,
	}
}

func (ListDir) Lower(a Args) (string, error) {
	flags := "-l"
	if a.Bool("all", false) {
		flags = "-la"
	}
	return fmt.Sprintf("ls %s -- %s", flags, quote(listPath(a))), nil
}

// Run lists here in a format of its own, since ls -l differs by OS: mode,
// size, modified time and name, with a directory ending in a slash.
func (ListDir) Run(ctx context.Context, a Args) capture.Result {
	p := listPath(a)
	info, err := os.Stat(p)
	if err != nil {
		return failed(2, "list_dir: %s", osReason(p, err))
	}
	if !info.IsDir() {
		return capture.Result{Stdout: listing([]listed{{p, info, ""}}, 1)}
	}
	d, err := os.Open(p)
	if err != nil {
		return failed(2, "list_dir: %s", osReason(p, err))
	}
	defer func() { _ = d.Close() }() // read only, so closing cannot lose anything
	all := a.Bool("all", false)
	names := &top[string]{cmp: strings.Compare, limit: listEntries}
	for {
		if err := ctx.Err(); err != nil {
			return stopped("list_dir", "", err)
		}
		chunk, err := d.Readdirnames(256)
		for _, name := range chunk {
			if all || !strings.HasPrefix(name, ".") {
				names.add(name)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return failed(2, "list_dir: %s", osReason(p, err))
		}
	}
	var rows []listed
	for _, name := range names.kept {
		fi, err := os.Lstat(found(p, name))
		if err != nil {
			continue // gone since it was listed
		}
		target := ""
		if fi.Mode()&fs.ModeSymlink != 0 {
			target, _ = os.Readlink(found(p, name)) // an unreadable link shows no target
		}
		rows = append(rows, listed{name, fi, target})
	}
	return capture.Result{Stdout: listing(rows, names.total-len(names.kept)+len(rows))}
}

func listPath(a Args) string {
	if p := a.String("path"); p != "" {
		return p
	}
	return "."
}

// listed is one entry and, for a symlink, where it points.
type listed struct {
	name   string
	info   fs.FileInfo
	target string
}

// listing draws one line per entry with sizes aligned, through the same
// window as a read so a huge directory still ends in a footer.
func listing(rows []listed, total int) string {
	sizes := make([]string, len(rows))
	width := 0
	for i, r := range rows {
		sizes[i] = "-"
		if !r.info.IsDir() {
			sizes[i] = strconv.FormatInt(r.info.Size(), 10)
		}
		width = max(width, len(sizes[i]))
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		name := printable(r.name)
		switch {
		case r.info.IsDir():
			name += "/"
		case r.info.Mode()&fs.ModeSymlink != 0:
			name += " -> " + printable(r.target)
		}
		lines[i] = fmt.Sprintf("%s  %*s  %s  %s", modeString(r.info.Mode()), width, sizes[i],
			r.info.ModTime().Format("2006-01-02 15:04"), name)
	}
	return windowTop(lines, total, listEntries, "[%d more entries, list a narrower path]", "[the directory is empty]")
}

// modeString is the mode as ls -l spells it: a type letter, then rwx.
func modeString(m fs.FileMode) string {
	kind := "-"
	switch {
	case m.IsDir():
		kind = "d"
	case m&fs.ModeSymlink != 0:
		kind = "l"
	case m&fs.ModeNamedPipe != 0:
		kind = "p"
	case m&fs.ModeSocket != 0:
		kind = "s"
	case m&fs.ModeCharDevice != 0:
		kind = "c"
	case m&fs.ModeDevice != 0:
		kind = "b"
	}
	return kind + m.Perm().String()[1:]
}

// printable quotes a name holding a newline or another control character,
// which would otherwise break one entry per line.
func printable(name string) string {
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return strconv.Quote(name)
	}
	return name
}
