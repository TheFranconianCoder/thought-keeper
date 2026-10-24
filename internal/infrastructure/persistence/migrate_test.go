package persistence

import (
	"path/filepath"
	"testing"
)

func TestMigrateAndFTS5(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !HasFTS5(db) {
		t.Skip("SQLite build has no FTS5; search would be degraded")
	}
	for _, table := range []string{"sources", "atoms", "clusters", "cluster_atoms", "runs"} {
		if got := Count(db, table); got != 0 {
			t.Errorf("Count(%s) = %d, want 0", table, got)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := Migrate(db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}
