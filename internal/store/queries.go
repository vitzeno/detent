package store

const (
	// A later run moves only resumed and schema, so a header keeps its start
	// time and the detent that began it.
	upsertSession = `INSERT INTO sessions (id, started, model, judge, sandbox, network, resumed, detent, schema)
	  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	  ON CONFLICT(id) DO UPDATE SET resumed = excluded.resumed, schema = excluded.schema`

	// A name is the human's, so a later run must not overwrite it.
	renameSession = `UPDATE sessions SET name = ? WHERE id = ?`

	insert = `INSERT OR REPLACE INTO events
	  (session, ordinal, at, kind, turn, tool_call, agent, payload) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

	selectSession = `SELECT ordinal, at, kind, payload FROM events
	  WHERE session = ? ORDER BY ordinal`

	// Both come off the events primary key rather than a scan: ordinals only
	// grow, so a session's highest is when it was last used.
	selectSessions = `SELECT s.id, s.name, s.started, s.model, COUNT(e.ordinal),
	  COALESCE((SELECT at FROM events WHERE session = s.id ORDER BY ordinal DESC LIMIT 1), s.started) AS used
	  FROM sessions s LEFT JOIN events e ON e.session = s.id
	  GROUP BY s.id ORDER BY used DESC`

	// The events go with it, since the foreign key cascades.
	deleteSession = `DELETE FROM sessions WHERE id = ?`
)
