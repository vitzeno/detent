package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openBrowser hands a link to the platform's opener and does not wait:
// a browser outlives the command that started it.
func openBrowser(link string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		cmd = exec.Command("xdg-open", link)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opening a browser: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
