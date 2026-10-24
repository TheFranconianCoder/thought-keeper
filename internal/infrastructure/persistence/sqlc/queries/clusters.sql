-- name: UpsertCluster :exec
INSERT INTO clusters (id, signature, created_at, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    signature = excluded.signature,
    updated_at = excluded.updated_at;

-- name: DeleteCluster :exec
DELETE FROM clusters WHERE id = ?;

-- name: GetCluster :one
SELECT * FROM clusters WHERE id = ?;

-- name: ListClusters :many
SELECT * FROM clusters ORDER BY id LIMIT ? OFFSET ?;

-- name: DeleteClusterAtoms :exec
DELETE FROM cluster_atoms WHERE cluster_id = ?;

-- name: InsertClusterAtom :exec
INSERT OR REPLACE INTO cluster_atoms (cluster_id, atom_id) VALUES (?, ?);

-- name: ListClusterAtoms :many
SELECT atom_id FROM cluster_atoms WHERE cluster_id = ? ORDER BY atom_id;

-- name: ListClusterIDsForAtom :many
SELECT cluster_id FROM cluster_atoms WHERE atom_id = ? ORDER BY cluster_id;

-- name: ClusterMembershipsForAtoms :many
-- Membership of EXACTLY these claims: a list row asks "which clusters does this claim travel in?"
-- for the page it returns, not for the whole vault.
SELECT cluster_id, atom_id FROM cluster_atoms
WHERE atom_id IN (sqlc.slice('ids'))
ORDER BY atom_id, cluster_id;

-- name: ClusterMembershipsForClusters :many
-- Membership of EXACTLY these clusters: a paged list read must not load every membership row in
-- the index to decorate one page.
SELECT cluster_id, atom_id FROM cluster_atoms
WHERE cluster_id IN (sqlc.slice('ids'))
ORDER BY cluster_id, atom_id;

-- name: CountClusters :one
SELECT count(*) FROM clusters;

-- name: CountUnclusteredAtoms :one
SELECT count(*) FROM atoms a
WHERE NOT EXISTS (SELECT 1 FROM cluster_atoms ca WHERE ca.atom_id = a.id);
