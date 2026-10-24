-- name: UpsertSource :exec
INSERT INTO sources (
    id, hash, kind, ext, path, url, title, bytes, pages, tree_state, imported_at, meta, bookmark, original_name
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
ON CONFLICT(hash) DO UPDATE SET
    kind = excluded.kind,
    ext = excluded.ext,
    path = excluded.path,
    bytes = excluded.bytes;

-- name: GetSourceByHash :one
SELECT * FROM sources WHERE hash = ?;

-- name: SetSourceSteps :exec
-- The mirror of the source's .meta file: which of the four steps are done, running or failed.
UPDATE sources SET steps = ? WHERE hash = ?;

-- name: SourceDerivedCounts :one
-- The derived numbers of ONE source, in the same shape the list computes for every source: the
-- detail view is opened FROM a row, so it has to show the same state that row showed instead of
-- zeros (a status label built on zeros reads "pending distill" for a derived source).
-- The five aggregates of one Sources page: claims, parts, finished parts, claims left for the
-- association layer, and raw provenance rows. Each is the same aggregate the list used to compute
-- over the whole table, restricted to the page's ids.
SELECT
    (SELECT count(DISTINCT a.id)
       FROM atom_sources s JOIN atoms a ON a.id = s.atom_id
      WHERE s.source_id = src.id) AS atom_count,
    (SELECT count(*)
       FROM atom_sources s
      WHERE s.source_id = src.id) AS provenance_rows,
    (SELECT count(*)
       FROM source_nodes n
      WHERE n.source_id = src.id AND n.text_hash != '') AS parts_total,
    (SELECT count(*)
       FROM distill_parts p
      WHERE p.source_id = src.id AND p.state = 'done') AS parts_done,
    (SELECT count(*)
       FROM distill_parts p
      WHERE p.source_id = src.id AND p.state = 'done'
        AND p.reason <> 'source_relative') AS parts_derived,
    (SELECT count(DISTINCT a.id)
       FROM atom_sources s
       JOIN atoms a ON a.id = s.atom_id
      WHERE s.source_id = src.id
        AND NOT EXISTS (SELECT 1 FROM cluster_atoms ca WHERE ca.atom_id = a.id)) AS atoms_left
FROM sources src
WHERE src.hash = ?;

-- name: CountSources :one
SELECT count(*) FROM sources;

-- name: CountSourcesByKind :one
SELECT count(*) FROM sources WHERE kind = ?;

-- name: ListSources :many
SELECT * FROM sources ORDER BY imported_at DESC LIMIT ? OFFSET ?;

-- name: ListSourcesByKind :many
SELECT * FROM sources WHERE kind = ? ORDER BY imported_at DESC LIMIT ? OFFSET ?;

-- name: ListSourcesByParent :many
-- The sources one source OWNS: the pages a sitemap declared. The owner fields are part of the
-- source's meta JSON (they are identity, not columns), so the read extracts them. Ordered by title so a tab reads like a
-- table of contents, not like an import log.
SELECT * FROM sources
WHERE json_valid(meta) AND json_extract(meta, '$.parent_hash') = sqlc.arg(parent_hash)
ORDER BY title COLLATE NOCASE, hash;

-- name: ListSourcesByWikiPage :many
-- The sources one WIKI PAGE brought into the vault: its linked URLs. The page's source representation
-- owns nothing itself; the page id is the owner it was recorded under.
SELECT * FROM sources
WHERE json_valid(meta) AND json_extract(meta, '$.wiki_page') = sqlc.arg(wiki_page)
ORDER BY title COLLATE NOCASE, hash;

-- name: CountSourcesByParent :one
SELECT count(*) FROM sources WHERE json_valid(meta) AND json_extract(meta, '$.parent_hash') = ?;

-- name: CountSourcesByWikiPage :one
SELECT count(*) FROM sources WHERE json_valid(meta) AND json_extract(meta, '$.wiki_page') = ?;

-- name: SetSourceTags :exec
UPDATE sources SET tags = sqlc.arg(tags) WHERE hash = sqlc.arg(hash);

-- name: ListSourceTags :many
-- The source-tags vocabulary observes every source's labels (human and derived). Ordered for a stable
-- report.
SELECT hash, tags, auto_tags FROM sources ORDER BY hash;

-- name: SetSourceAutoTags :exec
UPDATE sources SET auto_tags = sqlc.arg(auto_tags), auto_tags_at = sqlc.arg(auto_tags_at)
WHERE hash = sqlc.arg(hash);

-- name: DeleteSourceByHash :exec
DELETE FROM sources WHERE hash = ?;

-- name: ListSourceHashes :many
SELECT hash FROM sources;

-- name: ListSourceKinds :many
-- What every indexed source IS: the reindex needs the kind because a source's record may predate the
-- identity being recorded there, and the file name cannot tell a URL snapshot from a Markdown source.
SELECT hash, kind FROM sources;

-- name: SetSourceKindURL :exec
-- Repair a source's kind: a URL snapshot is stored as a plain .md, and its kind can be lost when the
-- index is rebuilt from the vault alone. The snapshot's own frontmatter says where it came from.
UPDATE sources SET kind = 'url', url = ? WHERE hash = ?;

-- name: FillURLSource :exec
-- A QUEUED url source (a wiki link whose fetch is still owed) has a row and a record but no object.
-- The fetch fills that row in place: the title the document named itself, the bookmark decision and
-- the snapshot metadata all arrive with the first successful fetch, and the address stays what it
-- was, so every reference to the link keeps working. This is deliberately NOT UpsertSource: that one
-- keeps the row's first identity (right for a reindex, wrong for a row that was never more than a
-- claim on a URL).
UPDATE sources SET kind = 'url', ext = ?, path = ?, bytes = ?, title = ?, original_name = ?, bookmark = ?, meta = ? WHERE hash = ?;

