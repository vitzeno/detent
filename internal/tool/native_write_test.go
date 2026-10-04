package tool

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteFile_NativeMatchesTheSandbox(t *testing.T) {
	numbered := func(from, to int, format string) string {
		var b strings.Builder
		for i := from; i < to; i++ {
			fmt.Fprintf(&b, format, i)
		}
		return b.String()
	}
	for name, c := range map[string]struct {
		args  Args
		files map[string]string
	}{
		"a new file":             {Args{"path": "a.txt", "content": "one\ntwo\n"}, nil},
		"a new file, no newline": {Args{"path": "a.txt", "content": "one\ntwo"}, nil},
		"a new empty file":       {Args{"path": "a.txt", "content": ""}, nil},
		"a new file in a dir":    {Args{"path": "sub/a.txt", "content": "x\n"}, map[string]string{"sub/keep": ""}},
		"an overwrite":           {Args{"path": "a.txt", "content": "one\nTWO\nthree\n"}, map[string]string{"a.txt": "one\ntwo\nthree\n"}},
		"no change":              {Args{"path": "a.txt", "content": "same\n"}, map[string]string{"a.txt": "same\n"}},
		"emptied":                {Args{"path": "a.txt", "content": ""}, map[string]string{"a.txt": "one\ntwo\n"}},
		"filled from empty":      {Args{"path": "a.txt", "content": "one\n"}, map[string]string{"a.txt": ""}},
		"losing the last newline": {Args{"path": "a.txt", "content": "one\ntwo"},
			map[string]string{"a.txt": "one\ntwo\n"}},
		"gaining the last newline": {Args{"path": "a.txt", "content": "one\ntwo\n"},
			map[string]string{"a.txt": "one\ntwo"}},
		"unicode": {Args{"path": "é.txt", "content": "naïve ✓\n日本語\n"},
			map[string]string{"é.txt": "naive\n日本語\n"}},
		"spaces in the name": {Args{"path": "my file.txt", "content": "b\n"}, map[string]string{"my file.txt": "a\n"}},
		"a leading dash":     {Args{"path": "-a.txt", "content": "b\n"}, map[string]string{"-a.txt": "a\n"}},
		"a path awk misreads": {Args{"path": "year=2024.csv", "content": "a,c\n"},
			map[string]string{"year=2024.csv": "a,b\n"}},
		"the heredoc's own delimiter": {Args{"path": "a.txt", "content": "x\nDETENT_EOF\ny\n"},
			map[string]string{"a.txt": "x\ny\n"}},
		"two hunks": {Args{"path": "a.txt", "content": strings.Replace(strings.Replace(numbered(0, 40, "l%d\n"), "l3\n", "L3\n", 1), "l30\n", "L30\n", 1)},
			map[string]string{"a.txt": numbered(0, 40, "l%d\n")}},
		"hunks close enough to merge": {Args{"path": "a.txt", "content": strings.Replace(strings.Replace(numbered(0, 40, "l%d\n"), "l10\n", "L10\n", 1), "l17\n", "L17\n", 1)},
			map[string]string{"a.txt": numbered(0, 40, "l%d\n")}},
		"a diff past the window": {Args{"path": "a.txt", "content": numbered(0, 300, "new %d\n")},
			map[string]string{"a.txt": numbered(0, 300, "old %d\n")}},
		"a diff past the byte budget": {Args{"path": "a.txt", "content": numbered(0, 150, "a much longer replacement line, number %d\n")},
			map[string]string{"a.txt": numbered(0, 150, "an original line that is fairly long, number %d\n")}},
	} {
		t.Run(name, func(t *testing.T) {
			parityWriting(t, WriteFile{}, c.args, c.files)
			got, err := os.ReadFile(c.args.String("path"))
			require.NoError(t, err)
			assert.Equal(t, c.args.String("content"), string(got), "the file holds the content byte for byte")
		})
	}
}

func TestWriteFile_NativeShowsCreatedFileAsDiff(t *testing.T) {
	t.Chdir(t.TempDir())
	got := WriteFile{}.Run(t.Context(), Args{"path": "f.txt", "content": "one\ntwo\n"})
	require.Zero(t, got.ExitCode, got.Stderr)
	assert.Equal(t, "--- f.txt\n+++ f.txt\n@@ -0,0 +1,2 @@\n+one\n+two\n", got.Stdout)
}

// A missing directory is not made. Both fail, but the status is the shell's:
// dash, the sandbox's, says 2 and bash says 1.
func TestWriteFile_NativeDoesNotMakeDirectories(t *testing.T) {
	args := Args{"path": "no/such/a.txt", "content": "x\n"}
	t.Chdir(t.TempDir())
	got := WriteFile{}.Run(t.Context(), args)
	assert.Equal(t, 2, got.ExitCode)
	assert.Empty(t, got.Stdout)
	assert.Contains(t, got.Stderr, "no/such/a.txt")
	assert.NoDirExists(t, "no")

	if runtime.GOOS == "windows" {
		return
	}
	cmd, err := WriteFile{}.Lower(args)
	require.NoError(t, err)
	out, err := exec.CommandContext(t.Context(), "sh", "-c", cmd).Output()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	assert.Empty(t, string(out))
	assert.NoDirExists(t, "no")
}

