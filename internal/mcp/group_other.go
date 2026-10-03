//go:build !unix && !windows

package mcp

import "os/exec"

func ownGroup(*exec.Cmd) {}

func joinGroup(*exec.Cmd) func() { return func() {} }
