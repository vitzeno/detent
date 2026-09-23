package store

const (
	insert = `INSERT OR REPLACE INTO events
	  (session, ordinal, at, kind, turn, call, payload) VALUES (?, ?, ?, ?, ?, ?, ?)`

	selectSession = `SELECT ordinal, at, kind, payload FROM events
	  WHERE session = ? ORDER BY ordinal`

	selectSessions = `SELECT session, MIN(at), COUNT(*) FROM events
	  GROUP BY session ORDER BY MIN(at) DESC`

	deleteAfter = `DELETE FROM events WHERE session = ? AND ordinal > ?`
)
