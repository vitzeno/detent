//go:build unix

package mcp

import (
	"os/exec"
	"syscall"
)

// ownGroup starts a server as the leader of its own process group.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// joinGroup returns what signals whatever is left of a server's group once it has closed.
func joinGroup(cmd *exec.Cmd) func() {
	return func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	}
}
