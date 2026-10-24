-- A source can carry HUMAN tags, like a wiki page: the labels a reader files and filters sources by.
-- The source's <hash>.meta record is the truth; this column mirrors it so the source list and the
-- source-tags vocabulary can read it without opening one file per source. A JSON array, because a tag
-- has no identity of its own (the vocabulary decides what it is called).
-- +goose Up
ALTER TABLE sources ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE sources DROP COLUMN tags;
