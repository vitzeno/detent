-- +goose Up
-- The schema v0.5.0 left, its four migrations folded into one. An older
-- database is refused, since v0.5.0 brings it this far.

-- A session header and its log. The header comes from SessionStarted and
-- only its resumed count, name and schema ever change, so it cannot drift.
CREATE TABLE sessions (
  id      TEXT    PRIMARY KEY,
  started INTEGER NOT NULL,
  model   TEXT    NOT NULL,
  judge   TEXT    NOT NULL DEFAULT '',
  sandbox INTEGER NOT NULL,
  network INTEGER NOT NULL,
  -- resumed is how many records the latest run began from
  resumed INTEGER NOT NULL DEFAULT 0,
  name    TEXT    NOT NULL DEFAULT '',
  -- which detent started it, and the schema its records are in
  detent  TEXT    NOT NULL DEFAULT '',
  schema  INTEGER NOT NULL DEFAULT 1
);

-- The log, which state is replayed from. turn, tool_call and agent are lifted
-- out for queries, agent NULL for the root and for a child's later calls.
CREATE TABLE events (
  session   TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  ordinal   INTEGER NOT NULL,
  at        INTEGER NOT NULL,
  kind      TEXT    NOT NULL,
  turn      TEXT,
  tool_call TEXT,
  payload   BLOB    NOT NULL,
  agent     TEXT,
  PRIMARY KEY (session, ordinal)
);

CREATE INDEX events_turn ON events (session, turn);
CREATE INDEX events_agent ON events (session, agent);

-- A name has to pick out one session or -resume <name> is ambiguous.
-- Partial, because "" means unnamed and any number of those is fine.
CREATE UNIQUE INDEX sessions_name ON sessions (name) WHERE name != '';
