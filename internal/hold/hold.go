// Package hold keeps a session to the one detent running it, with a lock the
// OS drops when that process ends, so a crash leaves nothing to clear up.
package hold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

// ErrHeld is a session another detent holds.
var ErrHeld = errors.New("open in another detent")

// Holder is the one session this process holds, moved as it moves.
type Holder struct {
	dir string

	mu     sync.Mutex
	held   uuid.UUID
	unlock func()
}

// New holds sessions under dir, one lock file each.
func New(dir string) *Holder { return &Holder{dir: dir} }

// Take holds session, then lets go of the one held before. Refused, the old
// one is still held, since the caller stays on it.
func (h *Holder) Take(session uuid.UUID) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if session == h.held && h.unlock != nil {
		return nil
	}
	unlock, err := Lock(h.dir, session)
	if err != nil {
		return err
	}
	if h.unlock != nil {
		h.unlock()
	}
	h.held, h.unlock = session, unlock
	return nil
}

// Close lets go of the session held, which the store must have finished writing.
func (h *Holder) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.unlock != nil {
		h.unlock()
		h.held, h.unlock = uuid.Nil, nil
	}
}

// Lock holds session until unlock, failing with ErrHeld when another holds it.
// A delete takes it for as long as it runs, so nothing resumes what is going.
func Lock(dir string, session uuid.UUID) (unlock func(), err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("hold: %w", err)
	}
	f, err := os.OpenFile(path(dir, session), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("hold: %w", err)
	}
	if err := lock(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	// Closing the file is what lets go.
	return func() { _ = f.Close() }, nil
}

// Remove deletes a deleted session's lock file, once nothing holds it.
func Remove(dir string, session uuid.UUID) error {
	if err := os.Remove(path(dir, session)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("hold: %w", err)
	}
	return nil
}

// DefaultDir is beside the store, or with no home directory a shared temporary one.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "detent-sessions")
	}
	return filepath.Join(home, ".local", "state", "detent", "sessions")
}

func path(dir string, session uuid.UUID) string {
	return filepath.Join(dir, session.String()+".lock")
}
