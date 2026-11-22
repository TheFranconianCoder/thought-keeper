-- The sidebar and the Start pulse ask "how many bookmarks" on every /status read. Without an
-- index that is a scan of every source row; the badge is the one number a reader expects to be
-- instant. A partial index keeps it to the bookmark rows, which are few by nature.
--
-- Additive and index-only: no shape change, so the user_version marker stays where it is.
-- +goose Up
CREATE INDEX IF NOT EXISTS idx_sources_bookmark
    ON sources (bookmark) WHERE bookmark = 1;

-- +goose Down
DROP INDEX IF EXISTS idx_sources_bookmark;
