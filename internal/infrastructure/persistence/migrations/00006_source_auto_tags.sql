-- The agent's coarse labels for a source, kept apart from the human's (like a wiki page's auto_tags).
-- The modal: the source's <hash>.meta record is the truth and a reindex restores them; the columns are
-- the mirror the source list and the source-tags vocabulary read. auto_tags_at (Unix millis, NULL =
-- never tagged) is the backfill marker: a source whose derivation is complete and that was never
-- tagged is owed one derivation pass, a source tagged with an empty answer is not.
-- +goose Up
ALTER TABLE sources ADD COLUMN auto_tags TEXT NOT NULL DEFAULT '[]';
ALTER TABLE sources ADD COLUMN auto_tags_at INTEGER;

-- +goose Down
ALTER TABLE sources DROP COLUMN auto_tags_at;
ALTER TABLE sources DROP COLUMN auto_tags;
