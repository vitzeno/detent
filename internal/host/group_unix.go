//go:build !windows

package host

import (
	"os/exec"
	"syscall"
)

// group is the process group a command leads. Unlike a Windows job, it
// outlives a detent that is killed.
type group struct{}

// newGroup makes a cancel kill the command's whole process group, since
// killing sh alone leaves its children running on the host.
func newGroup(cmd *exec.Cmd) *group {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	return &group{}
}

func (*group) started(*exec.Cmd) {}

func (*group) done() {}
