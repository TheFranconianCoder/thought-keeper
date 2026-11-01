package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func openFTSVault(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !HasFTS5(db) {
		t.Skip("SQLite build has no FTS5; search would be degraded")
	}
	return db
}

func insertClaim(t *testing.T, db *sql.DB, n int, claim, body string) {
	t.Helper()
	// The rowid is what the FTS triggers mirror; any 16-byte id and a unique path are enough.
	id := make([]byte, 16)
	id[0] = byte(n)
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO atoms (id, topic, path, claim, body, body_hash, size, mtime, created_at, updated_at, indexed_at)
		 VALUES (?, 'testing', ?, ?, ?, 'h', 1, 1, 1, 1, 1)`,
		id, fmt.Sprintf("claim-%d.md", n), claim, body,
	); err != nil {
		t.Fatalf("insert claim %d: %v", n, err)
	}
}

func ftsMatches(t *testing.T, db *sql.DB, term string) bool {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM atoms_fts WHERE atoms_fts MATCH ?`, term).Scan(&n); err != nil {
		t.Fatalf("search %q: %v", term, err)
	}
	return n > 0
}

// TestEnsureFTSRebuildsOnlyWhenItMust is the start-time guard: the rebuild is what made a
// 238k-claim vault take half a minute to open, and it is only needed at the one moment rows could
// exist that no trigger saw — a trigger is new. Once the triggers are there, they keep the index in
// step and the rebuild must not run again.
func TestEnsureFTSRebuildsOnlyWhenItMust(t *testing.T) {
	db := openFTSVault(t)
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	// A claim written before the triggers existed: content without an index.
	insertClaim(t, db, 1, "a cache must be invalidated", "the body of the first claim")
	if !ftsEmpty(db, "atoms_fts", "atoms", log) {
		t.Fatal("the index is not empty although no trigger ran")
	}

	if !EnsureFTS(db, log) {
		t.Fatal("EnsureFTS reported no FTS5")
	}
	if got := strings.Count(buf.String(), "fts index rebuilt"); got != 2 {
		t.Fatalf("rebuilds = %d, want one per table (2)", got)
	}
	if !ftsMatches(t, db, "invalidated") {
		t.Fatal("the claim written before the triggers is not searchable")
	}

	// The triggers are installed now: a new claim is indexed by the trigger, and a second EnsureFTS
	// must not rebuild anything.
	insertClaim(t, db, 2, "the second claim about invalidation", "the body of the second claim")
	if !ftsMatches(t, db, "second") {
		t.Fatal("the trigger did not index a new claim")
	}
	buf.Reset()
	if !EnsureFTS(db, log) {
		t.Fatal("EnsureFTS reported no FTS5")
	}
	if strings.Contains(buf.String(), "fts index rebuilt") {
		t.Fatalf("the index was rebuilt although the triggers were in place:\n%s", buf.String())
	}
	if !ftsMatches(t, db, "invalidated") || !ftsMatches(t, db, "second") {
		t.Fatal("a skipped rebuild lost a claim")
	}
}

// TestEnsureFTSRebuildsAnEmptyIndex: a table recreated by a migration has its triggers but no rows.
// The content is there, the index is not — that is the second case a rebuild is for.
func TestEnsureFTSRebuildsAnEmptyIndex(t *testing.T) {
	db := openFTSVault(t)
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	insertClaim(t, db, 1, "a claim that must survive a lost index", "body")
	if !EnsureFTS(db, log) {
		t.Fatal("EnsureFTS reported no FTS5")
	}
	if _, err := db.ExecContext(context.Background(), `INSERT INTO atoms_fts(atoms_fts) VALUES ('delete-all')`); err != nil {
		t.Fatalf("empty the index: %v", err)
	}
	if !ftsEmpty(db, "atoms_fts", "atoms", log) {
		t.Fatal("the emptied index is not reported as empty")
	}

	buf.Reset()
	if !EnsureFTS(db, log) {
		t.Fatal("EnsureFTS reported no FTS5")
	}
	if !strings.Contains(buf.String(), "fts index rebuilt") {
		t.Fatal("an empty index over existing content was not rebuilt")
	}
	if !ftsMatches(t, db, "survive") {
		t.Fatal("the claim is not searchable after the rebuild")
	}
}
