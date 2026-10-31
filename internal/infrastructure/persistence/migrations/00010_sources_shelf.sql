-- The reader's own marks on a source: a favorite (a shelf, no lifecycle) and the read-later queue
-- (open, done, and which interests queued it). The source's <hash>.meta record is the truth, as it
-- is for tags; these columns are the mirror the windowed list filters and sorts by.
--
-- `read_later_at` is when the source entered the queue and `read_later_done_at` when it left it
-- (NULL while it waits). `read_later_manual` says the reader put it there by hand, so "not this
-- interest" cannot remove an entry they made themselves. `read_later_via` is a JSON array of
-- interest slugs — the queue row's explanation and the interest filter's key.
--
-- This is an additive migration: an index written by the previous build gains the columns in place,
-- so the shape marker (which would demand a reindex) stays where it is.
-- +goose Up
ALTER TABLE sources ADD COLUMN favorite_at INTEGER;
ALTER TABLE sources ADD COLUMN read_later_at INTEGER;
ALTER TABLE sources ADD COLUMN read_later_done_at INTEGER;
ALTER TABLE sources ADD COLUMN read_later_manual INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sources ADD COLUMN read_later_via TEXT NOT NULL DEFAULT '[]';

CREATE INDEX IF NOT EXISTS idx_sources_read_later ON sources (read_later_at);
CREATE INDEX IF NOT EXISTS idx_sources_favorite ON sources (favorite_at);

-- +goose Down
DROP INDEX IF EXISTS idx_sources_favorite;
DROP INDEX IF EXISTS idx_sources_read_later;
ALTER TABLE sources DROP COLUMN read_later_via;
ALTER TABLE sources DROP COLUMN read_later_manual;
ALTER TABLE sources DROP COLUMN read_later_done_at;
ALTER TABLE sources DROP COLUMN read_later_at;
ALTER TABLE sources DROP COLUMN favorite_at;
