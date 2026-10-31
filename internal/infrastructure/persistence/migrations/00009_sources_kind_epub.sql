-- A source can be an EPUB now, so the index's CHECK has to accept the kind. SQLite cannot change a
-- CHECK constraint in place: the table is rebuilt (create → copy → drop → rename), which keeps every
-- existing row and every child row (source_nodes, atom_sources, distill_parts).
--
-- NO TRANSACTION on purpose: `PRAGMA foreign_keys` is a no-op inside a transaction, and the DROP would
-- otherwise cascade into the child tables and delete the provenance the index exists for. With the
-- pragma off, the children keep their source_id and the copy preserves every id.
-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;

CREATE TABLE sources_new (
    id            BLOB NOT NULL PRIMARY KEY CHECK (length(id) = 16),  -- ULID, internal handle
    hash          TEXT NOT NULL UNIQUE,       -- sha256 hex = content address
    kind          TEXT NOT NULL CHECK (kind IN ('md','pdf','txt','epub','url')),
    ext           TEXT NOT NULL,
    path          TEXT NOT NULL,
    url           TEXT,
    title         TEXT NOT NULL DEFAULT '',
    original_name TEXT NOT NULL DEFAULT '',
    bytes         INTEGER NOT NULL,
    pages         INTEGER,
    tree_state    TEXT NOT NULL DEFAULT 'none' CHECK (tree_state IN ('none','pending','built','failed')),
    steps         TEXT NOT NULL DEFAULT '{}',
    url_status    INTEGER,
    url_checked_at INTEGER,
    bookmark      INTEGER NOT NULL DEFAULT 0,
    imported_at   INTEGER NOT NULL,
    meta          TEXT NOT NULL DEFAULT '{}',
    tags          TEXT NOT NULL DEFAULT '[]',
    auto_tags     TEXT NOT NULL DEFAULT '[]',
    auto_tags_at  INTEGER
);

INSERT INTO sources_new (
    id, hash, kind, ext, path, url, title, original_name, bytes, pages, tree_state, steps,
    url_status, url_checked_at, bookmark, imported_at, meta, tags, auto_tags, auto_tags_at
)
SELECT
    id, hash, kind, ext, path, url, title, original_name, bytes, pages, tree_state, steps,
    url_status, url_checked_at, bookmark, imported_at, meta, tags, auto_tags, auto_tags_at
FROM sources;

DROP TABLE sources;
ALTER TABLE sources_new RENAME TO sources;
CREATE INDEX IF NOT EXISTS idx_sources_kind ON sources (kind, imported_at DESC);
CREATE INDEX IF NOT EXISTS idx_sources_imported_at ON sources (imported_at DESC);

PRAGMA foreign_keys = ON;

-- +goose Down
PRAGMA foreign_keys = OFF;

CREATE TABLE sources_old (
    id            BLOB NOT NULL PRIMARY KEY CHECK (length(id) = 16),
    hash          TEXT NOT NULL UNIQUE,
    kind          TEXT NOT NULL CHECK (kind IN ('md','pdf','txt','url')),
    ext           TEXT NOT NULL,
    path          TEXT NOT NULL,
    url           TEXT,
    title         TEXT NOT NULL DEFAULT '',
    original_name TEXT NOT NULL DEFAULT '',
    bytes         INTEGER NOT NULL,
    pages         INTEGER,
    tree_state    TEXT NOT NULL DEFAULT 'none' CHECK (tree_state IN ('none','pending','built','failed')),
    steps         TEXT NOT NULL DEFAULT '{}',
    url_status    INTEGER,
    url_checked_at INTEGER,
    bookmark      INTEGER NOT NULL DEFAULT 0,
    imported_at   INTEGER NOT NULL,
    meta          TEXT NOT NULL DEFAULT '{}',
    tags          TEXT NOT NULL DEFAULT '[]',
    auto_tags     TEXT NOT NULL DEFAULT '[]',
    auto_tags_at  INTEGER
);

INSERT INTO sources_old (
    id, hash, kind, ext, path, url, title, original_name, bytes, pages, tree_state, steps,
    url_status, url_checked_at, bookmark, imported_at, meta, tags, auto_tags, auto_tags_at
)
SELECT
    id, hash, kind, ext, path, url, title, original_name, bytes, pages, tree_state, steps,
    url_status, url_checked_at, bookmark, imported_at, meta, tags, auto_tags, auto_tags_at
FROM sources WHERE kind <> 'epub';

DROP TABLE sources;
ALTER TABLE sources_old RENAME TO sources;
CREATE INDEX IF NOT EXISTS idx_sources_kind ON sources (kind, imported_at DESC);
CREATE INDEX IF NOT EXISTS idx_sources_imported_at ON sources (imported_at DESC);

PRAGMA foreign_keys = ON;
