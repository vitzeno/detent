-- A session header and its log. The header is written once from the
-- SessionStarted fact and never updated, so it cannot drift from the
-- log the way a rolling projection would.
CREATE TABLE sessions (
  id      TEXT    PRIMARY KEY,
  started INTEGER NOT NULL,
  model   TEXT    NOT NULL,
  sandbox INTEGER NOT NULL,
  network INTEGER NOT NULL,
  -- resumed is how many records the latest run began from, so a
  -- header says whether a session has been picked up again.
  resumed INTEGER NOT NULL DEFAULT 0
);

-- The log. State is what replaying it gives you, so there is no
-- second shape to keep in step with the structs.
--
-- (session, ordinal) is the natural key: an ordinal only means
-- anything within its session. turn and call are lifted out of the
-- payload because "everything about this request" is the query
-- anyone runs, and it should not need a payload scan.
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