-- name: SetSourceURLState :exec
UPDATE sources SET url_status = ?, url_checked_at = ? WHERE hash = ?;

-- name: ListURLSources :many
SELECT * FROM sources WHERE kind = 'url' ORDER BY imported_at DESC;

-- name: LatestSourceByURL :one
SELECT * FROM sources WHERE url = ? ORDER BY imported_at DESC LIMIT 1;

-- name: SourceAtomCount :one
SELECT count(*)
FROM atom_sources s
JOIN sources src ON src.id = s.source_id
WHERE src.hash = ?;

-- name: ListSourcesWithoutAtoms :many
SELECT s.* FROM sources s
WHERE s.bookmark = 0
  AND s.tree_state = 'built'
  AND NOT EXISTS (SELECT 1 FROM atom_sources a WHERE a.source_id = s.id)
ORDER BY s.imported_at DESC;

-- name: ListSourcesWithUnclustered :many
-- Sources that still have claims no cluster owns yet. This is the association backlog: the pump and
-- the reconcile tick ask exactly this to decide whether step 4 has work.
SELECT s.* FROM sources s
WHERE s.bookmark = 0
  AND s.tree_state = 'built'
  AND EXISTS (SELECT 1 FROM atom_sources a WHERE a.source_id = s.id)
  AND EXISTS (
        SELECT 1 FROM atom_sources a
        JOIN atoms at ON at.id = a.atom_id
        WHERE a.source_id = s.id
          AND NOT EXISTS (SELECT 1 FROM cluster_atoms ca WHERE ca.atom_id = at.id)
  )
ORDER BY s.imported_at DESC;

