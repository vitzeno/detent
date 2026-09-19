// Package editfile reads, diffs, and writes files for the editor
// component. Write is the harness's own deterministic action — never a
// model-proposed shell command, and never invoked without the caller
// having shown the human a Diff first. See internal/ui's save-confirm
// flow for where that approval happens.
package editfile

import (
	"fmt"
	"os"

	"github.com/aymanbagabas/go-udiff"
)

// MaxBytes bounds how much of a file the editor will load — matches
// the spirit of shell.MaxOutputBytes: cap it, tell the caller it was
// capped, never silently truncate.
const MaxBytes = 256 * 1024

// Read loads path's current content for editing. Truncated reports
// whether the file was larger than MaxBytes and got cut off.
func Read(path string) (content string, truncated bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("editfile: read %s: %w", path, err)
	}
	if len(raw) > MaxBytes {
		return string(raw[:MaxBytes]), true, nil
	}
	return string(raw), false, nil
}

// Diff renders a unified diff of old -> new for path, or "" when they
// don't differ — the caller's cue that there's nothing to save.
func Diff(path, old, newContent string) string {
	if old == newContent {
		return ""
	}
	return udiff.Unified(path, path, old, newContent)
}

// Write persists content to path, replacing it entirely. If path
// already exists its permissions are preserved (os.WriteFile only
// applies perm when creating a new file); only called after a human
// has approved the Diff.
func Write(path, content string) error {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("editfile: write %s: %w", path, err)
	}
	return nil
}
