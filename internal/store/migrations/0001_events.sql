-- +goose Up
-- A session header and its log. The header comes from SessionStarted and
-- only its resumed count and name ever change, so it cannot drift.
CREATE TABLE sessions (
  id      TEXT    PRIMARY KEY,
  started INTEGER NOT NULL,
  model   TEXT    NOT NULL,
  judge   TEXT    NOT NULL DEFAULT '',
  sandbox INTEGER NOT NULL,
  network INTEGER NOT NULL,
  -- resumed is how many records the latest run began from
  resumed INTEGER NOT NULL DEFAULT 0,
  name    TEXT    NOT NULL DEFAULT ''
);

-- The log. State is what replaying it gives, so there is no second shape.
-- turn and call are lifted out of the payload so a query needs no scan.
CREATE TABLE events (
  session TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  ordinal INTEGER NOT NULL,
  at      INTEGER NOT NULL,
  kind    TEXT    NOT NULL,
  turn    TEXT,
  call    TEXT,
  payload BLOB    NOT NULL,
  PRIMARY KEY (session, ordinal)
);

CREATE INDEX events_turn ON events (session, turn);

-- A name has to pick out one session or -resume <name> is ambiguous.
-- Partial, because "" means unnamed and any number of those is fine.
CREATE UNIQUE INDEX sessions_name ON sessions (name) WHERE name != '';
