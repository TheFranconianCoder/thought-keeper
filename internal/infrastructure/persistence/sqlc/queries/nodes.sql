-- name: SetSourceTreeState :exec
UPDATE sources SET tree_state = ? WHERE hash = ?;

-- name: SetSourceTitle :exec
UPDATE sources SET title = ? WHERE hash = ?;

-- name: SetSourceHash :exec
-- Moves a source to the address it should have had (a URL source to its URL's hash). The row keeps its
-- ID, so everything that references the source by ID (its provenance rows above all) survives.
UPDATE sources SET hash = ?, path = ? WHERE hash = ?;

-- name: SetSourceMeta :exec
-- The URL snapshot metadata (content hash, favicon, excerpt, site) as the vault's record has it. A
-- reindex restores it here: without this the index-only copy was lost whenever index.db was rebuilt,
-- and every URL then looked changed to the refresher.
UPDATE sources SET meta = ? WHERE hash = ?;

-- name: SetSourcePages :exec
UPDATE sources SET pages = ? WHERE hash = ?;

-- name: DeleteSourceNodes :exec
DELETE FROM source_nodes WHERE source_id = ?;

-- name: InsertSourceNode :exec
INSERT INTO source_nodes (
    source_id, node_id, parent_id, depth, ord, title, summary, start_page, end_page, text_hash
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: ListSourceNodes :many
SELECT source_id, node_id, parent_id, depth, ord, title, summary, start_page, end_page, text_hash
FROM source_nodes
WHERE source_id = ?
ORDER BY ord;
