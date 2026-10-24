-- +goose Up
-- The whole schema in one file: this IS the current shape of the index, not the sum of the changes it
-- went through. The index is a derived, disposable cache (everything in it can be rebuilt from the
-- vault with `reindex` + `rebuild-trees`), so a schema change is a new migration — and a rename of a
-- vocabulary is done here, in the initial file, with the database thrown away.
--
-- Vocabulary used below: a RUN is one pipeline run (`op` is the class: `pipeline`, `query`, `lint`,
-- `vocabulary`; `step` names which of the four steps it executed: capture, outline, distill, cluster).
-- A source's distilling work unit is a PART (one node that carries text), recorded in `distill_parts`.
--
-- There is NO page/note layer. The three layers are sources (immutable), atoms (claims, the truth)
-- and clusters (titleless associations derived from the claims). Clusters are a CACHE: the graph over
-- the claims is deterministic, so a reindex or a re-derivation rebuilds `clusters/` entirely.

CREATE TABLE IF NOT EXISTS sources (
    id            BLOB NOT NULL PRIMARY KEY CHECK (length(id) = 16),  -- ULID, internal handle
    hash          TEXT NOT NULL UNIQUE,       -- sha256 hex = content address
    kind          TEXT NOT NULL CHECK (kind IN ('md','pdf','txt','url')),
    ext           TEXT NOT NULL,
    path          TEXT NOT NULL,
    url           TEXT,
    title         TEXT NOT NULL DEFAULT '',
    -- The raw input's own name (the dropped file, or the URL): shown ADDITIONALLY, because the title
    -- belongs to the document and is chosen from its content.
    original_name TEXT NOT NULL DEFAULT '',
    bytes         INTEGER NOT NULL,
    pages         INTEGER,
    tree_state    TEXT NOT NULL DEFAULT 'none' CHECK (tree_state IN ('none','pending','built','failed')),
    -- The three lights of the DOCUMENT steps (capture, outline, distill), mirrored from the vault's
    -- <hash>.meta file. The file is the truth; `reindex` rebuilds this column from it.
    steps         TEXT NOT NULL DEFAULT '{}',
    -- A URL's last HTTP status and check time: "when was it updated and does it still exist".
    url_status    INTEGER,
    url_checked_at INTEGER,
    -- A collected link, not indexed: no tree, no atoms, never distilled.
    bookmark      INTEGER NOT NULL DEFAULT 0,
    imported_at   INTEGER NOT NULL,
    meta          TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_sources_kind ON sources (kind, imported_at DESC);

CREATE TABLE IF NOT EXISTS source_nodes (
    source_id  BLOB NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    node_id    TEXT NOT NULL,               -- stable address, e.g. '0.2.1'
    parent_id  TEXT NOT NULL DEFAULT '',
    depth      INTEGER NOT NULL,
    ord        INTEGER NOT NULL,
    title      TEXT NOT NULL,
    summary    TEXT NOT NULL DEFAULT '',
    start_page INTEGER,
    end_page   INTEGER,
    -- The hash of the node's text; empty for a node without text. A node WITH text is one part (one
    -- distilling work unit), so this is what "how many parts does this source have" counts.
    text_hash  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (source_id, node_id)
);
CREATE INDEX IF NOT EXISTS idx_source_nodes_parent ON source_nodes (source_id, parent_id, ord);

