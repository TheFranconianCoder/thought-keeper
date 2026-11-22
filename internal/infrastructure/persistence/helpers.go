package persistence

import (
	"context"
	"database/sql"
)

// CountUnclustered is how many claims no cluster owns: the backlog of the association step.
//
// The count is "all claims minus the claims the association layer owns", not a correlated
// NOT EXISTS. `idx_cluster_atoms_atom` covers the DISTINCT, so the read is one index scan over the
// memberships instead of an index probe per claim — the difference between tens and hundreds of
// milliseconds on a six-figure claim vault, on a request the front door makes repeatedly.
func CountUnclustered(db *sql.DB) int64 {
	if db == nil {
		return 0
	}
	var n int64
	if err := db.QueryRowContext(context.Background(), `SELECT max(0,
		(SELECT count(*) FROM atoms) -
		(SELECT count(*) FROM (SELECT DISTINCT atom_id FROM cluster_atoms)))`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// CountFailedSources is how many sources have at least one FAILED step in the mirrored step record.
// A badge over the whole vault must not be derived from the page of the list a reader happens to have
// loaded: that count grew with every page, which looked like the app changing its mind.
//
// The predicate is the one the Sources list's `failed` filter uses (`instr(steps,
// '"state":"failed"')`): `steps` is compact `json.Marshal` output written by SetSourceSteps, so the
// state is always spelled `"state":"failed"` and a JSON parse per source is not needed. Decoding
// every row's JSON on each `/status` read cost ~120 ms per 30k sources.
func CountFailedSources(db *sql.DB) int64 {
	if db == nil {
		return 0
	}
	var n int64
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM sources
		WHERE instr(steps, '"state":"failed"') > 0`).Scan(&n); err != nil {
		return 0
	}
	return n
}
