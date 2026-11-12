-- The last time a URL snapshot's READABLE CONTENT actually changed. `url_checked_at` says when the
-- page was last FETCHED; it cannot tell a page that answered the same from one that was rewritten.
-- The Sources list orders by ACTIVITY (the arrival, or the change), so a changed page rises like an
-- arrival does instead of staying buried at its old arrival date.
--
-- Additive: an index written by the previous build gains the column and the indexes in place, so the
-- shape marker (which would demand a reindex) stays where it is.
-- +goose Up
ALTER TABLE sources ADD COLUMN url_changed_at INTEGER;

-- The list's order: the last real change when there is one, otherwise the arrival. The expression
-- index carries the tiebreakers too — with only the first key, SQLite builds a temp B-tree for the
-- rest. The partial index serves the "changed" filter's count.
CREATE INDEX IF NOT EXISTS idx_sources_activity
    ON sources (COALESCE(url_changed_at, imported_at) DESC, imported_at DESC, hash DESC);
CREATE INDEX IF NOT EXISTS idx_sources_url_changed
    ON sources (url_changed_at DESC) WHERE url_changed_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_sources_url_changed;
DROP INDEX IF EXISTS idx_sources_activity;
ALTER TABLE sources DROP COLUMN url_changed_at;
