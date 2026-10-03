package tool

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parity runs a tool as the sandbox would (its sh command) and as the host does
// (Run) in one directory, since the model reads whichever ran.
func parity(t *testing.T, tl Native, args Args, files map[string]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the sh side needs a POSIX shell")
	}
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	t.Chdir(dir)

	cmd, err := tl.Lower(args)
	require.NoError(t, err)
	sh := exec.CommandContext(t.Context(), "sh", "-c", cmd)
	var stdout strings.Builder
	sh.Stdout = &stdout
	code := 0
	if err := sh.Run(); err != nil {
		var exit *exec.ExitError
		require.ErrorAs(t, err, &exit)
		code = exit.ExitCode()
	}

	got := tl.Run(t.Context(), args)
	assert.Equal(t, stdout.String(), got.Stdout, "%s %v prints what the sandbox would", tl.Name(), args)
	assert.Equal(t, code, got.ExitCode, "%s %v exits as the sandbox would", tl.Name(), args)
}

func TestReadFile_NativeMatchesTheSandbox(t *testing.T) {
	long := strings.Repeat("x", outputBudget+50)
	many := strings.Repeat("a line of text\n", 900)
	for name, c := range map[string]struct {
		args  Args
		files map[string]string
	}{
		"a short file":        {Args{"path": "a.txt"}, map[string]string{"a.txt": "one\ntwo\n"}},
		"no final newline":    {Args{"path": "a.txt"}, map[string]string{"a.txt": "one\ntwo"}},
		"an empty file":       {Args{"path": "a.txt"}, map[string]string{"a.txt": ""}},
		"an offset past it":   {Args{"path": "a.txt", "offset": 9}, map[string]string{"a.txt": "one\ntwo\n"}},
		"a window":            {Args{"path": "a.txt", "offset": 2, "max_lines": 2}, map[string]string{"a.txt": "1\n2\n3\n4\n5\n"}},
		"the byte budget":     {Args{"path": "a.txt"}, map[string]string{"a.txt": many}},
		"a line over budget":  {Args{"path": "a.txt"}, map[string]string{"a.txt": "short\n" + long + "\nafter\n"}},
		"a path awk misreads": {Args{"path": "year=2024.csv"}, map[string]string{"year=2024.csv": "a,b\n"}},
	} {
		t.Run(name, func(t *testing.T) { parity(t, ReadFile{}, c.args, c.files) })
	}
}

// A file that is not there fails both ways, with a reason on stderr.
func TestReadFile_NativeSaysWhyItCouldNotRead(t *testing.T) {
	t.Chdir(t.TempDir())
	got := ReadFile{}.Run(t.Context(), Args{"path": "missing.txt"})
	assert.NotZero(t, got.ExitCode)
	assert.Contains(t, got.Stderr, "missing.txt")
}

// A Windows file's line endings do not reach the model as stray \r.
func TestReadFile_NativeDropsCarriageReturns(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("a.txt", []byte("one\r\ntwo\r\n"), 0o600))
	got := ReadFile{}.Run(t.Context(), Args{"path": "a.txt"})
	assert.Equal(t, "one\ntwo\n", got.Stdout)
}
