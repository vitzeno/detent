-- +goose Up
-- Per-output views are gone, and Decode refuses a kind it has no type for, so a
-- session still holding a view.ready record could not be replayed.
DELETE FROM events WHERE kind = 'view.ready';
