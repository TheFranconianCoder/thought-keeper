-- name: GetSourceIDByHash :one
SELECT id FROM sources WHERE hash = ?;

-- name: UpsertAtom :exec
INSERT INTO atoms (
    id, topic, path, claim, body, frontmatter, type, status, body_hash, size, mtime,
    created_at, updated_at, indexed_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
ON CONFLICT(id) DO UPDATE SET
    topic = excluded.topic,
    path = excluded.path,
    claim = excluded.claim,
    body = excluded.body,
    frontmatter = excluded.frontmatter,
    type = excluded.type,
    status = excluded.status,
    body_hash = excluded.body_hash,
    size = excluded.size,
    mtime = excluded.mtime,
    updated_at = excluded.updated_at,
    indexed_at = excluded.indexed_at;

-- name: GetAtom :one
SELECT * FROM atoms WHERE id = ?;

-- name: ListAtoms :many
SELECT * FROM atoms ORDER BY updated_at DESC LIMIT ? OFFSET ?;

-- name: ListAtomsByTopic :many
SELECT * FROM atoms WHERE topic = ? ORDER BY updated_at DESC LIMIT ? OFFSET ?;

-- name: ListClaimTopics :many
SELECT topic, count(*) AS count FROM atoms GROUP BY topic ORDER BY topic;

-- name: DeleteAtomSources :exec
DELETE FROM atom_sources WHERE atom_id = ?;

-- name: InsertAtomSource :exec
INSERT OR REPLACE INTO atom_sources (atom_id, source_id, node_id, ocr) VALUES (?, ?, ?, ?);

-- name: DeleteAtomLinks :exec
DELETE FROM atom_links WHERE from_id = ?;

-- name: ListAtomSources :many
SELECT s.hash AS hash, s.title AS title, n.node_id AS node_id, n.ocr AS ocr
FROM atom_sources n
JOIN sources s ON s.id = n.source_id
WHERE n.atom_id = ?;

-- name: ListAtomsBySource :many
-- DISTINCT: an atom citing several nodes of the same source is still ONE claim in the list.
SELECT DISTINCT a.id, a.topic, a.path, a.claim, a.body, a.frontmatter, a.type, a.status,
       a.body_hash, a.size, a.mtime, a.created_at, a.updated_at, a.indexed_at
FROM atoms a
JOIN atom_sources s ON a.id = s.atom_id
JOIN sources src ON src.id = s.source_id
WHERE src.hash = ?
ORDER BY a.updated_at DESC;

-- name: InsertAtomLink :exec
INSERT OR REPLACE INTO atom_links (from_id, raw_target, to_id, alias, heading, occurrences, position)
VALUES (?, ?, ?, ?, ?, 1, ?);

-- name: SupersedeAtomsBySource :execrows
UPDATE atoms SET status = 'superseded', updated_at = ?
WHERE id IN (
    SELECT a.id FROM atoms a
    JOIN atom_sources s ON a.id = s.atom_id
    JOIN sources src ON src.id = s.source_id
    WHERE src.hash = ?
);

-- name: DeleteAtom :exec
DELETE FROM atoms WHERE id = ?;

-- name: CountAtomSources :one
SELECT count(*) FROM atom_sources WHERE atom_id = ?;

-- name: NodeRefsBySource :many
SELECT n.atom_id AS atom_id, n.node_id AS node_id
FROM atom_sources n
JOIN sources s ON s.id = n.source_id
WHERE s.hash = ?;

-- name: ResolveAtomLinks :execrows
-- An atom's [[Claim Text]] link is resolved by the claim it quotes: the claim IS an atom's title.
-- A target that names nothing stays red (to_id NULL) until somebody writes the claim it points at.
UPDATE atom_links
SET to_id = (
    SELECT a.id FROM atoms a
    WHERE lower(a.claim) = lower(atom_links.raw_target)
    LIMIT 1
)
WHERE to_id IS NULL
  AND EXISTS (
    SELECT 1 FROM atoms a
    WHERE lower(a.claim) = lower(atom_links.raw_target)
  );

-- name: ListUnresolvedAtomLinks :many
SELECT l.from_id, a.claim AS from_title, l.raw_target
FROM atom_links l
JOIN atoms a ON a.id = l.from_id
WHERE l.to_id IS NULL
LIMIT ?;

-- name: AtomSourcesForAtoms :many
-- The provenance of EXACTLY these atoms. The association pass reads the corpus as a stream and
-- needs no per-page join; a LIST page needs its own rows only. Reading 170k provenance rows to
-- decorate 50 is what made one small request cost a second and a half.
SELECT a.atom_id, s.hash, a.node_id, a.ocr
FROM atom_sources a
JOIN sources s ON s.id = a.source_id
WHERE a.atom_id IN (sqlc.slice('ids'));

-- name: AtomLinksForAtoms :many
-- The links of EXACTLY these atoms: hydration carries what a SAVE would replace, for the page it
-- returns and nothing else.
SELECT from_id, raw_target, to_id, alias, heading, occurrences, position
FROM atom_links
WHERE from_id IN (sqlc.slice('ids'));

-- name: AtomSourceCountsForAtoms :many
-- How many sources each of THESE atoms cites: a page's counts, never the table's.
SELECT atom_id, count(*) AS count FROM atom_sources
WHERE atom_id IN (sqlc.slice('ids'))
GROUP BY atom_id;

-- name: ListAtomsOldest :many
-- The OLDEST claims, bounded: the topic repair walks the vault oldest-first and a pass that reads
-- the whole corpus to process a budget of a few thousand pays the vault for a page of it. The order
-- is (updated_at, id): a re-labelled claim moves to the back of the queue by its new timestamp, so
-- the queue is the clock. Requires idx_atoms_updated.
SELECT id, topic, claim, body, updated_at FROM atoms
ORDER BY updated_at ASC, id ASC
LIMIT ?;
