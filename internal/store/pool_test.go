package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A pragma run once reaches one pooled connection, so the pool is held to
// one and every connection it opens must come configured.
func TestOpen_ConfiguresEveryConnection(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "events.db"))
	require.NoError(t, err)
	defer s.Close()

	assert.Equal(t, 1, s.db.Stats().MaxOpenConnections)
	// Closing the idle connection makes the next query open a fresh one.
	s.db.SetMaxIdleConns(0)

	var fk, busy int
	var mode string
	require.NoError(t, s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk))
	require.NoError(t, s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy))
	require.NoError(t, s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	assert.Equal(t, 1, fk)
	assert.Equal(t, 5000, busy)
	assert.Equal(t, "wal", mode)
}

// ":memory:" is one database per connection, so it only works pooled to one.
func TestOpen_InMemoryKeepsItsTables(t *testing.T) {
	s, err := Open(":memory:")
	require.NoError(t, err)
	defer s.Close()
	_, err = s.Sessions()
	assert.NoError(t, err)
}
