package tool

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/internal/capture"
)

// A line with no end is counted, not held, and the read stops with its context.
func TestWindowLines_HoldsLittleOfALineThatNeverEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	r := &endless{}
	var err error
	start := time.Now()
	alloc := allocated(func() { _, err = windowLines(ctx, r, 1, 500, readMore, readEmpty) })
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second, "it stopped when its context did")
	assert.Greater(t, r.read, 1<<20, "it kept reading for a while")
	assert.Less(t, alloc, uint64(4<<20), "having read %d bytes", r.read)
}

// Keeping only lineKeep bytes of a line cuts exactly where holding it all did.
func TestWindowLines_CutsAtTheBudgetAsBefore(t *testing.T) {
	b, c := strings.Repeat("c", outputBudget), " [line cut]\n"
	for in, want := range map[string]string{
		b + "\r\n":        b + "\n",
		b + "\r":          b + "\n",
		b + "c\r\n":       b + c,
		b + "\r\r\n":      b + c,
		b + "cc":          b + c,
		"é" + b[2:] + "é": "é" + b[2:] + c,
	} {
		got, err := windowLines(t.Context(), strings.NewReader(in), 1, 10, readMore, readEmpty)
		require.NoError(t, err)
		assert.Equal(t, want, got, "%q", in[len(in)-4:])
	}
}

// A 20MB line with no newline is cut to the budget without being held whole.
func TestReadFile_CutsAHugeLineWithoutHoldingIt(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("min.json", bytes.Repeat([]byte("x"), 20<<20), 0o600))
	require.NoError(t, os.WriteFile("two.txt", append(bytes.Repeat([]byte("y"), 20<<20), "\nafter\n"...), 0o600))

	var got, two capture.Result
	alloc := allocated(func() {
		got = ReadFile{}.Run(t.Context(), Args{"path": "min.json"})
		two = ReadFile{}.Run(t.Context(), Args{"path": "two.txt"})
	})
	assert.Equal(t, strings.Repeat("x", outputBudget)+" [line cut]\n", got.Stdout)
	assert.Equal(t, strings.Repeat("y", outputBudget)+" [line cut]\n[1 more lines, read on with offset 2]\n", two.Stdout,
		"the cut line still counts as one")
	assert.Less(t, alloc, uint64(8<<20))
}

// A read, a search or a listing stops when its context does, with what it had.
func TestNative_StopsWhenItsContextDoes(t *testing.T) {
	files := map[string]string{"SKILL.md": "body\n"}
	for i := range 50 {
		files[fmt.Sprintf("d%d/f%d.txt", i%5, i)] = "hit\n"
	}
	t.Chdir(tree(t, files))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, c := range []struct {
		tl   Native
		args Args
	}{
		{ReadFile{}, Args{"path": "SKILL.md"}},
		{Grep{}, Args{"pattern": "hit"}},
		{FindFiles{}, Args{"pattern": "*.txt"}},
		{ListDir{}, Args{}},
		{Skill{Entries: []SkillEntry{{Name: "s", Dir: "."}}}, Args{"name": "s"}},
		{WriteFile{}, Args{"path": "SKILL.md", "content": "new\n"}},
		{EditFile{}, Args{"path": "SKILL.md", "old_string": "body", "new_string": "new"}},
	} {
		got := c.tl.Run(ctx, c.args)
		assert.Equal(t, 1, got.ExitCode, c.tl.Name())
		assert.Contains(t, got.Stderr, c.tl.Name()+": stopped: context canceled", c.tl.Name())
	}
	b, err := os.ReadFile("SKILL.md")
	require.NoError(t, err)
	assert.Equal(t, "body\n", string(b), "a stopped write writes nothing")
}

