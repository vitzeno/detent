-- Which agent a record is about, NULL for the root, which is every record
-- before this. A child's later tool call records stay NULL here too, and are
-- found through their proposal's tool_call.
ALTER TABLE events ADD COLUMN agent TEXT;

CREATE INDEX events_agent ON events (session, agent);
