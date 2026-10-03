package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

// openBrowser hands a link to the platform's opener and does not wait:
// a browser outlives the command that started it.
func openBrowser(link string) error {
	// Background, since nothing should cancel a browser once it is asked for.
	ctx := context.Background()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", link)
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", link)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", link)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("opening a browser: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
