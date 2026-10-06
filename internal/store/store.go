// Package store keeps a session's events on disk, so it can be
// replayed rather than reconstructed. Speaks event.Record only.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"modernc.org/sqlite" // pure Go, because CGO_ENABLED=0 is the cross-build
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/vitzeno/detent/event"
	"github.com/vitzeno/detent/version"
)

// ReservedName is the one word -resume reads as an instruction.
const ReservedName = "last"

// pragmas are per connection, so the DSN carries them. The busy wait and
// WAL make a second writer wait, and immediate transactions let it.
const pragmas = "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"

// Store is one database, safe for concurrent use.
type Store struct{ db *sql.DB }

// Open creates the database if it is not there. ":memory:" works, for
// a test that wants no file.
func Open(path string) (*Store, error) {
	switch {
	case path == "":
		return nil, errors.New("store: no path, so nothing is recorded")
	case strings.Contains(path, "?"):
		return nil, fmt.Errorf("store: %s: a path cannot contain ?", path)
	}
	if dir := filepath.Dir(path); dir != "." && path != ":memory:" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+pragmas)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// SQLite has one writer anyway, and one connection is also what
	// keeps ":memory:" a single database.
	db.SetMaxOpenConns(1)
	if path != ":memory:" {
		if err := backup(db, path); err != nil {
			return nil, errors.Join(err, db.Close())
		}
	}
	if err := migrate(db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &Store{db: db}, nil
}

// DefaultPath is where a session's events go, beside the logs.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "detent", "events.db")
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Append writes one record keyed on the bus's ordinal, so writing it twice
// is the same row. A SessionStarted writes the header in the same transaction.
func (s *Store) Append(session uuid.UUID, r event.Record) error {
	payload, err := event.Encode(r.Event)
	if err != nil {
		return fmt.Errorf("store: encode %s: %w", r.Event.Kind(), err)
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: append: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a committed tx rolls back to nothing

	if started, ok := r.Event.(event.SessionStarted); ok {
		if _, err := tx.ExecContext(ctx, upsertSession, session.String(), r.At.UnixMilli(),
			started.Model, started.Judge, started.Sandbox, started.Network,
			started.Resumed, version.String(), latest()); err != nil {
			return fmt.Errorf("store: session header: %w", err)
		}
	}
	turn, toolCall, agent := event.Subject(r.Event)
	if _, err := tx.ExecContext(ctx, insert, session.String(), r.Ordinal, r.At.UnixMilli(),
		string(r.Event.Kind()), nullable(turn), nullable(toolCall), nullable(agent), payload); err != nil {
		return fmt.Errorf("store: append %s: %w", r.Event.Kind(), err)
	}
	return tx.Commit()
}

// Replay is a session's records in publish order. Whole rather than
// streamed: a session is small, and bounding it is a later decision.
func (s *Store) Replay(session uuid.UUID) ([]event.Record, error) {
	rows, err := s.db.QueryContext(context.Background(), selectSession, session.String())
	if err != nil {
		return nil, fmt.Errorf("store: replay: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err reports what reading can fail with

	var out []event.Record
	for rows.Next() {
		var (
			ordinal, at int64
			kind        string
			payload     []byte
		)
		if err := rows.Scan(&ordinal, &at, &kind, &payload); err != nil {
			return nil, fmt.Errorf("store: scan: %w", err)
		}
		e, err := event.Decode(event.Kind(kind), payload)
		if err != nil {
			return nil, fmt.Errorf("store: replay: %w", err)
		}
		out = append(out, event.Record{
			Ordinal: uint64(ordinal), //nolint:gosec // written from a uint64 by Append
			At:      time.UnixMilli(at), Event: e,
		})
	}
	return out, rows.Err()
}

// Sessions are what can be replayed, newest first.
func (s *Store) Sessions() ([]event.SessionSummary, error) {
	rows, err := s.db.QueryContext(context.Background(), selectSessions)
	if err != nil {
		return nil, fmt.Errorf("store: sessions: %w", err)
	}
	defer rows.Close() //nolint:errcheck // rows.Err reports what reading can fail with

	var out []event.SessionSummary
	for rows.Next() {
		var (
			id     string
			name   string
			at     int64
			model  string
			events int
			used   int64
		)
		if err := rows.Scan(&id, &name, &at, &model, &events, &used); err != nil {
			return nil, fmt.Errorf("store: scan session: %w", err)
		}
		parsed, err := uuid.Parse(id)
		if err != nil {
			continue // not ours to offer
		}
		out = append(out, event.SessionSummary{ID: parsed, Name: name, Model: model,
			Started: time.UnixMilli(at).UTC(), Used: time.UnixMilli(used).UTC(), Events: events})
	}
	return out, rows.Err()
}

// Rename names a session. A name -resume would read as an id or as
// "last" is refused here rather than found not to work later.
func (s *Store) Rename(session uuid.UUID, name string) error {
	if err := usableName(name); err != nil {
		return err
	}
	res, err := s.db.ExecContext(context.Background(), renameSession, name, session.String())
	if err != nil {
		var se *sqlite.Error
		if errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return fmt.Errorf("another session is already called %q", name)
		}
		return fmt.Errorf("store: rename: %w", err)
	}
	// Said rather than reported as named, since the header may not be written yet.
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("no session %s to name", session)
	}
	return nil
}

// Delete forgets a session and its events, reporting whether there
// was anything to forget.
func (s *Store) Delete(session uuid.UUID) (bool, error) {
	res, err := s.db.ExecContext(context.Background(), deleteSession, session.String())
	if err != nil {
		return false, fmt.Errorf("store: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: delete: %w", err)
	}
	return n > 0, nil
}

func usableName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("a name cannot be blank")
	case strings.EqualFold(name, ReservedName):
		return fmt.Errorf("%q is what -resume calls the most recently used session, so a name would never be read", ReservedName)
	}
	if _, err := uuid.Parse(name); err == nil {
		return fmt.Errorf("%q reads as a session id, so a name would never be read", name)
	}
	return nil
}

// nullable keeps a zero uuid out of the column, so a turn query
// cannot match a record belonging to none.
func nullable(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id.String()
}
