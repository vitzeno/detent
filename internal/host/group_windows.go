package host

import "os/exec"

// killGroup keeps the default kill on Windows, which has no process groups to signal.
func killGroup(*exec.Cmd) {}
