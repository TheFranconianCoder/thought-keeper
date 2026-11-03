-- The date a source declares for itself: a URL page's own publication (its metadata, a <time>
-- element, or the date under the title), a feed item's pubDate, or a wiki page's last edit. It is
-- NOT the arrival time (`imported_at`) and it is never read out of the body of an article.
--
-- `published_from` names the layer that found it (feed, meta, time, text, wiki): an automatism that
-- cannot say where it got a date from is one the reader cannot trust.
--
-- Additive: an index written by the previous build gains the columns in place, so the shape marker
-- (which would demand a reindex) stays where it is.
-- +goose Up
ALTER TABLE sources ADD COLUMN published_at INTEGER;
ALTER TABLE sources ADD COLUMN published_from TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sources DROP COLUMN published_from;
ALTER TABLE sources DROP COLUMN published_at;
