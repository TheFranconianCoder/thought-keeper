// Package persistence opens the SQLite index and runs migrations.
package persistence

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// Open opens the index database, applies connection pragmas and verifies it is reachable.
func Open(path string) (*sql.DB, error) {
	// The pragmas go into the DSN so EVERY connection gets them. Executing them once on one
	// pooled connection is not enough: a replacement connection starts with the defaults, and
	// with foreign_keys off the ON DELETE CASCADEs silently stop firing — which leaves child
	// rows orphaned (a source counting atoms its detail view can no longer resolve).
	dsn := path
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	dsn += sep + "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// A small POOL, not one connection. WAL lets readers and the writer work at the same time, so a
	// slow read (the sources list computes five aggregates per row) no longer holds up every write —
	// with a single connection, one 38-second query stalled the whole app, step records included.
	//
	// It is deliberately small: every WRITE still serialises on SQLite's write lock, and
	// busy_timeout(5000) is what absorbs the wait between two writers. A single-statement write is
	// short, and the few explicit transactions are index-only work, so five seconds is a generous
	// ceiling. Readers never block: they read the last committed snapshot.
	//
	// An in-memory database is the exception: every connection would be a DIFFERENT database, so the
	// tests' `:memory:` index keeps exactly one.
	pool := 4
	if isMemoryDB(path) {
		pool = 1
	}
	db.SetMaxOpenConns(pool)
	db.SetMaxIdleConns(pool)

	// Verify what the DSN promises instead of assuming it: silent integrity loss is exactly
	// what this check exists to prevent.
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pragma foreign_keys: %w", err)
	}
	if fk != 1 {
		_ = db.Close()
		return nil, fmt.Errorf("pragma foreign_keys is off: the index would lose referential integrity")
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return db, nil
}

// isMemoryDB reports whether the DSN names an in-memory database. Every connection to `:memory:` opens a
// PRIVATE database, so such an index must stay on one connection or the schema disappears between
// queries.
func isMemoryDB(path string) bool {
	p := strings.ToLower(strings.TrimSpace(path))
	return p == ":memory:" ||
		strings.Contains(p, "mode=memory") ||
		strings.Contains(p, "file::memory:") ||
		p == ""
}

// HasFTS5 probes whether the SQLite build supports the FTS5 extension.
func HasFTS5(db *sql.DB) bool {
	if _, err := db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS _fts5_probe USING fts5(x);"); err != nil {
		return false
	}
	_, _ = db.Exec("DROP TABLE IF EXISTS _fts5_probe;")
	return true
}

// CountWhere returns the row count of a table for one condition, or 0 when the table does not exist
// yet. Both the table and the condition are fixed internal identifiers, never user input.
func CountWhere(db *sql.DB, table, where string) int64 {
	var n int64
	row := db.QueryRow("SELECT count(*) FROM " + table + " WHERE " + where) //nolint:gosec // fixed internal identifiers
	if err := row.Scan(&n); err != nil {
		return 0
	}
	return n
}

// SourceTitle returns a source's title for a hash, or "" when it is not in the index. It exists for the
// status report, which lists what the app is working on and should name it the way the reader knows it
// (the Sources view shows titles, not hashes).
func SourceTitle(db *sql.DB, hash string) string {
	var title string
	if err := db.QueryRow("SELECT title FROM sources WHERE hash = ?", hash).Scan(&title); err != nil {
		return ""
	}
	return title
}

// Count returns the row count of a table, or 0 when the table does not exist yet.
func Count(db *sql.DB, table string) int64 {
	n, _ := CountChecked(db, table)
	return n
}

// CountChecked is Count with the SQL error preserved. A caller that decides to rebuild the index
// must use this: "the query failed" is not "the table is empty".
func CountChecked(db *sql.DB, table string) (int64, error) {
	var n int64
	// The table name is not user input; it comes from a fixed set of constants.
	row := db.QueryRow("SELECT count(*) FROM " + table) //nolint:gosec // fixed internal identifiers
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
