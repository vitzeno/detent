-- One append-only table. State is what replaying it gives you, so
-- there is no second shape to keep in step with the structs.
--
-- turn and call are lifted out of the payload because "everything
-- about this request" is the query anyone runs, and it should not
-- need a payload scan.
CREATE TABLE events (
  session  TEXT    NOT NULL,
  ordinal  INTEGER NOT NULL,
  at       INTEGER NOT NULL,
  kind     TEXT    NOT NULL,
  turn     TEXT,
  call     TEXT,
  payload  BLOB    NOT NULL,
  PRIMARY KEY (session, ordinal)
);

CREATE INDEX events_turn ON events (session, turn);
