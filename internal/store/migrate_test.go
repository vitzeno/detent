package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrate_BringsAFreshDatabaseUpToDate(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.Zero(t, userVersion(t, db))
	require.NoError(t, migrate(db))
	assert.Positive(t, userVersion(t, db), "a migrated database records how far it got")

	_, err = db.Exec(`SELECT 1 FROM events LIMIT 1`)
	assert.NoError(t, err, "the table exists")
}

// Reopening must not reapply: migration 1 creates a table and would
// fail twice, which is what proves the version is read.
func TestMigrate_IsANoOpSecondTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")

	first, err := Open(path)
	require.NoError(t, err)
	at := userVersion(t, first.db)
	require.NoError(t, first.Close())

	second, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	assert.Equal(t, at, userVersion(t, second.db))
}

// A newer build's database has migrations this one never saw, so
// saying so beats failing later on a missing column.
func TestMigrate_RefusesADatabaseFromTheFuture(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

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
	t.Cleanup(func() { _ = db.Close() })

	err = apply(db, step{name: "0001_bad.sql", sql: `CREATE TABLE fine (x INTEGER); THIS IS NOT SQL;`}, 1)
	require.Error(t, err)

	assert.Zero(t, userVersion(t, db), "the version bump rolled back with it")
	_, err = db.Exec(`SELECT 1 FROM fine LIMIT 1`)
	assert.Error(t, err, "and so did the half that worked")
}

// The version is a migration's place in order, so SQL files and Go steps
// must count 1..n between them, with no gap and no number taken twice.
func TestMigrate_NumberingHasNoGaps(t *testing.T) {
	_, err := steps()
	require.NoError(t, err, "the shipped migrations")

	run := func(context.Context, *sql.Tx) error { return nil }
	tests := []struct {
		files map[string]string
		gos   map[int]step
	}{
		{map[string]string{"0001_a.sql": "", "0003_b.sql": ""}, nil},
		{map[string]string{"001_a.sql": ""}, nil},
		{map[string]string{"a.sql": ""}, nil},
		{map[string]string{"0001_a.sql": ""}, map[int]step{1: {name: "0001_go", run: run}}},
		{map[string]string{"0001_a.sql": ""}, map[int]step{3: {name: "0003_go", run: run}}},
	}
	for _, c := range tests {
		_, err := assemble(c.files, c.gos)
		require.Error(t, err, "%v %v", c.files, c.gos)
	}
	got, err := assemble(map[string]string{"0001_a.sql": "", "0003_c.sql": ""}, map[int]step{2: {name: "0002_go", run: run}})
	require.NoError(t, err)
	assert.Equal(t, []string{"0001_a.sql", "0002_go", "0003_c.sql"}, []string{got[0].name, got[1].name, got[2].name})
}

// A Go step that fails rolls back like a SQL one, version bump and all.
func TestMigrate_DoesNotRecordAFailedGoStep(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	err = apply(db, step{name: "0001_go", run: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE half (x INTEGER)`); err != nil {
			return err
		}
		return errors.New("then it failed")
	}}, 1)
	require.Error(t, err)
	assert.Zero(t, userVersion(t, db))
	_, err = db.Exec(`SELECT 1 FROM half LIMIT 1`)
	assert.Error(t, err, "the table it made rolled back too")
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	require.NoError(t, db.QueryRow(`PRAGMA user_version`).Scan(&v))
	return v
}
