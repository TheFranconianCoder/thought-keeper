-- The paced refresh asks one question per gap: "which URL source has not been checked for the
-- longest?" The candidate query orders by `url_checked_at`, and without this index SQLite would
-- sort the whole URL set every minute. A partial index keeps it to the URL rows (the only kind the
-- cadence refreshes) and lets the query read the oldest first.
--
-- Additive and index-only: no shape change, so the user_version marker stays where it is.
-- +goose Up
CREATE INDEX IF NOT EXISTS idx_sources_url_checked
    ON sources (url_checked_at) WHERE kind = 'url';

-- +goose Down
DROP INDEX IF EXISTS idx_sources_url_checked;
