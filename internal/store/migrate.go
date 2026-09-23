package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrate brings a database up to the newest migration, applying only
// what it has not seen. SQLite counts that itself in user_version, so
// there is no table of our own to keep and no library to pull in for
// one table.
//
// Each file is applied in one transaction with the version bump, so a
// half-applied migration cannot be recorded as done.
func migrate(db *sql.DB) error {
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("store: read migrations: %w", err)
	}
	// Glob sorts, and the names are numbered, so this is apply order.

	var at int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&at); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	if at > len(files) {
		return fmt.Errorf("store: database is at schema %d, this build only knows %d — it was written by a newer detent", at, len(files))
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

func apply(db *sql.DB, body string, version int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a committed tx rolls back to nothing
	if _, err := tx.Exec(body); err != nil {
		return err
	}
	// Not a placeholder: PRAGMA takes a literal, and version is an int
	// we counted ourselves.
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		return err
	}
	return tx.Commit()
}
