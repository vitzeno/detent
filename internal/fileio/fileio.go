// Package fileio reads, diffs, and writes files for the editor
// component. Write is the harness's own deterministic action — never a
// model-proposed shell command, and never invoked without the caller
// having shown the human a Diff first. See internal/ui's save-confirm
// flow for where that approval happens.
package fileio

import (
	"fmt"
	"os"
)

// MaxBytes bounds how much of a file the editor will load — matches
// the spirit of host.MaxOutputBytes: cap it, tell the caller it was
// capped, never silently truncate.
const MaxBytes = 256 * 1024

// defaultFilePerm is used only when Write creates a new file — see
// Write's own comment on why an existing file's permissions win instead.
const defaultFilePerm = 0o644

// Read loads path's current content for editing. Truncated reports
// whether the file was larger than MaxBytes and got cut off.
func Read(path string) (content string, truncated bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("fileio: read %s: %w", path, err)
	}
	if len(raw) > MaxBytes {
		return string(raw[:MaxBytes]), true, nil
	}
	return string(raw), false, nil
}

// Write persists content to path, replacing it entirely. If path
// already exists its permissions are preserved (os.WriteFile only
// applies perm when creating a new file); only called after a human
// has approved the Diff.
func Write(path, content string) error {
	if err := os.WriteFile(path, []byte(content), defaultFilePerm); err != nil {
		return fmt.Errorf("fileio: write %s: %w", path, err)
	}
	return nil
}
