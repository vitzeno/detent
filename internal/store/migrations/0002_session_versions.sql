-- Which detent started a session, and which schema its records are in:
-- the version they were written or last rewritten at. Older rows are 1.
ALTER TABLE sessions ADD COLUMN detent TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN schema INTEGER NOT NULL DEFAULT 1;
