package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrate_BringsAFreshDatabaseUpToDate(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	require.Zero(t, version(t, db))
	require.NoError(t, migrate(db))
	assert.Positive(t, version(t, db), "a migrated database records how far it got")

	_, err = db.Exec(`SELECT 1 FROM events LIMIT 1`)
	assert.NoError(t, err, "the table exists")
}

// Reopening must not reapply: migration 1 creates a table and would
// fail twice, which is what proves the version is read.
func TestMigrate_IsANoOpSecondTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")

	first, err := Open(path)
	require.NoError(t, err)
	at := version(t, first.db)
	require.NoError(t, first.Close())

	second, err := Open(path)
	require.NoError(t, err)
	defer second.Close()
	assert.Equal(t, at, version(t, second.db))
}

// A newer build's database has migrations this one never saw, so
// saying so beats failing later on a missing column.
func TestMigrate_RefusesADatabaseFromTheFuture(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`PRAGMA user_version = 99`)
	require.NoError(t, err)

	err = migrate(db)
	require.Error(t, err)
	assert.ErrorContains(t, err, "newer detent")
}

// A failed migration must not record as applied, or the next start
// skips it and runs against a table that was never created.
func TestMigrate_DoesNotRecordAFailedMigration(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	err = apply(db, `CREATE TABLE fine (x INTEGER); THIS IS NOT SQL;`, 1)
	require.Error(t, err)

	assert.Zero(t, version(t, db), "the version bump rolled back with it")
	_, err = db.Exec(`SELECT 1 FROM fine LIMIT 1`)
	assert.Error(t, err, "and so did the half that worked")
}

func version(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	require.NoError(t, db.QueryRow(`PRAGMA user_version`).Scan(&v))
	return v
}
