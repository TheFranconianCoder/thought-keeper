-- +goose Up
-- The query agent reads a RECENCY window: the newest sources by arrival. The list's index is
-- (kind, imported_at), which cannot serve a bare `imported_at >= ?` range, so give the window its
-- own index. This is an index only; the shape the queries read is unchanged.
CREATE INDEX IF NOT EXISTS idx_sources_imported_at ON sources (imported_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_sources_imported_at;
