package skills

import (
	"bytes"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// shipped holds the skills detent comes with, one folder each.
//
//go:embed builtin
var shipped embed.FS

// BuiltinDir is where the shipped skills are written, beside detent's other state.
func BuiltinDir(home string) string {
	return filepath.Join(home, ".local", "state", "detent", "skills")
}

// Builtin writes the shipped skills under dir as files the tool can read, and
// returns their root, searched last so the human's own of a name wins.
func Builtin(dir string) (Root, error) {
	err := fs.WalkDir(shipped, "builtin", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(p, "builtin")))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o700)
		}
		want, err := shipped.ReadFile(p)
		if err != nil {
			return err
		}
		// Rewritten only when this build ships something different.
		if have, err := os.ReadFile(dest); err == nil && bytes.Equal(have, want) {
			return nil
		}
		return os.WriteFile(dest, want, 0o600)
	})
	return Root{Dir: dir, Builtin: true}, err
}
