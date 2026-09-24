// Package store keeps a session's events on disk, so it can be
// replayed rather than reconstructed. Speaks event.Record only.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite" // pure Go, because CGO_ENABLED=0 is the cross-build

	"github.com/vitzeno/detent/event"
)

// Store is one database, safe for concurrent use.
type Store struct{ db *sql.DB }

// Open creates the database if it is not there. ":memory:" works, for
// a test that wants no file.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." && path != ":memory:" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// Off by default in SQLite, and per connection, so the pool has
	// to be told rather than the database.
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: enable foreign keys: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Append writes one record, keyed on the bus's ordinal, so writing it
// twice is the same row. A SessionStarted writes the header in the
// same transaction, since the events row has a foreign key to it.
func (s *Store) Append(session uuid.UUID, r event.Record) error {
	payload, err := event.Encode(r.Event)
	if err != nil {
		return fmt.Errorf("store: encode %s: %w", r.Event.Kind(), err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: append: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // a committed tx rolls back to nothing

	if started, ok := r.Event.(event.SessionStarted); ok {
		if _, err := tx.Exec(upsertSession, session.String(), r.At.UnixMilli(),
			started.Model, started.Judge, started.Sandbox, started.Network,
			started.Resumed); err != nil {
			return fmt.Errorf("store: session header: %w", err)
		}
	}
	turn, call := event.Subject(r.Event)
	if _, err := tx.Exec(insert, session.String(), r.Ordinal, r.At.UnixMilli(),
		string(r.Event.Kind()), nullable(turn), nullable(call), payload); err != nil {
		return fmt.Errorf("store: append %s: %w", r.Event.Kind(), err)
	}
	return tx.Commit()
}

// Replay is a session's records in publish order. Whole rather than
// streamed: a session is small, and bounding it is a later decision.
func (s *Store) Replay(session uuid.UUID) ([]event.Record, error) {
	rows, err := s.db.Query(selectSession, session.String())
	if err != nil {
		return nil, fmt.Errorf("store: replay: %w", err)
	}
	defer rows.Close()

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
			Ordinal: uint64(ordinal), At: time.UnixMilli(at), Event: e,
		})
	}
	return out, rows.Err()
}

// Sessions are what can be replayed, newest first.
func (s *Store) Sessions() ([]event.SessionSummary, error) {
	rows, err := s.db.Query(selectSessions)
	if err != nil {
		return nil, fmt.Errorf("store: sessions: %w", err)
	}
	defer rows.Close()

	var out []event.SessionSummary
	for rows.Next() {
		var (
			id     string
			name   string
			at     int64
			model  string
			events int
		)
		if err := rows.Scan(&id, &name, &at, &model, &events); err != nil {
			return nil, fmt.Errorf("store: scan session: %w", err)
		}
		parsed, err := uuid.Parse(id)
		if err != nil {
			continue // not ours to offer
		}
		out = append(out, event.SessionSummary{ID: parsed, Name: name, Model: model,
			Started: time.UnixMilli(at).UTC(), Events: events})
	}
	return out, rows.Err()
}

// Rename names a session. Not derived from the log, so it is the
// header's own. A name -resume would read as an id or as "last" is
// refused here rather than found not to work later.
func (s *Store) Rename(session uuid.UUID, name string) error {
	if err := usableName(name); err != nil {
		return err
	}
	if _, err := s.db.Exec(renameSession, name, session.String()); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("another session is already called %q", name)
		}
		return fmt.Errorf("store: rename: %w", err)
	}
	return nil
}

// ReservedName is the one word -resume reads as an instruction.
const ReservedName = "last"

func usableName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("a name cannot be blank")
	case strings.EqualFold(name, ReservedName):
		return fmt.Errorf("%q is what -resume calls the newest session, so a name would never be read", ReservedName)
	}
	if _, err := uuid.Parse(name); err == nil {
		return fmt.Errorf("%q reads as a session id, so a name would never be read", name)
	}
	return nil
}

// Delete forgets a session and every event in it. Irreversible, and
// reports whether there was anything there to forget.
func (s *Store) Delete(session uuid.UUID) (bool, error) {
	res, err := s.db.Exec(deleteSession, session.String())
	if err != nil {
		return false, fmt.Errorf("store: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: delete: %w", err)
	}
	return n > 0, nil
}

// Truncate drops everything after an ordinal: undo, on disk.
func (s *Store) Truncate(session uuid.UUID, after uint64) error {
	_, err := s.db.Exec(deleteAfter, session.String(), after)
	if err != nil {
		return fmt.Errorf("store: truncate: %w", err)
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

// DefaultPath is where a session's events go, beside the logs.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "detent", "events.db")
}
