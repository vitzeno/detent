package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Like write_file's, this runs the command, since what is under test is
// what sh and perl do with it.
func TestEditFile_RoundTripsThroughARealShell(t *testing.T) {
	cases := map[string]struct {
		file, old, repl string
		all             bool
		want            string
	}{
		"one line":             {file: "a\nb\nc\n", old: "b", repl: "B", want: "a\nB\nc\n"},
		"across lines":         {file: "a\nb\nc\n", old: "a\nb\n", repl: "x\n", want: "x\nc\n"},
		"deleting":             {file: "keep\ndrop\n", old: "drop\n", repl: "", want: "keep\n"},
		"no trailing newline":  {file: "end", old: "end", repl: "fin", want: "fin"},
		"crlf kept":            {file: "a\r\nb\r\n", old: "a", repl: "z", want: "z\r\nb\r\n"},
		"shell expansions":     {file: "x\n", old: "x", repl: "$(whoami) `id` ${HOME} '\"\\\n", want: "$(whoami) `id` ${HOME} '\"\\\n\n"},
		"regex characters":     {file: "a.*b\n", old: ".*", repl: "$1", want: "a$1b\n"},
		"the delimiter itself": {file: "x\n", old: "x", repl: "DETENT_NEW\nDETENT_OLD", want: "DETENT_NEW\nDETENT_OLD\n"},
		"replace all":          {file: "aXbXc", old: "X", repl: "-", all: true, want: "a-b-c"},
		"unicode":              {file: "héllo 世界\n", old: "世界", repl: "🎉", want: "héllo 🎉\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f.txt")
			require.NoError(t, os.WriteFile(path, []byte(tc.file), 0o644))

			out, err := runEdit(t, Args{"path": path, "old_string": tc.old, "new_string": tc.repl, "replace_all": tc.all})
			require.NoError(t, err, "shell said: %s", out)
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(got))
		})
	}
}

func TestEditFile_RefusesAndLeavesTheFileAlone(t *testing.T) {
	for name, tc := range map[string]struct{ old, says string }{
		"not there": {"missing", "is not in"},
		"ambiguous": {"x", "2 times"},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f.txt")
			require.NoError(t, os.WriteFile(path, []byte("x\nx\n"), 0o644))

			out, err := runEdit(t, Args{"path": path, "old_string": tc.old, "new_string": "y"})
			require.Error(t, err)
			assert.Contains(t, string(out), tc.says)
			got, _ := os.ReadFile(path)
			assert.Equal(t, "x\nx\n", string(got))
		})
	}
}

func TestEditFile_RejectsWhatCouldNeverWork(t *testing.T) {
	for name, a := range map[string]Args{
		"no path":   {"path": "", "old_string": "a", "new_string": "b"},
		"empty old": {"path": "f", "old_string": "", "new_string": "b"},
		"no change": {"path": "f", "old_string": "a", "new_string": "a"},
	} {
		_, err := EditFile{}.Lower(a)
		assert.Error(t, err, name)
	}
}

// The change comes back as a diff, so the model sees where it landed and
// the human sees it side by side.
func TestEditFile_ShowsWhatChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.go")
	require.NoError(t, os.WriteFile(path, []byte("a\nb\nc\n"), 0o644))
	out, err := runEdit(t, Args{"path": path, "old_string": "b", "new_string": "B"})
	require.NoError(t, err, string(out))
	assert.Contains(t, string(out), "--- "+path+"\n+++ "+path+"\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n")
}

func runEdit(t *testing.T, a Args) ([]byte, error) {
	t.Helper()
	cmd, err := EditFile{}.Lower(a)
	require.NoError(t, err)
	return exec.Command("sh", "-c", cmd).CombinedOutput()
}
