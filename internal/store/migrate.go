package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
)

//go:embed migrations/*.sql
var migrations embed.FS

// goMigrations say what SQL cannot. Each is frozen once released: it describes
// the data as it was, not as it is now.
var goMigrations = []*goose.Migration{
	goose.NewGoMigration(3, &goose.GoFunc{RunTx: toolCallNames}, nil),
}

// migrate applies what db has not seen and returns the version it is at, first
// copying file aside when there is something to apply to a database already in use.
func migrate(db *sql.DB, file string) (int, error) {
	sqlFiles, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return 0, fmt.Errorf("store: read migrations: %w", err)
	}
	return migrateFrom(db, file, sqlFiles, goMigrations...)
}

// migrateFrom is migrate over any migrations, so a test can apply one that fails.
func migrateFrom(db *sql.DB, file string, sqlFiles fs.FS, gos ...*goose.Migration) (int, error) {
	p, err := provider(db, sqlFiles, gos...)
	if err != nil {
		return 0, err
	}
	sources := p.ListSources()
	if len(sources) == 0 {
		return 0, nil
	}
	latest := int(sources[len(sources)-1].Version)
	at, err := schemaVersion(db)
	switch {
	case err != nil:
		return 0, err
	case at > latest:
		return 0, fmt.Errorf("store: database is at schema %d, this build only knows %d, so it was written by a newer detent", at, latest)
	case at == latest:
		return at, nil
	}
	if file != "" && at > 0 {
		if err := backup(db, file, at); err != nil {
			return 0, err
		}
	}
	if _, err := p.Up(context.Background()); err != nil {
		return 0, fmt.Errorf("store: migrate: %w", err)
	}
	return latest, nil
}

// provider is goose over these migrations, counting versions in user_version.
func provider(db *sql.DB, sqlFiles fs.FS, gos ...*goose.Migration) (*goose.Provider, error) {
	p, err := goose.NewProvider(goose.DialectCustom, db, sqlFiles,
		goose.WithStore(pragmaVersions{}), goose.WithGoMigrations(gos...), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return nil, fmt.Errorf("store: migrations: %w", err)
	}
	return p, nil
}

// backup copies a database about to migrate to file.bak-v<at>, once: a copy
// already there is from an earlier attempt and is kept.
func backup(db *sql.DB, file string, at int) error {
	to := fmt.Sprintf("%s.bak-v%d", file, at)
	if _, err := os.Stat(to); err == nil {
		return nil
	}
	// VACUUM INTO is a consistent copy, WAL included, which copying the file is not.
	if _, err := db.ExecContext(context.Background(), `VACUUM INTO ?`, to); err != nil {
		return fmt.Errorf("store: back up before migrating: %w", err)
	}
	return os.Chmod(to, 0o600)
}

// pragmaVersions keeps goose's record of what ran in SQLite's own user_version, as
// detent did before goose, so a database migrated then needs nothing added.
// Every version up to it has run, since migrations count 1..n with no gap.
type pragmaVersions struct{}

var _ database.Store = pragmaVersions{}

func (pragmaVersions) Tablename() string { return "user_version" }

func (pragmaVersions) CreateVersionTable(context.Context, database.DBTxConn) error { return nil }

func (pragmaVersions) Insert(ctx context.Context, db database.DBTxConn, req database.InsertRequest) error {
	return setVersion(ctx, db, req.Version)
}

func (pragmaVersions) Delete(ctx context.Context, db database.DBTxConn, version int64) error {
	return setVersion(ctx, db, version-1)
}

func (pragmaVersions) GetMigration(ctx context.Context, db database.DBTxConn, version int64) (*database.GetMigrationResult, error) {
	at, err := readVersion(ctx, db)
	if err != nil {
		return nil, err
	}
	if version > at {
		return nil, database.ErrVersionNotFound
	}
	return &database.GetMigrationResult{IsApplied: true}, nil
}

func (pragmaVersions) GetLatestVersion(ctx context.Context, db database.DBTxConn) (int64, error) {
	return readVersion(ctx, db)
}

func (pragmaVersions) ListMigrations(ctx context.Context, db database.DBTxConn) ([]*database.ListMigrationsResult, error) {
	at, err := readVersion(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make([]*database.ListMigrationsResult, 0, at+1)
	for v := at; v >= 0; v-- {
		out = append(out, &database.ListMigrationsResult{Version: v, IsApplied: true})
	}
	return out, nil
}

func schemaVersion(db *sql.DB) (int, error) {
	at, err := readVersion(context.Background(), db)
	return int(at), err
}

func readVersion(ctx context.Context, db database.DBTxConn) (int64, error) {
	var at int64
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&at); err != nil {
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}
	return at, nil
}

// setVersion runs in the migration's transaction, so a failed one records nothing.
func setVersion(ctx context.Context, db database.DBTxConn, version int64) error {
	// PRAGMA takes a literal, and version is goose's count, never input.
	_, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, version))
	return err
}
