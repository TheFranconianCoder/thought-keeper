-- name: UpsertPartState :exec
INSERT INTO distill_parts (source_id, node_id, state, reason, atoms, run_id, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source_id, node_id) DO UPDATE SET
    state = excluded.state,
    reason = excluded.reason,
    atoms = excluded.atoms,
    run_id = excluded.run_id,
    updated_at = excluded.updated_at;

-- name: ListPartStates :many
SELECT node_id, state, reason, atoms, updated_at FROM distill_parts WHERE source_id = ?;

-- name: DeletePartStates :exec
DELETE FROM distill_parts WHERE source_id = ?;

-- name: DeletePartState :exec
DELETE FROM distill_parts WHERE source_id = ? AND node_id = ?;

-- name: DeletePartsWithoutText :execrows
-- A part is a node that carries text. A record for a node that is gone or has no text is a stale
-- leftover (the synthetic root of an older tree, a part whose section was removed): it must not keep
-- a "done" light over nothing.
DELETE FROM distill_parts
WHERE EXISTS (
        SELECT 1 FROM source_nodes n WHERE n.source_id = distill_parts.source_id
      )
  AND NOT EXISTS (
        SELECT 1 FROM source_nodes n
        WHERE n.source_id = distill_parts.source_id
          AND n.node_id = distill_parts.node_id
          AND n.text_hash <> ''
      );
