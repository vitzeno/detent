package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The heredoc has to survive content that attacks it, so this runs the
// command: what is under test is what `sh` does with it.
func TestWriteFile_RoundTripsThroughARealShell(t *testing.T) {
	posixOnly(t, "Git Bash rewrites backslashes and CRLF")
	bodies := map[string]string{
		"plain":                "hello\nworld\n",
		"no trailing newline":  "no newline at the end",
		"one byte":             "x",
		"blank lines at end":   "a\n\n\n",
		"crlf without final":   "a\r\nb\r",
		"the delimiter itself": "before\nDETENT_EOF\nafter\n",
		"every fallback too":   "DETENT_EOF\nDETENT_EOF_0\nDETENT_EOF_1\ndone\n",
		"shell expansions":     "$(whoami) `id` ${HOME} $PATH\n",
		"quotes":               `single ' double " backtick ` + "`\n",
		"backslashes":          `C:\path\to\thing \n \\ \$\n`,
		"empty":                "",
		"unicode":              "héllo 世界 🎉\n",
		"long line":            strings.Repeat("x", 10000) + "\n",
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "out.txt")

			cmd, err := WriteFile{}.Lower(Args{"path": path, "content": body})
			require.NoError(t, err)

			out, err := exec.CommandContext(t.Context(), "sh", "-c", cmd).CombinedOutput()
			require.NoError(t, err, "shell said: %s", out)

			got, err := os.ReadFile(path)
			require.NoError(t, err)

			assert.Equal(t, body, string(got))
		})
	}
}

func TestWriteFile_QuotesHostilePaths(t *testing.T) {
	posixOnly(t, "names like *.txt are not legal paths on Windows")
	dir := t.TempDir()
	for _, name := range []string{"a b.txt", "$(touch pwned).txt", "it's.txt", "*.txt"} {
		path := filepath.Join(dir, name)
		cmd, err := WriteFile{}.Lower(Args{"path": path, "content": "ok\n"})
		require.NoError(t, err)

		out, err := exec.CommandContext(t.Context(), "sh", "-c", cmd).CombinedOutput()
		require.NoError(t, err, "shell said: %s", out)

		got, err := os.ReadFile(path)
		require.NoError(t, err, "the literal path %q must be what was written", name)
		assert.Equal(t, "ok\n", string(got))
	}
	_, err := os.Stat(filepath.Join(dir, "pwned"))
	assert.True(t, os.IsNotExist(err), "a path must never be evaluated by the shell")
}

func TestHeredoc_GrowsTheDelimiterUntilItIsSafe(t *testing.T) {
	body := "DETENT_EOF\nDETENT_EOF_0\n"
	cmd := heredoc("f", body)
	assert.Contains(t, cmd, "<<'DETENT_EOF_1'")

	// A delimiter appearing mid-line cannot end a heredoc, so it must
	// not force a rename either.
	assert.Contains(t, heredoc("f", "x DETENT_EOF y\n"), "<<'DETENT_EOF'")
}

func TestWriteFile_ShowsWhatChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	write := func(body string) (string, error) {
		cmd, err := WriteFile{}.Lower(Args{"path": path, "content": body})
		require.NoError(t, err)
		out, err := exec.CommandContext(t.Context(), "sh", "-c", cmd).CombinedOutput()
		return string(out), err
	}

	out, err := write("one\ntwo\n")
	require.NoError(t, err, out)
	assert.Contains(t, out, "--- "+path+"\n+++ "+path+"\n@@ -0,0 +1,2 @@\n+one\n+two\n", "a new file is shown as additions")

	out, err = write("one\n2\n")
	require.NoError(t, err, out)
	assert.Contains(t, out, "-two\n+2\n", "an existing one is a diff")

	for _, body := range []string{"x\n", "x"} {
		cmd, err := WriteFile{}.Lower(Args{"path": filepath.Join(dir, "missing", "f.txt"), "content": body})
		require.NoError(t, err)
		_, err = exec.CommandContext(t.Context(), "sh", "-c", cmd).CombinedOutput()
		assert.Error(t, err, "a write that failed must not exit 0")
	}
}

// existed is the command's own variable, so one inherited from the environment means nothing.
func TestWriteFile_IgnoresAnInheritedExisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	cmd, err := WriteFile{}.Lower(Args{"path": path, "content": "one\n"})
	require.NoError(t, err)
	c := exec.CommandContext(t.Context(), "sh", "-c", cmd)
	c.Env = append(os.Environ(), "existed=1")
	out, err := c.CombinedOutput()
	require.NoError(t, err, string(out))
	assert.Equal(t, "--- "+path+"\n+++ "+path+"\n@@ -0,0 +1 @@\n+one\n", string(out))
}
