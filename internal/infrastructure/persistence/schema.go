package persistence

import (
	"database/sql"
	"fmt"
)

// SchemaVersion is the shape of the index this build writes. It is stored in the database itself
// (`PRAGMA user_version`, set by the migration that last changed the shape) so a build can tell
// whether the index in hand is one it knows how to read and write.
//
// goose cannot answer that question, and that is why this marker exists: the index is DERIVED and
// disposable, so a change of vocabulary happens in the initial migration file with the database thrown
// away — and goose does not run an already-applied migration again. A database written by an older
// build therefore keeps its old shape while the queries move on: every write dies on a constraint
// ("NOT NULL constraint failed: atoms.slug") that names the column but not the reason, and every read
// fails on a column list that no longer matches. The marker turns that into one sentence.
const SchemaVersion = 20261027

// SchemaStatus is what the index says about its own shape.
type SchemaStatus struct {
	// Version is what the database reports, Expected what this build writes.
	Version  int
	Expected int
	// Tables says whether the database holds a schema at all: an empty file is not out of date, it is
	// new — it gets its shape from the migrations, not from a rebuild.
	Tables bool
}

// Outdated reports an index that was written by a build with another shape. It is a state to REPORT,
// never to repair by itself: the index lives in the user's vault, and deleting it is their decision —
// the app says which file and what to do, and nothing happens behind their back.
func (s SchemaStatus) Outdated() bool {
	return s.Tables && s.Version != s.Expected
}

// Fix names the file and the step, so a log line or a status report can be acted on without reading
// this package.
func (s SchemaStatus) Fix(path string) string {
	return fmt.Sprintf("the index %s was written by an older version — delete it and reindex "+
		"(the vault is the truth: sources/, atoms/ and clusters/ rebuild the index)", path)
}

// InspectSchema reads the index's shape marker.
func InspectSchema(db *sql.DB) (SchemaStatus, error) {
	st := SchemaStatus{Expected: SchemaVersion}
	if err := db.QueryRow("PRAGMA user_version").Scan(&st.Version); err != nil {
		return st, fmt.Errorf("pragma user_version: %w", err)
	}
	var n int
	// "Does it hold a schema at all": sqlite_master is empty in a fresh file.
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'").Scan(&n); err != nil {
		return st, fmt.Errorf("count tables: %w", err)
	}
	st.Tables = n > 0
	return st, nil
}
