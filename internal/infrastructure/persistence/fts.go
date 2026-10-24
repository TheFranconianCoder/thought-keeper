package persistence

import (
	"database/sql"
	"fmt"
	"log/slog"
)

// ftsTriggers keep the external-content FTS tables in sync. They live here rather
// than in a migration so sqlc's schema parser never sees them.
var ftsTriggers = []string{
	`CREATE TRIGGER IF NOT EXISTS atoms_ai AFTER INSERT ON atoms BEGIN
		INSERT INTO atoms_fts(rowid, claim, body) VALUES (new.rowid, new.claim, new.body); END`,
	`CREATE TRIGGER IF NOT EXISTS atoms_ad AFTER DELETE ON atoms BEGIN
		INSERT INTO atoms_fts(atoms_fts, rowid, claim, body) VALUES ('delete', old.rowid, old.claim, old.body); END`,
	`CREATE TRIGGER IF NOT EXISTS atoms_au AFTER UPDATE ON atoms BEGIN
		INSERT INTO atoms_fts(atoms_fts, rowid, claim, body) VALUES ('delete', old.rowid, old.claim, old.body);
		INSERT INTO atoms_fts(rowid, claim, body) VALUES (new.rowid, new.claim, new.body); END`,
	`CREATE TRIGGER IF NOT EXISTS source_nodes_ai AFTER INSERT ON source_nodes BEGIN
		INSERT INTO source_nodes_fts(rowid, title, summary) VALUES (new.rowid, new.title, new.summary); END`,
	`CREATE TRIGGER IF NOT EXISTS source_nodes_ad AFTER DELETE ON source_nodes BEGIN
		INSERT INTO source_nodes_fts(source_nodes_fts, rowid, title, summary) VALUES ('delete', old.rowid, old.title, old.summary); END`,
	`CREATE TRIGGER IF NOT EXISTS source_nodes_au AFTER UPDATE ON source_nodes BEGIN
		INSERT INTO source_nodes_fts(source_nodes_fts, rowid, title, summary) VALUES ('delete', old.rowid, old.title, old.summary);
		INSERT INTO source_nodes_fts(rowid, title, summary) VALUES (new.rowid, new.title, new.summary); END`,
}

// EnsureFTS installs the FTS triggers and repopulates the index from the content
// tables. Returns false when the SQLite build has no FTS5.
func EnsureFTS(db *sql.DB, log *slog.Logger) bool {
	if !HasFTS5(db) {
		return false
	}
	for _, stmt := range ftsTriggers {
		if _, err := db.Exec(stmt); err != nil {
			log.Warn("fts trigger", "err", err)
			return false
		}
	}
	// Rebuild so rows written before the triggers existed are indexed too.
	for _, table := range []string{"atoms_fts", "source_nodes_fts"} {
		if _, err := db.Exec(fmt.Sprintf("INSERT INTO %s(%s) VALUES ('rebuild')", table, table)); err != nil {
			log.Warn("fts rebuild", "table", table, "err", err)
		}
	}
	return true
}
