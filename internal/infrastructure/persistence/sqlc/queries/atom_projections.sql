-- The PROJECTIONS of the atom layer: the narrow reads that ask only what a step needs, never the
-- whole row. The list read (SELECT * + hydration) exists for a reader that RENDERS a claim; the
-- association step, the health checks and the consistency check need a few columns over EVERY
-- claim, and reading the whole vault for that is what made a pass cost a gigabyte.
--
-- House rule for this folder, the hard way: sqlc's sqlite engine (v1.31.1) mis-splits a query file
-- on non-ASCII bytes -- a multi-byte character in a comment shifts the tail of a later statement
-- into garbage that still COMPILES and still passes `sqlc vet`. So query files stay ASCII-only.

-- name: AllAtomAddresses :many
-- The id and the recorded file path of every claim: what a stale index row needs in order to delete
-- the FILE it points at. The path is the record's own, never recomputed: a use case must not learn
-- the store's folder convention in order to clean up after it.
SELECT id, path FROM atoms ORDER BY id;

-- name: AllAtomIDs :many
-- The existence set of the claim layer: which claims are there. A consistency check compares the
-- cluster membership against this and never reads a body.
SELECT id FROM atoms ORDER BY id;

-- name: AtomsWithBracketSyntax :many
-- The claims whose body still carries a [[...]] span: the candidates of the one-time code repair. On
-- a claim a bracket pair is quoted syntax, never a link, so the span belongs in an inline code mark.
SELECT id, path FROM atoms WHERE instr(body, '[[') > 0 ORDER BY id;

-- name: ListAtomTextsPage :many
-- One keyset page of claim texts, id order. The association pass walks the corpus by pages: one
-- page is tokenized, folded into the running statistics and dropped - the vault is never held in
-- memory whole. WHERE id > ? ORDER BY id uses the primary key; the page walk is one index scan.
SELECT id, topic, claim, body FROM atoms WHERE id > ? ORDER BY id LIMIT ?;

-- name: AtomTextsForIDs :many
-- The text of EXACTLY these claims: the signature of a touched record is computed from their
-- (id, claim, body), and touching one record per pass is the common case - not 170k reads.
SELECT id, topic, claim, body FROM atoms WHERE id IN (sqlc.slice('ids')) ORDER BY id;

-- name: ListAtomIdentityPage :many
-- One keyset page of everything that makes a claim a CLAIM (the duplicate check's key): id, topic,
-- claim, body, type, status. The check must read the index, not walk 170k atom files; the start-up
-- consistency check is what guarantees the index agrees with the vault.
SELECT id, topic, claim, body, type, status FROM atoms WHERE id > ? ORDER BY id LIMIT ?;

-- name: CountAtoms :one
SELECT count(*) FROM atoms;
