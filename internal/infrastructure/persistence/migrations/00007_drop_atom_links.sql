-- A claim body has no links. A double-bracket span in claim prose is syntax quoted from a source (a
-- C attribute, a shell test, a grammar fragment), not an edge; claims are associated by their
-- cluster, never by a link. The table and the findings that reported its red links go together.
-- +goose Up
DROP TABLE IF EXISTS atom_links;

DELETE FROM lint_findings WHERE kind = 'ghost_link';

-- The table is gone, so the shape the queries expect has moved with it: a build from before this
-- migration would die on a missing table, and the marker has to name the reason.
PRAGMA user_version = 20261027;

-- +goose Down
-- The table is derivable from a body only under a rule that no longer exists; it is not rebuilt.
SELECT 1;