-- name: ListSourcesWithOpenParts :many
-- Sources whose derivation is unfinished: they have text parts (nodes with text) that no recorded
-- done covers. This is what a long book looks like between runs; without it a restart would leave
-- the book half-derived, because "has no atoms" is only true before the FIRST run.
SELECT s.* FROM sources s
WHERE s.bookmark = 0
  AND s.tree_state = 'built'
  AND EXISTS (
        SELECT 1 FROM source_nodes n
        WHERE n.source_id = s.id
          AND n.text_hash != ''
          AND (
                -- no DONE record yet (no record at all, or an incomplete one)
                NOT EXISTS (
                  SELECT 1 FROM distill_parts t
                  WHERE t.source_id = s.id AND t.node_id = n.node_id AND t.state = 'done'
                )
                -- a part recorded done but with no claim citing it: its claims were lost, so the
                -- "done" record hangs on a derivation that is gone. A source_relative part is the
                -- exception: it correctly has no claim, so it is not open work.
                OR EXISTS (
                  SELECT 1 FROM distill_parts t
                  WHERE t.source_id = s.id AND t.node_id = n.node_id
                    AND t.state = 'done'
                    AND t.reason != 'source_relative'
                    AND NOT EXISTS (
                      SELECT 1 FROM atom_sources a
                      WHERE a.source_id = s.id AND a.node_id = n.node_id
                    )
                )
          )
  )
ORDER BY s.imported_at DESC;

-- name: CountSourceOpenParts :one
-- The per-source form of ListSourcesWithOpenParts. The pipeline asks it only when a step record is
-- ambiguous (distill ok, no claims, no recorded parts): one source's lost bookkeeping must not
-- decide the whole vault's place in the four steps.
SELECT count(*) FROM source_nodes n
WHERE n.source_id = (SELECT id FROM sources WHERE hash = sqlc.arg(hash))
  AND n.text_hash != ''
  AND (
        NOT EXISTS (
          SELECT 1 FROM distill_parts t
          WHERE t.source_id = n.source_id AND t.node_id = n.node_id AND t.state = 'done'
        )
        OR EXISTS (
          SELECT 1 FROM distill_parts t
          WHERE t.source_id = n.source_id AND t.node_id = n.node_id
            AND t.state = 'done'
            AND t.reason != 'source_relative'
            AND NOT EXISTS (
              SELECT 1 FROM atom_sources a
              WHERE a.source_id = n.source_id AND a.node_id = n.node_id
            )
        )
  );

-- name: ListSourcesWithNoTree :many
SELECT s.* FROM sources s
WHERE s.bookmark = 0
  AND s.tree_state = 'none'
ORDER BY s.imported_at DESC;

-- name: ResetSourceDerived :exec
-- A reset puts the derived source fields back where an intake leaves them: the title is the
-- file name again until the title step derives a new one, and the page count is unknown.
UPDATE sources SET title = ?, pages = NULL WHERE hash = ?;

-- name: SourceAtomCountsForSources :many
-- Claim counts for EXACTLY these sources. One list page must not pay for the GROUP BY over every
-- provenance row in the index.
SELECT s.source_id AS source_id, count(DISTINCT a.id) AS count
FROM atom_sources s
JOIN atoms a ON a.id = s.atom_id
WHERE s.source_id IN (sqlc.slice('ids'))
GROUP BY s.source_id;

-- name: SourceProvenanceCountsForSources :many
-- Raw provenance rows for EXACTLY these sources: when they exceed the resolvable claims, the index
-- is damaged and the UI can say so.
SELECT source_id, count(*) AS count FROM atom_sources
WHERE source_id IN (sqlc.slice('ids'))
GROUP BY source_id;

-- name: SourcePartTotalsForSources :many
SELECT source_id, count(*) AS count FROM source_nodes
WHERE text_hash != '' AND source_id IN (sqlc.slice('ids'))
GROUP BY source_id;

-- name: SourcePartsDoneForSources :many
SELECT source_id, count(*) AS count,
       count(CASE WHEN reason <> 'source_relative' THEN 1 END) AS derived
FROM distill_parts WHERE state = 'done' AND source_id IN (sqlc.slice('ids')) GROUP BY source_id;

-- name: SourceAtomsLeftForSources :many
SELECT s.source_id AS source_id, count(DISTINCT a.id) AS count
FROM atom_sources s
JOIN atoms a ON a.id = s.atom_id
WHERE NOT EXISTS (SELECT 1 FROM cluster_atoms ca WHERE ca.atom_id = a.id)
  AND s.source_id IN (sqlc.slice('ids'))
GROUP BY s.source_id;
