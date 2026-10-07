package hold

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A second detent on a session overwrote the first one's records, so only one
// may hold it, and the other is told.
func TestTake_RefusesASessionHeldElsewhere(t *testing.T) {
	dir := t.TempDir()
	session := uuid.Must(uuid.NewV7())
	first, second := New(dir), New(dir)
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)

	require.NoError(t, first.Take(session))
	require.ErrorIs(t, second.Take(session), ErrHeld)

	first.Close()
	require.NoError(t, second.Take(session), "a closed holder lets go")
}

// Moving to another session lets go of the old one, and a refused move keeps it.
func TestTake_MovesTheHoldOnlyOnceTheNewOneIsHeld(t *testing.T) {
	dir := t.TempDir()
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	mine, theirs := New(dir), New(dir)
	t.Cleanup(mine.Close)
	t.Cleanup(theirs.Close)
	require.NoError(t, mine.Take(a))
	require.NoError(t, theirs.Take(b))

	require.ErrorIs(t, mine.Take(b), ErrHeld)
	_, err := Lock(dir, a)
	require.ErrorIs(t, err, ErrHeld, "a refused move still holds the old session")

	theirs.Close()
	require.NoError(t, mine.Take(b))
	unlock, err := Lock(dir, a)
	require.NoError(t, err, "the old session was let go")
	unlock()
}

func TestTake_TheSameSessionAgainIsKept(t *testing.T) {
	h := New(t.TempDir())
	t.Cleanup(h.Close)
	session := uuid.Must(uuid.NewV7())
	require.NoError(t, h.Take(session))
	assert.NoError(t, h.Take(session))
}

func TestRemove_ForgetsTheLockFile(t *testing.T) {
	dir := t.TempDir()
	session := uuid.Must(uuid.NewV7())
	unlock, err := Lock(dir, session)
	require.NoError(t, err)
	unlock()
	require.NoError(t, Remove(dir, session))
	assert.NoFileExists(t, path(dir, session))
	assert.NoError(t, Remove(dir, session), "already gone is fine")
}
