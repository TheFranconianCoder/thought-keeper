package persistence

import (
	"context"
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

// ftsTriggerNames are the triggers ftsTriggers installs. They are checked BEFORE the statements run,
// because the rebuild is only needed at the one moment rows could exist that no trigger ever saw: a
// trigger is new. Rebuilding on every start made a 238k-claim vault take half a minute to open.
var ftsTriggerNames = []string{
	"atoms_ai", "atoms_ad", "atoms_au",
	"source_nodes_ai", "source_nodes_ad", "source_nodes_au",
}

// EnsureFTS installs the FTS triggers and repopulates the index when that is actually needed: a
// trigger was missing (rows written before it existed are not indexed), or the index is empty while
// its content is not (a table recreated by a migration). Returns false when the SQLite build has no
// FTS5.
func EnsureFTS(db *sql.DB, log *slog.Logger) bool {
	if !HasFTS5(db) {
		return false
	}
	installed := false
	for _, name := range ftsTriggerNames {
		var n int
		if err := db.QueryRowContext(context.Background(),
			"SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?", name).Scan(&n); err != nil {
			log.Warn("fts trigger check", "trigger", name, "err", err)
			return false
		}
		if n == 0 {
			installed = true
		}
	}
	for _, stmt := range ftsTriggers {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			log.Warn("fts trigger", "err", err)
			return false
		}
	}
	for _, t := range []struct{ fts, content string }{
		{"atoms_fts", "atoms"},
		{"source_nodes_fts", "source_nodes"},
	} {
		if !installed && !ftsEmpty(db, t.fts, t.content, log) {
			continue
		}
		if _, err := db.ExecContext(context.Background(),
			fmt.Sprintf("INSERT INTO %s(%s) VALUES ('rebuild')", t.fts, t.fts)); err != nil {
			log.Warn("fts rebuild", "table", t.fts, "err", err)
			continue
		}
		log.Info("fts index rebuilt", "table", t.fts)
	}
	return true
}

// ftsEmpty reports whether the content table has rows the index does not. The index is read from
// its docsize shadow table: an external-content FTS table answers a plain SELECT from the CONTENT
// table, so asking it would always say "not empty". An unreadable answer counts as empty:
// rebuilding is the safe response, skipping is not.
func ftsEmpty(db *sql.DB, fts, content string, log *slog.Logger) bool {
	hasContent, err := tableHasRows(db, content)
	if err != nil {
		log.Warn("fts content check", "table", content, "err", err)
		return true
	}
	hasIndex, err := tableHasRows(db, fts+"_docsize")
	if err != nil {
		log.Warn("fts index check", "table", fts, "err", err)
		return true
	}
	return hasContent && !hasIndex
}

func tableHasRows(db *sql.DB, table string) (bool, error) {
	var exists int
	if err := db.QueryRowContext(context.Background(),
		fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s)", table)).Scan(&exists); err != nil {
		return false, err
	}
	return exists == 1, nil
}
