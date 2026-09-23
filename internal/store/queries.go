package store

const (
	// A header is written once and only its resumed count moves, so
	// a second run of the same session does not restart its clock.
	upsertSession = `INSERT INTO sessions (id, started, model, sandbox, network, resumed)
	  VALUES (?, ?, ?, ?, ?, ?)
	  ON CONFLICT(id) DO UPDATE SET resumed = excluded.resumed`

	insert = `INSERT OR REPLACE INTO events
	  (session, ordinal, at, kind, turn, call, payload) VALUES (?, ?, ?, ?, ?, ?, ?)`

	selectSession = `SELECT ordinal, at, kind, payload FROM events
	  WHERE session = ? ORDER BY ordinal`

	// The count comes off the events primary key rather than a scan.
	selectSessions = `SELECT s.id, s.started, s.model, COUNT(e.ordinal)
	  FROM sessions s LEFT JOIN events e ON e.session = s.id
	  GROUP BY s.id ORDER BY s.started DESC`

	deleteAfter = `DELETE FROM events WHERE session = ? AND ordinal > ?`
)
