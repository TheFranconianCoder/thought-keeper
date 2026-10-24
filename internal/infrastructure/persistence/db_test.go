package persistence_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// TestForeignKeysSurviveAReconnect is the guard for silent integrity loss: the pragmas are
// per connection, so a replaced connection used to run with foreign_keys OFF — and then the
// ON DELETE CASCADEs stopped firing, leaving child rows whose parent was gone.
func TestForeignKeysSurviveAReconnect(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	db, err := persistence.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	check := func(stage string) {
		t.Helper()
		var fk int
		if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("%s: pragma: %v", stage, err)
		}
		if fk != 1 {
			t.Fatalf("%s: foreign_keys = %d, want 1", stage, fk)
		}
	}
	check("initial")

	// Force the pool to hand out a brand-new connection.
	db.SetMaxIdleConns(0)
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	check("after reconnect")

	// A second handle on the same file (its own connection) must be safe too.
	db2, err := persistence.Open(path)
	if err != nil {
		t.Fatalf("open second: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	var fk int
	if err := db2.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("second handle pragma: %v", err)
	}
	if fk != 1 {
		t.Errorf("second handle: foreign_keys = %d, want 1", fk)
	}

	// And the cascade really fires: a deleted source takes its nodes with it.
	if _, err := db.ExecContext(ctx, `INSERT INTO sources (id, hash, kind, ext, path, title, bytes, tree_state, imported_at, meta)
		VALUES (x'0102030405060708090a0b0c0d0e0f10', 'aa', 'md', '.md', 'aa.md', 't', 1, 'built', 1, '{}')`); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_nodes (source_id, node_id, parent_id, depth, ord, title)
		VALUES (x'0102030405060708090a0b0c0d0e0f10', '1', '', 1, 0, 'n')`); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM sources WHERE hash = 'aa'`); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	if n := persistence.Count(db, "source_nodes"); n != 0 {
		t.Errorf("cascade did not fire: %d orphaned node(s) left", n)
	}
}

// TestCountFailedSources: the Attention badge is a vault-wide number. A source with two failed steps
// counts once, and a source whose steps are not valid JSON is skipped instead of failing the report.
func TestCountFailedSources(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	insert := func(hash, steps string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO sources (id, hash, kind, ext, path, title, bytes, tree_state, steps, imported_at, meta)
			VALUES (randomblob(16), ?, 'md', '.md', ?, 't', 1, 'none', ?, 1, '{}')`, hash, hash+".md", steps); err != nil {
			t.Fatalf("insert %s: %v", hash, err)
		}
	}
	insert("aa", `{"capture":{"state":"failed"},"distill":{"state":"failed"}}`)
	insert("bb", `{"capture":{"state":"ok"},"distill":{"state":"failed"}}`)
	insert("cc", `{"capture":{"state":"ok"}}`)
	insert("dd", `not json`)

	if n := persistence.CountFailedSources(db); n != 2 {
		t.Fatalf("failed sources = %d, want 2 (one per source, invalid JSON skipped)", n)
	}
}
