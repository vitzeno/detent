package hold

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// lock is LockFileEx on the first byte, which another handle conflicts with even in this process.
func lock(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	switch {
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION):
		return ErrHeld
	case err != nil:
		return fmt.Errorf("hold: %w", err)
	}
	return nil
}
