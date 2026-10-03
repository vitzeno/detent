// Package gitroot walks a directory up to its git root and says whether a
// path, links followed, stays under it. Instructions and skills share it.
package gitroot

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Dirs is dir and each parent up to the one holding .git, nearest first.
// Outside a repository it is dir alone.
func Dirs(dir string) []string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
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

// Contains reports whether p, links followed, is dir or under it. A link to nothing
// is not inside, and nothing there at all is fs.ErrNotExist, for the caller to decide.
func Contains(dir, p string) (bool, error) {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		if _, lerr := os.Lstat(p); errors.Is(lerr, fs.ErrNotExist) {
			return false, fs.ErrNotExist
		}
		return false, nil
	}
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	rel, err := filepath.Rel(dir, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}
