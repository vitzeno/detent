package host

import (
	"os/exec"
	"sync"

	"github.com/vitzeno/detent/internal/winjob"
)

// group is the Job Object a command runs in, since Windows has no
// process groups to signal.
type group struct {
	mu  sync.Mutex
	job *winjob.Job
}

// newGroup makes a cancel kill the whole job, falling back to the shell
// alone when it never joined one.
func newGroup(cmd *exec.Cmd) *group {
	g := &group{}
	cmd.Cancel = func() error {
		g.mu.Lock()
		job := g.job
		g.mu.Unlock()
		if job != nil && job.Kill() == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
	return g
}

// started joins the job straight after Start. Anything the shell starts before
// then is outside it, a race detent nearly always wins as shells start slowly.
func (g *group) started(cmd *exec.Cmd) {
	job, err := winjob.Assign(cmd.Process)
	if err != nil {
		return
	}
	g.mu.Lock()
	g.job = job
	g.mu.Unlock()
}

// done leaves a backgrounded child running, as it does under sh.
func (g *group) done() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != nil {
		_ = g.job.Release()
	}
}
