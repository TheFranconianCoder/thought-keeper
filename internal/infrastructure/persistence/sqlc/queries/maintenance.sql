-- Orphan repair: rows whose parent vanished while foreign keys were off. Deleting them makes
-- the derived counts agree with what can actually be resolved.

-- name: DeleteOrphanAtomSources :execrows
DELETE FROM atom_sources
WHERE atom_id NOT IN (SELECT id FROM atoms) OR source_id NOT IN (SELECT id FROM sources);

-- name: DeleteOrphanClusterAtoms :execrows
DELETE FROM cluster_atoms
WHERE cluster_id NOT IN (SELECT id FROM clusters) OR atom_id NOT IN (SELECT id FROM atoms);

-- name: DeleteOrphanClusters :execrows
DELETE FROM clusters WHERE id NOT IN (SELECT cluster_id FROM cluster_atoms);

-- name: DeleteOrphanAtomLinks :execrows
DELETE FROM atom_links WHERE from_id NOT IN (SELECT id FROM atoms);

-- name: DeleteOrphanSourceNodes :execrows
DELETE FROM source_nodes WHERE source_id NOT IN (SELECT id FROM sources);
