package gitroot

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContains(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need developer mode on Windows")
	}
	base := t.TempDir()
	root, outside := filepath.Join(base, "repo"), filepath.Join(base, "elsewhere")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "a.md"), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(root, "sub", "a.md"), filepath.Join(root, "in-link")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "out-link")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "gone"), filepath.Join(root, "dangling")))
	// A sibling sharing the root's name as a prefix is not inside it.
	require.NoError(t, os.MkdirAll(root+"2", 0o755))

	for name, c := range map[string]struct {
		p       string
		in      bool
		missing bool
	}{
		"the root itself":              {p: root, in: true},
		"a file under it":              {p: filepath.Join(root, "sub", "a.md"), in: true},
		"a link that stays inside":     {p: filepath.Join(root, "in-link"), in: true},
		"a link that leaves":           {p: filepath.Join(root, "out-link")},
		"a link to nothing":            {p: filepath.Join(root, "dangling")},
		"a sibling sharing its prefix": {p: root + "2"},
		"nothing there at all":         {p: filepath.Join(root, "absent"), missing: true},
	} {
		t.Run(name, func(t *testing.T) {
			in, err := Contains(root, c.p)
			assert.Equal(t, c.in, in)
			if c.missing {
				assert.ErrorIs(t, err, fs.ErrNotExist)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDirs_WalksUpToTheRepositoryRoot(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(deep, 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	root, err := filepath.EvalSymlinks(root) // macOS's /var is a link to /private/var
	require.NoError(t, err)
	deep = filepath.Join(root, "a", "b")

	assert.Equal(t, []string{deep, filepath.Join(root, "a"), root}, Dirs(deep))
}

func TestDirs_OutsideARepositoryIsTheDirectoryAlone(t *testing.T) {
	dir := t.TempDir()
	got := Dirs(dir)
	require.Len(t, got, 1)
	assert.Equal(t, filepath.Clean(dir), got[0])
}