CREATE TABLE IF NOT EXISTS atoms (
    id          BLOB NOT NULL PRIMARY KEY CHECK (length(id) = 16),
    topic       TEXT NOT NULL,
    path        TEXT NOT NULL UNIQUE,
    claim       TEXT NOT NULL,
    body        TEXT NOT NULL,
    frontmatter TEXT NOT NULL DEFAULT '{}',
    type        TEXT NOT NULL DEFAULT 'claim',   -- fact|definition|claim|example|counterpoint|quote
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','superseded','disputed')),
    body_hash   TEXT NOT NULL,
    size        INTEGER NOT NULL,
    mtime       INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    indexed_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_atoms_topic ON atoms (topic, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_atoms_status ON atoms (status);

CREATE TABLE IF NOT EXISTS atom_links (
    from_id     BLOB NOT NULL REFERENCES atoms(id) ON DELETE CASCADE,
    raw_target  TEXT NOT NULL,
    to_id       BLOB REFERENCES atoms(id) ON DELETE SET NULL,   -- NULL = red link
    alias       TEXT NOT NULL DEFAULT '',
    heading     TEXT NOT NULL DEFAULT '',
    occurrences INTEGER NOT NULL DEFAULT 1,
    position    INTEGER NOT NULL,
    PRIMARY KEY (from_id, raw_target, heading)
);
CREATE INDEX IF NOT EXISTS idx_atom_links_to ON atom_links (to_id);

-- Provenance: atom ↔ source node. ocr marks a lossy (OCR) extraction.
CREATE TABLE IF NOT EXISTS atom_sources (
    atom_id   BLOB NOT NULL REFERENCES atoms(id) ON DELETE CASCADE,
    source_id BLOB NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    node_id   TEXT NOT NULL DEFAULT '',
    ocr       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (atom_id, source_id, node_id)
);
CREATE INDEX IF NOT EXISTS idx_atom_sources_source ON atom_sources (source_id);

-- The association layer: one titleless group of related claims. It is a CACHE (the claims are the
-- truth); a re-derivation replaces it. No title, kind, tags, state or successor: those existed only
-- to name a wiki page, and the page layer is gone.
CREATE TABLE IF NOT EXISTS clusters (
    id         BLOB NOT NULL PRIMARY KEY CHECK (length(id) = 16),
    signature  TEXT NOT NULL DEFAULT '',   -- stable hash of the sorted member ids
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- What a cluster groups. A claim may sit in several clusters (many-to-many).
CREATE TABLE IF NOT EXISTS cluster_atoms (
    cluster_id BLOB NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    atom_id    BLOB NOT NULL REFERENCES atoms(id) ON DELETE CASCADE,
    PRIMARY KEY (cluster_id, atom_id)
);
CREATE INDEX IF NOT EXISTS idx_cluster_atoms_atom ON cluster_atoms (atom_id);

-- Append-only audit tables. NOT rebuilt by `reindex`.
CREATE TABLE IF NOT EXISTS runs (
    id                BLOB NOT NULL PRIMARY KEY CHECK (length(id) = 16),
    -- The class of work. Every pipeline run (document steps AND the cluster step) is a `pipeline` run;
    -- `step` says which step it executed.
    op                TEXT NOT NULL CHECK (op IN ('pipeline','query','lint','vocabulary')),
    step              TEXT NOT NULL DEFAULT '',   -- capture|outline|distill|cluster
    model             TEXT NOT NULL,
    target_id         BLOB,                       -- source hash, or "cluster"
    status            TEXT NOT NULL CHECK (status IN ('running','succeeded','failed','aborted')),
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens      INTEGER NOT NULL DEFAULT 0,
    iterations        INTEGER NOT NULL DEFAULT 0,
    answer            TEXT NOT NULL DEFAULT '',
    -- What a QUERY run was asked (empty for every other run).
    question          TEXT NOT NULL DEFAULT '',
    error             TEXT NOT NULL DEFAULT '',
    started_at        INTEGER NOT NULL,
    ended_at          INTEGER
);
CREATE INDEX IF NOT EXISTS idx_runs_op ON runs (op, started_at DESC);

CREATE TABLE IF NOT EXISTS run_steps (
    run_id        BLOB NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    seq           INTEGER NOT NULL,
    tool          TEXT NOT NULL,
    args_digest   TEXT NOT NULL,
    result_digest TEXT NOT NULL,
    is_error      INTEGER NOT NULL DEFAULT 0,
    duration_ms   INTEGER NOT NULL,
    PRIMARY KEY (run_id, seq)
);

-- How far the distilling of ONE part of a source has come. "Has atoms" is not the same as
-- "finished": a run can stop mid-part (iteration cap, timeout, error), and a resumed run must redo
-- such a part — never skip it. This is a record of what happened, not a cache of what exists.
CREATE TABLE IF NOT EXISTS distill_parts (
    source_id  BLOB NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    node_id    TEXT NOT NULL,               -- the part's node id (one node with text = one part)
    state      TEXT NOT NULL CHECK (state IN ('done','incomplete')),
    reason     TEXT NOT NULL DEFAULT '',    -- max_iterations | error | aborted | no_claims | …
    atoms      INTEGER NOT NULL DEFAULT 0,
    run_id     BLOB,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (source_id, node_id)
);

CREATE TABLE IF NOT EXISTS lint_findings (
    id           BLOB NOT NULL PRIMARY KEY CHECK (length(id) = 16),
    run_id       BLOB REFERENCES runs(id) ON DELETE SET NULL,
    layer        TEXT NOT NULL CHECK (layer IN ('programmatic','llm')),
    kind         TEXT NOT NULL,   -- ghost_link|orphan|provenance_gap|not_standalone|membership_gap|…
    severity     TEXT NOT NULL CHECK (severity IN ('info','warning','error')),
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('atom','cluster','source')),
    subject_id   BLOB,
    message      TEXT NOT NULL,
    created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_lint_findings_kind ON lint_findings (kind, severity, created_at DESC);

-- FTS5: the lexical half of vectorless retrieval. The tables are external-content
-- (`content='…'`), so the index is maintained by triggers that `persistence.EnsureFTS` creates and
-- rebuilds on open — this file only declares the tables.
CREATE VIRTUAL TABLE IF NOT EXISTS atoms_fts        USING fts5 (claim, body, content='atoms',        content_rowid='rowid');
CREATE VIRTUAL TABLE IF NOT EXISTS source_nodes_fts USING fts5 (title, summary, content='source_nodes', content_rowid='rowid');

-- The SHAPE this file describes, written into the database itself so a build can tell whether the
-- index in hand is the one it knows how to read and write. goose cannot answer that: a change of
-- VOCABULARY happens in THIS file (the index is derived and disposable, so it is thrown away rather
-- than patched), and goose does not run an applied migration again — a database written by an older
-- build therefore keeps its old shape while the queries move on, and every write dies on a constraint
-- nobody can read. Bump this number with the shape, in this file.
PRAGMA user_version = 20261022;

-- +goose Down
DROP TABLE IF EXISTS source_nodes_fts;
DROP TABLE IF EXISTS atoms_fts;
DROP TABLE IF EXISTS lint_findings;
DROP TABLE IF EXISTS distill_parts;
DROP TABLE IF EXISTS run_steps;
DROP TABLE IF EXISTS runs;
DROP TABLE IF EXISTS cluster_atoms;
DROP TABLE IF EXISTS clusters;
DROP TABLE IF EXISTS atom_sources;
DROP TABLE IF EXISTS atom_links;
DROP TABLE IF EXISTS atoms;
DROP TABLE IF EXISTS source_nodes;
DROP TABLE IF EXISTS sources;