func TestWriteFile_NativeRefusesAnEmptyPath(t *testing.T) {
	got := WriteFile{}.Run(t.Context(), Args{"path": "", "content": "x"})
	assert.Equal(t, 2, got.ExitCode)
	assert.Contains(t, got.Stderr, "path must not be empty")
}

// An ambiguous change sits where GNU diff, the sandbox's, puts it. macOS's BSD
// diff can choose otherwise, so the parity cases avoid ambiguous changes.
func TestUnifiedDiff_ReadsAsGNUDiff(t *testing.T) {
	for name, c := range map[string]struct{ before, after, want string }{
		"a repeated line goes last": {"a\n}\n}\n}\n", "a\n}\n}\n",
			"--- p\n+++ p\n@@ -1,4 +1,3 @@\n a\n }\n }\n-}\n"},
		"a change pairs with the line it replaces": {"x\n}\n}\ny\n", "x\n\n}\ny\n",
			"--- p\n+++ p\n@@ -1,4 +1,4 @@\n x\n-}\n+\n }\n y\n"},
		"a repeated block goes last": {"k\na\nb\na\nb\nk\n", "k\na\nb\nk\n",
			"--- p\n+++ p\n@@ -1,6 +1,4 @@\n k\n a\n b\n-a\n-b\n k\n"},
		"from empty":   {"", "one\n", "--- p\n+++ p\n@@ -0,0 +1 @@\n+one\n"},
		"to empty":     {"one\n", "", "--- p\n+++ p\n@@ -1 +0,0 @@\n-one\n"},
		"one line on":  {"a\n", "a\nb\n", "--- p\n+++ p\n@@ -1 +1,2 @@\n a\n+b\n"},
		"no newline":   {"a\n", "a", "--- p\n+++ p\n@@ -1 +1 @@\n-a\n+a\n\\ No newline at end of file\n"},
		"the same":     {"a\n", "a\n", ""},
		"binary files": {"a\x00b", "a\x00c", "Binary files p and p differ\n"},
	} {
		assert.Equal(t, c.want, unifiedDiff("p", c.before, c.after), name)
	}
}

// parityWriting is parity for a tool that changes files: each side starts from
// its own copy and must leave the same bytes. It ends in the native side's dir.
func parityWriting(t *testing.T, tl Native, args Args, files map[string]string) {
	t.Helper()
	shDir, nativeDir := tree(t, files), tree(t, files)
	stdout, code := runLowered(t, shDir, tl, args)

	t.Chdir(nativeDir)
	got := tl.Run(t.Context(), args)
	assert.Equal(t, stdout, got.Stdout, "%s %v prints what the sandbox would", tl.Name(), args)
	assert.Equal(t, code, got.ExitCode, "%s %v exits as the sandbox would", tl.Name(), args)
	assert.Equal(t, filesUnder(t, shDir), filesUnder(t, nativeDir), "%s %v leaves what the sandbox would", tl.Name(), args)
}

// runLowered runs what tl lowers args to in dir, as the sandbox would.
func runLowered(t *testing.T, dir string, tl Tool, args Args) (stdout string, code int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the sh side needs a POSIX shell")
	}
	cmd, err := tl.Lower(args)
	require.NoError(t, err)
	sh := exec.CommandContext(t.Context(), "sh", "-c", cmd)
	sh.Dir = dir
	out, err := sh.Output()
	if err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit)
		code = exit.ExitCode()
	}
	return string(out), code
}

// filesUnder is every file under dir and what it holds.
func filesUnder(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		files[rel] = string(b)
		return err
	}))
	return files
}

// Rewriting a CRLF file from what read_file showed keeps it CRLF, while a mixed
// file or content that chose its own endings is written as given.
func TestWriteFile_NativeKeepsACRLFFileCRLF(t *testing.T) {
	for name, c := range map[string]struct{ before, content, want string }{
		"all CRLF, model wrote LF": {"a\r\nb\r\n", "a\nB\n", "a\r\nB\r\n"},
		"mixed endings":            {"a\r\nb\n", "a\nB\n", "a\nB\n"},
		"content chose CRLF":       {"a\r\nb\r\n", "a\r\nB\n", "a\r\nB\n"},
		"an LF file":               {"a\nb\n", "a\nB\n", "a\nB\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			require.NoError(t, os.WriteFile("f.txt", []byte(c.before), 0o600))
			got := WriteFile{}.Run(t.Context(), Args{"path": "f.txt", "content": c.content})
			require.Zero(t, got.ExitCode, got.Stderr)
			b, err := os.ReadFile("f.txt")
			require.NoError(t, err)
			assert.Equal(t, c.want, string(b))
		})
	}
}
