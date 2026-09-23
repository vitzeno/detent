package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrate applies what a database has not seen. SQLite counts that
// itself in user_version, so there is no table of ours and no library
// for one table's worth of DDL.
func migrate(db *sql.DB) error {
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("store: read migrations: %w", err)
	}

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
	// PRAGMA takes a literal, and version is an int we counted.
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		return err
	}
	return tx.Commit()
}
