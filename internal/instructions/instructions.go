// Package instructions finds the files a project keeps for coding
// agents, AGENTS.md or CLAUDE.md, and the human's own for every project.
package instructions

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Names are tried in order in each directory, and only the first found is read.
var Names = []string{"AGENTS.md", "CLAUDE.md"}

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

// Find reads global, then one file per directory from dir's repository root
// down to dir, nearest last. Outside a repository only dir is read.
func Find(dir, global string) ([]File, error) {
	var found []*File
	dirs := upToRoot(dir)
	// Nearest first, so the file that matters most is the last to be cut.
	for _, d := range dirs {
		f, err := firstOf(d, dir)
		if err != nil {
			return nil, err
		}
		found = append(found, f)
	}
	if global != "" && !slices.Contains(dirs, filepath.Dir(global)) {
		f, err := read(global, tilde(global))
		if err != nil {
			return nil, err
		}
		found = append(found, f)
	}

	var out []File
	budget := MaxBytes
	for _, f := range found {
		if f == nil || strings.TrimSpace(f.Text) == "" {
			continue
		}
		if budget <= 0 {
			break
		}
		if len(f.Text) > budget {
			f.Text, f.Truncated = cut(f.Text, budget), true
		}
		budget -= len(f.Text)
		out = append([]File{*f}, out...)
	}
	return out, nil
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
			fmt.Fprintf(&b, "[cut at %d bytes, read the file for the rest]\n", len(f.Text))
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

// firstOf reads the first of Names in dir, named relative to from.
func firstOf(dir, from string) (*File, error) {
	for _, name := range Names {
		p := filepath.Join(dir, name)
		rel, err := filepath.Rel(from, p)
		if err != nil {
			rel = p
		}
		f, err := read(p, rel)
		if f != nil || err != nil {
			return f, err
		}
	}
	return nil, nil
}

// read returns the file at p, or nil when it is missing, not a regular
// file, or empty. An empty AGENTS.md still hides a CLAUDE.md beside it.
func read(p, name string) (*File, error) {
	info, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}
	text, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return &File{Path: name, Text: string(text)}, nil
}

// tilde shortens a path under the home directory, for the prompt and /status.
func tilde(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// upToRoot is dir and each parent up to the one holding .git, nearest first.
func upToRoot(dir string) []string {
	var dirs []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		dirs = append(dirs, d)
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return dirs
		}
		if filepath.Dir(d) == d {
			return dirs[:1]
		}
	}
}

// cut keeps at most n bytes, ending on a whole line where there is one.
func cut(s string, n int) string {
	s = s[:n]
	if i := strings.LastIndexByte(s, '\n'); i > 0 {
		return s[:i+1]
	}
	return s
}
