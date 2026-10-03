package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrate applies what a database has not seen, counted by SQLite's own
// user_version rather than a table or a library.
func migrate(db *sql.DB) error {
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("store: read migrations: %w", err)
	}
	if err := numbered(files); err != nil {
		return err
	}

	var at int
	if err := db.QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&at); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if at > len(files) {
		return fmt.Errorf("store: database is at schema %d, this build only knows %d, so it was written by a newer detent", at, len(files))
	}

	for i := at; i < len(files); i++ {
		body, err := migrations.ReadFile(files[i])
		if err != nil {
			return fmt.Errorf("store: read %s: %w", files[i], err)
		}
		if err := apply(db, string(body), i+1); err != nil {
			return fmt.Errorf("store: %s: %w", files[i], err)
		}
	}
	return nil
}

// numbered checks each file's own number is its place, since the version
// is the place: a gap or a misnamed file would shift every later one.
func numbered(files []string) error {
	for i, f := range files {
		prefix, _, _ := strings.Cut(path.Base(f), "_")
		if n, err := strconv.Atoi(prefix); err != nil || n != i+1 {
			return fmt.Errorf("store: migration %s is number %d in order, so it must be named %04d_*.sql", f, i+1, i+1)
		}
	}
	return nil
}

func apply(db *sql.DB, body string, version int) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a committed tx rolls back to nothing
	if _, err := tx.ExecContext(ctx, body); err != nil {
		return err
	}
	// PRAGMA takes a literal, and version is an int we counted.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		return err
	}
	return tx.Commit()
}
