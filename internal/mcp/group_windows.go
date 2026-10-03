package mcp

import (
	"os/exec"

	"github.com/vitzeno/detent/internal/winjob"
)

func ownGroup(*exec.Cmd) {}

// joinGroup puts a started server in a Job Object and returns what ends
// the job, children and all, once it has closed.
func joinGroup(cmd *exec.Cmd) func() {
	if cmd.Process == nil {
		return func() {}
	}
	job, err := winjob.Assign(cmd.Process)
	if err != nil {
		return func() {}
	}
	return func() { _ = job.Close() }
}