// A search through a big tree stops soon after its context does.
func TestGrep_StopsMidTree(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("a line that does not match\n", 2000)
	for i := range 400 {
		p := filepath.Join(dir, fmt.Sprintf("d%02d", i%20), fmt.Sprintf("f%03d.txt", i))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	start := time.Now()
	got := Grep{}.Run(ctx, Args{"pattern": "ma+tch(es)? [0-9]", "path": dir})
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Contains(t, got.Stderr, "grep: stopped: context deadline exceeded")
}

// grep matches a line far longer than a read as it streams past.
func TestGrep_MatchesAHugeLineWithoutHoldingIt(t *testing.T) {
	huge := strings.Repeat("x", 20<<20)
	t.Chdir(tree(t, map[string]string{"a.txt": huge + "needle\nshort needle\n", "b.txt": huge + "\x00needle\n"}))
	var got, short capture.Result
	alloc := allocated(func() {
		got = Grep{}.Run(t.Context(), Args{"pattern": "needle$"})
		short = Grep{}.Run(t.Context(), Args{"pattern": "^short"})
	})
	assert.Equal(t, "./a.txt:1:"+strings.Repeat("x", outputBudget-len("./a.txt:1:"))+" [line cut]\n"+
		"[1 more matches, narrow the pattern or the path]\n", got.Stdout, "b.txt is binary past its first read")
	assert.Equal(t, "./a.txt:2:short needle\n", short.Stdout, "the line after a huge one is numbered as the next")
	assert.Less(t, alloc, uint64(8<<20))
}

// windowTop over what top kept prints what windowOf prints over everything.
func TestTop_WindowsAsIfItHeldEverything(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for round := range 300 {
		n := rng.IntN(400)
		lines := make([]string, n)
		for i := range lines {
			lines[i] = fmt.Sprintf("%05d", rng.IntN(1000)) + strings.Repeat("y", rng.IntN([]int{40, 400, 9000}[round%3]))
			if rng.IntN(10) == 0 {
				lines[i] += "\r"
			}
		}
		limit := 1 + rng.IntN(300)
		kept := topLines(limit, strings.Compare)
		for _, l := range lines {
			kept.add(l)
		}
		sorted := slices.Sorted(slices.Values(lines))
		assert.Equal(t, windowOf(sorted, limit, grepMore, grepEmpty),
			windowTop(kept.kept, kept.total, limit, grepMore, grepEmpty), "round %d", round)
	}
}

// edit_file and write_file refuse a file too big to read whole, rather than read it.
func TestEditAndWrite_RefuseAHugeFile(t *testing.T) {
	t.Chdir(t.TempDir())
	f, err := os.Create("big.log")
	require.NoError(t, err)
	require.NoError(t, f.Truncate(maxEditBytes+1))
	require.NoError(t, f.Close())

	got := EditFile{}.Run(t.Context(), Args{"path": "big.log", "old_string": "a", "new_string": "b"})
	assert.NotZero(t, got.ExitCode)
	assert.Contains(t, got.Stderr, "larger than 50 MB, so change it with a command")
	got = WriteFile{}.Run(t.Context(), Args{"path": "big.log", "content": "small\n"})
	assert.Equal(t, 2, got.ExitCode)
	assert.Contains(t, got.Stderr, "larger than 50 MB")

	require.NoError(t, os.WriteFile("a.txt", bytes.Repeat([]byte("a"), 1<<20), 0o600))
	got = EditFile{}.Run(t.Context(), Args{"path": "a.txt", "old_string": "a", "new_string": strings.Repeat("b", 64), "replace_all": true})
	assert.Equal(t, 255, got.ExitCode)
	assert.Contains(t, got.Stderr, "would grow to 64 MB")
}

// A directory past the listing's limit still counts every entry.
func TestListDir_CountsEntriesPastItsLimit(t *testing.T) {
	dir := t.TempDir()
	for i := range listEntries + 100 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d", i)), nil, 0o600))
	}
	got := ListDir{}.Run(t.Context(), Args{"path": dir})
	lines := strings.Split(strings.TrimSuffix(got.Stdout, "\n"), "\n")
	require.NotEmpty(t, lines)
	shown := len(lines) - 1
	assert.Contains(t, lines[0], "f0000")
	assert.Equal(t, fmt.Sprintf("[%d more entries, list a narrower path]", listEntries+100-shown), lines[shown])
}

// endless is a reader that never ends and never sends a newline.
type endless struct{ read int }

func (e *endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	e.read += len(p)
	return len(p), nil
}

// allocated is how many bytes f allocates, give or take the runtime's own.
func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}
