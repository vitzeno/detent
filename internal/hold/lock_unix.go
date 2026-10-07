//go:build !windows

package hold

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// lock is flock, which another open of the file conflicts with even in this process.
func lock(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) //nolint:gosec // a descriptor fits an int
	switch {
	case errors.Is(err, unix.EWOULDBLOCK):
		return ErrHeld
	case err != nil:
		return fmt.Errorf("hold: %w", err)
	}
	return nil
}
