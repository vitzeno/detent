// Package store keeps a session's events on disk, so it can be
// replayed rather than reconstructed. Speaks event.Record only.
package store

import (
	"database/sql"
	"fmt"
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
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Append writes one record, keyed on the bus's ordinal, so writing
// the same one twice is the same row.
func (s *Store) Append(session uuid.UUID, r event.Record) error {
	payload, err := event.Encode(r.Event)
	if err != nil {
		return fmt.Errorf("store: encode %s: %w", r.Event.Kind(), err)
	}
	turn, call := event.Subject(r.Event)
	_, err = s.db.Exec(insert, session.String(), r.Ordinal, r.At.UnixMilli(),
		string(r.Event.Kind()), nullable(turn), nullable(call), payload)
	if err != nil {
		return fmt.Errorf("store: append %s: %w", r.Event.Kind(), err)
	}
	return nil
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
func (s *Store) Sessions() ([]Session, error) {
	rows, err := s.db.Query(selectSessions)
	if err != nil {
		return nil, fmt.Errorf("store: sessions: %w", err)
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var (
			id     string
			at     int64
			events int
		)
		if err := rows.Scan(&id, &at, &events); err != nil {
			return nil, fmt.Errorf("store: scan session: %w", err)
		}
		parsed, err := uuid.Parse(id)
		if err != nil {
			continue // not ours to offer
		}
		out = append(out, Session{ID: parsed, Started: time.UnixMilli(at), Events: events})
	}
	return out, rows.Err()
}

// Session is one resumable session, as a listing shows it.
type Session struct {
	ID      uuid.UUID
	Started time.Time
	Events  int
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
