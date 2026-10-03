package tool

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEditFile_NativeMatchesTheSandbox(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("the sh side needs perl")
	}
	lines := func(n int, format string) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, format, i)
		}
		return b.String()
	}
	edit := func(p, old, repl string) Args { return Args{"path": p, "old_string": old, "new_string": repl} }
	all := func(a Args) Args { a["replace_all"] = true; return a }
	code := "func main() {\n\tfmt.Println(\"hi\")\n}\n"
	for name, c := range map[string]struct {
		args  Args
		files map[string]string
	}{
		"one match":         {edit("a.go", `"hi"`, `"hello"`), map[string]string{"a.go": code}},
		"no match":          {edit("a.go", "absent", "x"), map[string]string{"a.go": code}},
		"two matches":       {edit("a.txt", "x", "y"), map[string]string{"a.txt": "x\nmid\nx\n"}},
		"replace_all":       {all(edit("a.txt", "x", "y")), map[string]string{"a.txt": "x\nmid\nx\nx\n"}},
		"replace_all, once": {all(edit("a.txt", "mid", "MID")), map[string]string{"a.txt": "x\nmid\nx\n"}},
		"overlapping text":  {all(edit("a.txt", "aa", "b")), map[string]string{"a.txt": "aaa\n"}},
		"a missing file":    {edit("missing.txt", "a", "b"), nil},
		"an empty file":     {edit("a.txt", "a", "b"), map[string]string{"a.txt": ""}},
		"several lines": {edit("a.go", "func main() {\n\tfmt.Println(\"hi\")\n", "func main() {\n\tx := 1\n\tfmt.Println(x)\n"),
			map[string]string{"a.go": code}},
		"deleting lines":          {edit("a.txt", "two\n", ""), map[string]string{"a.txt": "one\ntwo\nthree\n"}},
		"losing the last newline": {edit("a.txt", "two\n", "two"), map[string]string{"a.txt": "one\ntwo\n"}},
		"appending at the end":    {edit("a.txt", "two", "two\nthree"), map[string]string{"a.txt": "one\ntwo"}},
		"unicode":                 {edit("é.txt", "naïve", "naïf ✓"), map[string]string{"é.txt": "a naïve 日本語\n"}},
		"spaces in the name":      {edit("my file.txt", "a", "b"), map[string]string{"my file.txt": "a\n"}},
		"a leading dash":          {edit("-a.txt", "a", "b"), map[string]string{"-a.txt": "a\n"}},
		"the heredocs' delimiters": {edit("a.txt", "DETENT_OLD\n", "DETENT_NEW\n"),
			map[string]string{"a.txt": "x\nDETENT_OLD\ny\n"}},
		"inside a line": {edit("a.txt", "middle", "centre"), map[string]string{"a.txt": "the middle part\n"}},
		"a binary file": {edit("a.bin", "b", "c"), map[string]string{"a.bin": "a\x00b\n"}},
		"a diff past the window": {all(edit("a.txt", "old", "new")),
			map[string]string{"a.txt": lines(300, "old %d\n")}},
		"far apart changes": {all(edit("a.txt", "x", "y")),
			map[string]string{"a.txt": "x\n" + lines(20, "l%d\n") + "x\n"}},
	} {
		t.Run(name, func(t *testing.T) { parityWriting(t, EditFile{}, c.args, c.files) })
	}
}

// A CRLF file keeps its CRLFs. The diff drops each \r, as read_file does,
// where the sandbox's awk passes them through.
func TestEditFile_NativeKeepsCRLF(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("the sh side needs perl")
	}
	args := Args{"path": "a.txt", "old_string": "two\r\n", "new_string": "TWO\r\nmore\r\n"}
	files := map[string]string{"a.txt": "one\r\ntwo\r\nthree\r\n"}
	stdout, code := runLowered(t, tree(t, files), EditFile{}, args)

	t.Chdir(tree(t, files))
	got := EditFile{}.Run(t.Context(), args)
	assert.Equal(t, strings.ReplaceAll(stdout, "\r", ""), got.Stdout)
	assert.Equal(t, code, got.ExitCode)
	b, err := os.ReadFile("a.txt")
	require.NoError(t, err)
	assert.Equal(t, "one\r\nTWO\r\nmore\r\nthree\r\n", string(b))
}

func TestEditFile_NativeRefusesWhatLowerRefuses(t *testing.T) {
	for name, args := range map[string]Args{
		"no path":       {"path": "", "old_string": "a", "new_string": "b"},
		"no old_string": {"path": "a.txt", "old_string": "", "new_string": "b"},
		"no change":     {"path": "a.txt", "old_string": "a", "new_string": "a"},
	} {
		_, err := EditFile{}.Lower(args)
		require.Error(t, err, name)
		got := EditFile{}.Run(t.Context(), args)
		assert.Equal(t, 2, got.ExitCode, name)
		assert.Contains(t, got.Stderr, err.Error(), name)
	}
}

// read_file shows a CRLF file without its \r, so the model's old_string has
// none. The edit must still find it, and keep the file's CRLF endings.
func TestEditFile_NativeEditsACRLFFileFromWhatReadFileShowed(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("win.txt", []byte("alpha\r\nbeta\r\ngamma\r\n"), 0o600))

	shown := ReadFile{}.Run(t.Context(), Args{"path": "win.txt"}).Stdout
	require.Equal(t, "alpha\nbeta\ngamma\n", shown, "what the model reads")

	got := EditFile{}.Run(t.Context(), Args{"path": "win.txt", "old_string": "alpha\nbeta\n", "new_string": "alpha\nBETA\n"})
	require.Zero(t, got.ExitCode, got.Stderr)
	b, err := os.ReadFile("win.txt")
	require.NoError(t, err)
	assert.Equal(t, "alpha\r\nBETA\r\ngamma\r\n", string(b), "the file keeps CRLF, new text included")
}
