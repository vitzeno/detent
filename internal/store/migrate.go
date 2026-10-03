package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrations embed.FS

// step is one migration, a SQL file or a Go function for what SQL cannot
// say, numbered by its place. Either way it runs in one transaction with
// its version bump, so a half-applied step is never recorded as done.
type step struct {
	name string
	sql  string
	run  func(ctx context.Context, tx *sql.Tx) error
}

// goSteps are the migrations written in Go, keyed by their number. Each is
// frozen once released: it describes the data as it was, not as it is now.
var goSteps = map[int]step{
	3: {name: "0003_tool_call_names", run: toolCallNames},
}

// steps is every migration in order: the embedded SQL files and goSteps.
func steps() ([]step, error) {
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	sqlFiles := map[string]string{}
	for _, f := range files {
		body, err := migrations.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("store: read %s: %w", f, err)
		}
		sqlFiles[path.Base(f)] = string(body)
	}
	return assemble(sqlFiles, goSteps)
}

// assemble orders SQL files and Go steps by number, checking they count
// 1..n together with no gap and no number used twice.
func assemble(sqlFiles map[string]string, gos map[int]step) ([]step, error) {
	byNumber := map[int]step{}
	for name, body := range sqlFiles {
		prefix, _, _ := strings.Cut(name, "_")
		n, err := strconv.Atoi(prefix)
		if err != nil || len(prefix) != 4 {
			return nil, fmt.Errorf("store: migration %s must start with its number, as 0001_", name)
		}
		byNumber[n] = step{name: name, sql: body}
	}
	for n, s := range gos {
		if _, taken := byNumber[n]; taken {
			return nil, fmt.Errorf("store: migration %d is both %s and %s", n, byNumber[n].name, s.name)
		}
		byNumber[n] = s
	}
	out := make([]step, len(byNumber))
	for n, s := range byNumber {
		if n < 1 || n > len(byNumber) {
			return nil, fmt.Errorf("store: migration %s is number %d, but there are %d, so one is missing", s.name, n, len(byNumber))
		}
		out[n-1] = s
	}
	return out, nil
}

// latest is the schema version this build writes.
func latest() int {
	all, err := steps()
	if err != nil {
		return 0
	}
	return len(all)
}

// migrate applies what a database has not seen, counted by SQLite's own
// user_version rather than a table or a library.
func migrate(db *sql.DB) error {
	all, err := steps()
	if err != nil {
		return err
	}
	at, err := schemaVersion(db)
	if err != nil {
		return err
	}
	if at > len(all) {
		return fmt.Errorf("store: database is at schema %d, this build only knows %d, so it was written by a newer detent", at, len(all))
	}
	for i := at; i < len(all); i++ {
		if err := apply(db, all[i], i+1); err != nil {
			return fmt.Errorf("store: %s: %w", all[i].name, err)
		}
	}
	return nil
}

func schemaVersion(db *sql.DB) (int, error) {
	var at int
	if err := db.QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&at); err != nil {
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}
	return at, nil
}

// backup copies a database that is about to migrate to path.bak-v<at>,
// once: a copy already there is from an earlier attempt and is kept.
func backup(db *sql.DB, file string) error {
	at, err := schemaVersion(db)
	if err != nil || at == 0 || at >= latest() {
		return err // new, or nothing to do
	}
	to := fmt.Sprintf("%s.bak-v%d", file, at)
	if _, err := os.Stat(to); err == nil {
		return nil
	}
	// VACUUM INTO is a consistent copy, WAL included, which copying the file is not.
	if _, err := db.ExecContext(context.Background(), `VACUUM INTO ?`, to); err != nil {
		return fmt.Errorf("store: back up before migrating: %w", err)
	}
	return nil
}

func apply(db *sql.DB, s step, version int) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a committed tx rolls back to nothing
	if s.run != nil {
		err = s.run(ctx, tx)
	} else {
		_, err = tx.ExecContext(ctx, s.sql)
	}
	if err != nil {
		return err
	}
	// PRAGMA takes a literal, and version is an int we counted.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		return err
	}
	return tx.Commit()
}
