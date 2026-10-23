package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vitzeno/detent/event"
)

func TestMigrate_BringsAFreshDatabaseUpToDate(t *testing.T) {
	db := memory(t)
	require.Zero(t, userVersion(t, db))
	at, err := migrate(db, "")
	require.NoError(t, err)
	assert.Equal(t, at, userVersion(t, db), "a migrated database records how far it got")

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
	db := memory(t)
	_, err := db.Exec(`PRAGMA user_version = 99`)
	require.NoError(t, err)

	_, err = migrate(db, "")
	require.Error(t, err)
	assert.ErrorContains(t, err, "newer detent")
}

// A failed migration must not record as applied, or the next start
// skips it and runs against a table that was never created.
func TestMigrate_DoesNotRecordAFailedMigration(t *testing.T) {
	db := memory(t)
	bad := fstest.MapFS{"0001_bad.sql": {Data: []byte("-- +goose Up\nCREATE TABLE fine (x INTEGER);\nTHIS IS NOT SQL;\n")}}

	_, err := migrateFrom(db, "", bad)
	require.Error(t, err)
	assert.Zero(t, userVersion(t, db), "the version bump rolled back with it")
	_, err = db.Exec(`SELECT 1 FROM fine LIMIT 1`)
	assert.Error(t, err, "and so did the half that worked")
}

// A Go migration that fails rolls back like a SQL one, version bump and all.
func TestMigrate_DoesNotRecordAFailedGoMigration(t *testing.T) {
	db := memory(t)
	half := goose.NewGoMigration(1, &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE half (x INTEGER)`); err != nil {
			return err
		}
		return errors.New("then it failed")
	}}, nil)

	_, err := migrateFrom(db, "", fstest.MapFS{}, half)
	require.Error(t, err)
	assert.Zero(t, userVersion(t, db))
	_, err = db.Exec(`SELECT 1 FROM half LIMIT 1`)
	assert.Error(t, err, "the table it made rolled back too")
}

// user_version says every migration up to it ran, which holds only while the
// shipped ones count on from the baseline with no gap.
func TestMigrate_NumberingHasNoGaps(t *testing.T) {
	sources := shipped(t, memory(t)).ListSources()
	for i, src := range sources {
		assert.Equal(t, sources[0].Version+int64(i), src.Version, src.Path)
	}
}

// A database v0.5.0 left is at the baseline, so it is migrated on and never rebuilt,
// and one from before is refused rather than half rebuilt.
func TestMigrate_StartsFromTheBaseline(t *testing.T) {
	db := memory(t)
	_, err := shipped(t, db).UpTo(t.Context(), 4)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE marker (x INTEGER)`)
	require.NoError(t, err)
	at, err := migrate(db, "")
	require.NoError(t, err)
	assert.Positive(t, at)
	_, err = db.Exec(`SELECT 1 FROM marker LIMIT 1`)
	require.NoError(t, err, "nothing rebuilt what v0.5.0 made, so what it held stays")

	old := memory(t)
	_, err = old.Exec(`PRAGMA user_version = 2`)
	require.NoError(t, err)
	_, err = migrate(old, "")
	require.Error(t, err)
	assert.ErrorContains(t, err, "open it once with v0.5.0")
}

// A session stored while per-output views existed holds view.ready records, which
// nothing decodes any more, so migrating drops them and the session replays.
func TestMigrate_0005DropsTheViewsASessionStored(t *testing.T) {
	file := filepath.Join(t.TempDir(), "events.db")
	db, err := sql.Open("sqlite", file+pragmas)
	require.NoError(t, err)
	_, err = shipped(t, db).UpTo(t.Context(), 4)
	require.NoError(t, err)
	session := uuid.Must(uuid.NewV7())
	_, err = db.Exec(`INSERT INTO sessions (id, started, model, sandbox, network) VALUES (?, 0, 'm', 0, 0)`, session.String())
	require.NoError(t, err)
	for i, r := range []struct{ kind, payload string }{
		{"notice", `{"Level":"info","Text":"kept"}`},
		{"view.ready", `{"ToolCall":"01a101e1-fc16-7dcb-a97a-a4d28284b10d","Source":"shipped"}`},
	} {
		_, err = db.Exec(`INSERT INTO events (session, ordinal, at, kind, payload) VALUES (?, ?, 0, ?, ?)`,
			session.String(), i+1, r.kind, []byte(r.payload))
		require.NoError(t, err)
	}
	require.NoError(t, db.Close())

	s, err := Open(file)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	got, err := s.Replay(session)
	require.NoError(t, err, "a view.ready record left behind fails the whole replay")
	require.Len(t, got, 1)
	assert.Equal(t, event.Notice{Level: "info", Text: "kept"}, got[0].Event)
}

// shipped is goose over the migrations this build carries.
func shipped(t *testing.T, db *sql.DB) *goose.Provider {
	t.Helper()
	sqlFiles, err := fs.Sub(migrations, "migrations")
	require.NoError(t, err)
	p, err := provider(db, sqlFiles)
	require.NoError(t, err)
	return p
}

func memory(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	require.NoError(t, db.QueryRow(`PRAGMA user_version`).Scan(&v))
	return v
}
