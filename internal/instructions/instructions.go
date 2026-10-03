// Package instructions finds the files a project keeps for coding
// agents, AGENTS.md or CLAUDE.md, and the human's own for every project.
package instructions

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/vitzeno/detent/internal/gitroot"
)

// names are tried in order in each directory, and only the first found is read.
var names = []string{"AGENTS.md", "CLAUDE.md"}

// MaxBytes caps what every file together may add to the prompt. 128KB is
// about 32k tokens, a large share of a small local model's window.
const MaxBytes = 128 * 1024

// File is one instruction file, its Path relative to the working directory.
type File struct {
	Path      string
	Text      string
	Truncated bool
}

// Global is the human's own file for every project, or "" with no home directory.
func Global() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "detent", "AGENTS.md")
}

// Find reads global, then one file per directory from dir's repository root, or dir
// alone outside one, nearest last. One it cannot read is left out and named in the error.
func Find(dir, global string) ([]File, error) {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	var found []File
	var errs []error
	dirs := gitroot.Dirs(dir)
	// A project file may link only within its own repository, never to a key elsewhere.
	within := dirs[len(dirs)-1]
	// Nearest first, so the file that matters most is the last to be cut.
	for _, d := range dirs {
		f, ok, err := firstOf(d, dir, within)
		errs = append(errs, err)
		if ok {
			found = append(found, f)
		}
	}
	if global != "" && !slices.Contains(dirs, filepath.Dir(global)) {
		f, ok, err := read(global, tilde(global), "")
		errs = append(errs, err)
		if ok {
			found = append(found, f)
		}
	}

	var out []File
	budget := MaxBytes
	for _, f := range found {
		if strings.TrimSpace(f.Text) == "" {
			continue
		}
		if budget <= 0 {
			break
		}
		if len(f.Text) > budget {
			f.Text, f.Truncated = cut(f.Text, budget), true
		}
		budget -= len(f.Text)
		out = append(out, f)
	}
	slices.Reverse(out)
	return out, errors.Join(errs...)
}

// Prompt is the section the system prompt carries, or "" for no files.
func Prompt(files []File) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("The project keeps instructions for coding agents, below. Follow them. " +
		"A later file is nearer the working directory and wins where two disagree.\n")
	for _, f := range files {
		fmt.Fprintf(&b, "\n<instructions path=%q>\n%s\n", f.Path, strings.TrimRight(f.Text, "\n"))
		if f.Truncated {
			fmt.Fprintf(&b, "[kept the first %d bytes, read the file for the rest]\n", len(f.Text))
		}
		b.WriteString("</instructions>\n")
	}
	return b.String()
}

// Paths names the files, for whatever reports what the prompt carries.
func Paths(files []File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

// firstOf reads the first of names in dir, named relative to from.
func firstOf(dir, from, within string) (File, bool, error) {
	for _, name := range names {
		p := filepath.Join(dir, name)
		rel, err := filepath.Rel(from, p)
		if err != nil {
			rel = p
		}
		if f, ok, err := read(p, rel, within); ok || err != nil {
			return f, ok, err
		}
	}
	return File{}, false, nil
}

// read returns the file at p, ok false when it is missing or not a regular file,
// and an error when within is set and p resolves outside it.
func read(p, name, within string) (File, bool, error) {
	info, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, err
	}
	if !info.Mode().IsRegular() {
		return File{}, false, nil
	}
	if within != "" {
		if in, err := gitroot.Contains(within, p); err != nil || !in {
			return File{}, false, fmt.Errorf("%s links outside the repository, so it was not read", p)
		}
	}
	f, err := os.Open(p)
	if err != nil {
		return File{}, false, err
	}
	defer func() { _ = f.Close() }()
	// One byte over the cap is enough for Find to know it cut something.
	text, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return File{}, false, err
	}
	return File{Path: name, Text: string(text)}, true, nil
}

// tilde shortens a path under the home directory, for the prompt and /status.
func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// cut keeps at most n bytes, ending on a whole line where there is one
// and on a whole character where there is not.
func cut(s string, n int) string {
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	s = s[:n]
	if i := strings.LastIndexByte(s, '\n'); i > 0 {
		return s[:i+1]
	}
	return s
}
