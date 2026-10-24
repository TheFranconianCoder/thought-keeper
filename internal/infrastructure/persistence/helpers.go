package persistence

import "database/sql"

// CountUnclustered is how many claims no cluster owns: the backlog of the association step.
func CountUnclustered(db *sql.DB) int64 {
	if db == nil {
		return 0
	}
	var n int64
	_ = db.QueryRow(`SELECT count(*) FROM atoms a
		WHERE NOT EXISTS (SELECT 1 FROM cluster_atoms ca WHERE ca.atom_id = a.id)`).Scan(&n)
	return n
}

// CountFailedSources is how many sources have at least one FAILED step in the mirrored step record.
// A badge over the whole vault must not be derived from the page of the list a reader happens to have
// loaded: that count grew with every page, which looked like the app changing its mind.
func CountFailedSources(db *sql.DB) int64 {
	if db == nil {
		return 0
	}
	var n int64
	_ = db.QueryRow(`SELECT count(*) FROM sources
		WHERE json_valid(steps) AND EXISTS (
			SELECT 1 FROM json_each(sources.steps)
			WHERE json_extract(json_each.value, '$.state') = 'failed'
		)`).Scan(&n)
	return n
}
