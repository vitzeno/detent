package store

// One append-only table. State is what replaying it gives you, so
// there is no second shape to keep in step with the structs.
//
// turn and call are lifted out of the payload because "everything
// about this request" is the query anyone runs, and it should not
// need a payload scan.
const schema = `
CREATE TABLE IF NOT EXISTS events (
  session  TEXT    NOT NULL,
  ordinal  INTEGER NOT NULL,
  at       INTEGER NOT NULL,
  kind     TEXT    NOT NULL,
  turn     TEXT,
  call     TEXT,
  payload  BLOB    NOT NULL,
  PRIMARY KEY (session, ordinal)
);
CREATE INDEX IF NOT EXISTS events_turn ON events (session, turn);
`

const (
	insert = `INSERT OR REPLACE INTO events
	  (session, ordinal, at, kind, turn, call, payload) VALUES (?, ?, ?, ?, ?, ?, ?)`

	selectSession = `SELECT ordinal, at, kind, payload FROM events
	  WHERE session = ? ORDER BY ordinal`

	selectSessions = `SELECT session, MIN(at), COUNT(*) FROM events
	  GROUP BY session ORDER BY MIN(at) DESC`

	deleteAfter = `DELETE FROM events WHERE session = ? AND ordinal > ?`
)
