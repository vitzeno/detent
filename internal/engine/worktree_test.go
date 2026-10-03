package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/internal/capture"
	"github.com/vitzeno/detent/internal/model"
	"github.com/vitzeno/detent/internal/worktree"
)

// dirRunner runs each command in one directory, as the host runner does in the workspace.
type dirRunner struct{ dir string }

func (r dirRunner) Run(ctx context.Context, cmd string, _ chan<- capture.StreamEvent) (capture.Result, error) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	c.Dir = r.dir
	out, err := c.CombinedOutput()
	return capture.Result{Stdout: string(out)}, err
}

// Undo with your files too: what the Turn wrote goes back, and what the
// human wrote after it stays.
func TestRollback_RevertsTheHumansDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("commands go through sh")
	}
	for _, revert := range []bool{true, false} {
		t.Run(map[bool]string{true: "reverting files", false: "leaving files"}[revert], func(t *testing.T) {
			dir := gitRepo(t)
			wt, err := worktree.Open(t.Context(), dir)
			require.NoError(t, err)

			fm := &fakeModel{replies: []model.Reply{
				{Requests: []event.ToolRequest{bashCall("a",
					"echo edited > tracked.txt && echo new > made.txt && mkdir -p pkg && echo x > pkg/a.go")}},
				{Text: "done"},
			}}
			r := rigWith(t, event.New(), fm, dirRunner{dir}, WithWorktree(wt))

			end := r.run("change things")
			cp := r.await(event.CheckpointTakenKind).(event.CheckpointTaken)
			assert.NotEmpty(t, cp.Tree, "the Turn checkpointed the directory")
			require.Equal(t, "edited\n", readFile(t, dir, "tracked.txt"))
			writeFile(t, dir, "mine.txt", "the human's\n")

			r.bus.Publish(event.RequestRollback{Turn: end.Turn, RevertFiles: revert})
			back := r.await(event.RolledBackKind).(event.RolledBack)
			assert.Equal(t, revert, back.RevertFiles)

			assert.Equal(t, "the human's\n", readFile(t, dir, "mine.txt"), "written after the Turn, so never reverted")
			if !revert {
				assert.Equal(t, "edited\n", readFile(t, dir, "tracked.txt"))
				assert.FileExists(t, filepath.Join(dir, "made.txt"))
				return
			}
			assert.Equal(t, "original\n", readFile(t, dir, "tracked.txt"))
			assert.NoFileExists(t, filepath.Join(dir, "made.txt"))
			assert.NoDirExists(t, filepath.Join(dir, "pkg"))
			n := r.await(event.NoticeKind).(event.Notice)
			assert.Equal(t, "warn", n.Level)
			assert.Contains(t, n.Text, "mine.txt", "the human is told what stayed")
		})
	}
}

// Without a tree for the Turn, asking to revert files says so rather than nothing.
func TestRollback_SaysWhenFilesWereNotCheckpointed(t *testing.T) {
	snap := &snapRunner{fakeRunner: &fakeRunner{out: "ok\n"}}
	r := rigWith(t, event.New(), &fakeModel{replies: []model.Reply{{Text: "a"}}}, snap)
	end := r.run("one")
	r.bus.Publish(event.RequestRollback{Turn: end.Turn, RevertFiles: true})
	r.await(event.RolledBackKind)
	n := r.await(event.NoticeKind).(event.Notice)
	assert.Contains(t, n.Text, "not checkpointed")
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	writeFile(t, dir, "tracked.txt", "original\n")
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "x"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	return dir
}

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644))
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	require.NoError(t, err)
	return string(b)
}
