package tool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capture"
)

// The footer only survives if the window stops before the capture does.
func TestWindow_StopsBeforeTheCaptureCap(t *testing.T) {
	assert.Less(t, outputBudget+512, capture.MaxOutputBytes)
}

func TestReadFile_ReadsAWindowAndSaysWhereToGoOn(t *testing.T) {
	dir := tree(t, map[string]string{"f.txt": numbered(10)})
	f := filepath.Join(dir, "f.txt")

	assert.Equal(t, "3\n4\n[6 more lines, read on with offset 5]\n",
		run(t, ReadFile{}, Args{"path": f, "offset": 3, "max_lines": 2}))
	assert.Equal(t, "9\n10\n", run(t, ReadFile{}, Args{"path": f, "offset": 9}))
	assert.Equal(t, "[the file has 10 lines]\n", run(t, ReadFile{}, Args{"path": f, "offset": 50}))
}

// The footer has to survive the capture cap, or the model cannot tell
// where a long file was cut.
func TestReadFile_StopsUnderTheCaptureCap(t *testing.T) {
	line := strings.Repeat("x", 99)
	f := filepath.Join(tree(t, map[string]string{"big.txt": strings.Repeat(line+"\n", 500)}), "big.txt")

	out := run(t, ReadFile{}, Args{"path": f})
	assert.Less(t, len(out), 8*1024)
	shown := strings.Count(out, line+"\n")
	assert.Contains(t, out, fmt.Sprintf("[%d more lines, read on with offset %d]", 500-shown, shown+1))

	long := filepath.Join(tree(t, map[string]string{"one.txt": strings.Repeat("y", 20000) + "\n"}), "one.txt")
	assert.Contains(t, run(t, ReadFile{}, Args{"path": long}), "[line cut]", "one huge line still shows something")
}

// awk reads a leading - as a flag and name=value as an assignment, and
// either way would report a real file as empty.
func TestReadFile_TakesAPathThatLooksLikeSomethingElse(t *testing.T) {
	for _, name := range []string{"-n.txt", "year=2024/d.csv", "a=b"} {
		dir := tree(t, map[string]string{name: "ok\n"})
		cmd, err := ReadFile{}.Lower(Args{"path": name})
		require.NoError(t, err)
		out, err := shIn(dir, cmd)
		require.NoError(t, err, out)
		assert.Equal(t, "ok\n", out, name)
	}
}

// A pipeline reports its last stage, which used to turn a failed search into "no matches".
func TestSearch_FailsWhenTheSearchDoes(t *testing.T) {
	dir := tree(t, map[string]string{"f.txt": "x\n"})
	for name, c := range map[string]struct {
		tool Tool
		args Args
	}{
		"grep a missing path": {Grep{}, Args{"pattern": "x", "path": filepath.Join(dir, "nope")}},
		"grep a bad regex":    {Grep{}, Args{"pattern": "(", "path": dir}},
		"find a missing path": {FindFiles{}, Args{"pattern": "*", "path": filepath.Join(dir, "nope")}},
	} {
		cmd, err := c.tool.Lower(c.args)
		require.NoError(t, err)
		_, err = shIn(dir, cmd)
		assert.Error(t, err, name)
	}
}

func TestGrep_FindsWhatItIsAskedFor(t *testing.T) {
	dir := tree(t, map[string]string{
		"a.go": "func Alpha() {}\nfunc beta() {}\n", "b.md": "Alpha in prose\n",
		".git/config": "Alpha\n", "bin.dat": "Alpha\x00\x01",
	})
	cases := map[string]struct {
		args Args
		want string
	}{
		"recursive":   {Args{"pattern": "Alpha", "path": dir}, "a.go:1:func Alpha() {}\n" + "b.md:1:Alpha in prose\n"},
		"include":     {Args{"pattern": "Alpha", "path": dir, "include": "*.go"}, "a.go:1:func Alpha() {}\n"},
		"ignore case": {Args{"pattern": "BETA", "path": dir, "ignore_case": true}, "a.go:2:func beta() {}\n"},
		"regex":       {Args{"pattern": "func [a-z]+", "path": dir}, "a.go:2:func beta() {}\n"},
		"none":        {Args{"pattern": "gamma", "path": dir}, "[no matches]\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, strings.ReplaceAll(run(t, Grep{}, tc.args), dir+"/", ""))
		})
	}
}

func TestGrep_CapsWhatItShows(t *testing.T) {
	dir := tree(t, map[string]string{"f.txt": strings.Repeat("hit\n", 30)})
	out := run(t, Grep{}, Args{"pattern": "hit", "path": dir, "max_results": 5})
	assert.Equal(t, 5, strings.Count(out, ":hit\n"))
	assert.Contains(t, out, "[25 more matches, narrow the pattern or the path]")
}

func TestFindFiles_MatchesNamesOrPaths(t *testing.T) {
	dir := tree(t, map[string]string{
		"z_test.go": "", "a_test.go": "", "main.go": "", "sub/b_test.go": "", ".git/x_test.go": "",
	})
	strip := func(s string) string { return strings.ReplaceAll(s, dir+"/", "") }

	assert.Equal(t, "a_test.go\nsub/b_test.go\nz_test.go\n", strip(run(t, FindFiles{}, Args{"pattern": "*_test.go", "path": dir})))
	assert.Equal(t, "sub/b_test.go\n", strip(run(t, FindFiles{}, Args{"pattern": "*/sub/*.go", "path": dir})))
	assert.Equal(t, "[no files match]\n", run(t, FindFiles{}, Args{"pattern": "*.rs", "path": dir}))
	assert.Equal(t, "a_test.go\n[2 more files, narrow the pattern or the path]\n",
		strip(run(t, FindFiles{}, Args{"pattern": "*_test.go", "path": dir, "max_results": 1})))
}

// A pattern the model chose reaches sh -c, so it must stay one literal.
func TestSearch_QuotesHostilePatterns(t *testing.T) {
	dir := tree(t, map[string]string{"f.txt": "x\n"})
	for _, p := range []string{"$(touch pwned)", "'; touch pwned; '", "`touch pwned`"} {
		grep, err := Grep{}.Lower(Args{"pattern": p, "include": p})
		require.NoError(t, err)
		find, err := FindFiles{}.Lower(Args{"pattern": p})
		require.NoError(t, err)
		_, _ = shIn(dir, grep)
		_, _ = shIn(dir, find)
	}
	_, err := os.Stat(filepath.Join(dir, "pwned"))
	assert.True(t, os.IsNotExist(err))
}

func TestSearch_RejectsWhatCouldNeverWork(t *testing.T) {
	for name, c := range map[string]struct {
		tool Tool
		args Args
	}{
		"read from line 0":  {ReadFile{}, Args{"path": "f", "offset": 0}},
		"grep for nothing":  {Grep{}, Args{"pattern": ""}},
		"grep zero results": {Grep{}, Args{"pattern": "x", "max_results": 0}},
		"find nothing":      {FindFiles{}, Args{"pattern": ""}},
	} {
		_, err := c.tool.Lower(c.args)
		assert.Error(t, err, name)
	}
}

// tree writes files under a temp directory, making parents as needed.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	return dir
}

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	return b.String()
}

func run(t *testing.T, tl Tool, a Args) string {
	t.Helper()
	cmd, err := tl.Lower(a)
	require.NoError(t, err)
	out, err := shIn("", cmd)
	require.NoError(t, err, out)
	return out
}

func shIn(dir, cmd string) (string, error) {
	c := exec.Command("sh", "-c", cmd)
	c.Dir = dir
	out, err := c.Output()
	return string(out), err
}
