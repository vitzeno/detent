//go:build !windows

package host

import (
	"os/exec"
	"syscall"
)

// killGroup makes a cancel kill the command's whole process group, since
// killing sh alone leaves its children running on the host.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
