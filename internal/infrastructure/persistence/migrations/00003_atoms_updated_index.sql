-- The claim list and the topic repair both want claims IN TIME ORDER: the UI pages the newest
-- claims first (`ORDER BY updated_at DESC LIMIT/OFFSET`) and rebranch walks the oldest ones first.
-- Without an index each of those reads scanned all 170k rows and built a temp B-tree — the reason a
-- 50-claim page answered in over a second.
-- +goose Up
CREATE INDEX idx_atoms_updated ON atoms (updated_at, id);

-- +goose Down
DROP INDEX IF EXISTS idx_atoms_updated;
