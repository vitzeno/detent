//go:build !windows

package sandbox

import (
	"errors"
	"syscall"
)

// processAlive asks the kernel without signalling. EPERM is a process
// that exists under another user.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
