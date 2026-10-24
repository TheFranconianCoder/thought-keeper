-- name: ListAtomsWithoutSource :many
SELECT a.id, a.claim
FROM atoms a
WHERE NOT EXISTS (SELECT 1 FROM atom_sources s WHERE s.atom_id = a.id)
ORDER BY a.updated_at DESC
LIMIT ?;

-- name: DeleteLintFindingsByLayer :exec
DELETE FROM lint_findings WHERE layer = ?;

-- name: InsertLintFinding :exec
INSERT INTO lint_findings (
    id, run_id, layer, kind, severity, subject_kind, subject_id, message, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListLintFindings :many
SELECT * FROM lint_findings ORDER BY created_at DESC LIMIT ? OFFSET ?;

-- name: ListLintFindingsByKind :many
SELECT * FROM lint_findings WHERE kind = ? ORDER BY created_at DESC LIMIT ? OFFSET ?;
