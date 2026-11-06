package sqlc

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/clusters"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// TestNeedsRepairDetectsEachInconsistency pins the start-up contract: the app must ASK whether the
// index still agrees with the vault, and it must notice every real disagreement the association layer
// can have — a record with live members but no row, a row with live members but no record, and a
// membership table that contradicts the text the record still says. A record whose members are all
// gone is the NORMAL residue of a re-derivation: it is counted as stale, not treated as a rebuild.
func TestNeedsRepairDetectsEachInconsistency(t *testing.T) {
	ctx := context.Background()
	seed := func(t *testing.T) (ulid.ULID, *AtomRepository, *ClusterRepository, *clusters.Store) {
		t.Helper()
		db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if err := persistence.Migrate(db); err != nil {
			t.Fatal(err)
		}
		now := time.UnixMilli(1_700_000_000_000).UTC()
		hash := "feed000000000000000000000000000000000000000000000000000000000000"
		if err := NewSourceRepository(db).UpsertSource(ctx, entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
			Path: hash + ".md", Title: "src", Bytes: 3, TreeState: entities.TreeBuilt,
			ImportedAt: now, Meta: "{}",
		}); err != nil {
			t.Fatalf("source: %v", err)
		}
		repo := NewAtomRepository(db)
		id := ulid.Make()
		if err := repo.SaveAtom(ctx, entities.Atom{
			ID: id, Topic: "caching", Claim: "A claim.", Body: "b", Type: entities.AtomFact,
			Status: entities.AtomActive, Path: id.String() + ".md",
			Sources:   []entities.AtomSource{{Hash: hash, NodeID: "0.1"}},
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("atom: %v", err)
		}
		return id, repo, NewClusterRepository(db), clusters.New(t.TempDir())
	}
	check := func(t *testing.T, atoms *AtomRepository, repo *ClusterRepository, store *clusters.Store) (bool, string, int) {
		t.Helper()
		uc := usecases.Maintenance{Atoms: atoms, ClusterRepo: repo, ClusterStore: store}
		needs, reason, stale, err := uc.NeedsRepair(ctx)
		if err != nil {
			t.Fatalf("needs repair: %v", err)
		}
		return needs, reason, stale
	}
	now := time.UnixMilli(1_700_000_000_000).UTC()

	t.Run("consistent", func(t *testing.T) {
		id, atoms, repo, store := seed(t)
		rec := entities.Cluster{ID: ulid.Make(), Atoms: []ulid.ULID{id}, Created: now, Updated: now}
		if err := repo.SaveCluster(ctx, rec); err != nil {
			t.Fatal(err)
		}
		if err := store.WriteMeta(ctx, rec); err != nil {
			t.Fatal(err)
		}
		if needs, reason, stale := check(t, atoms, repo, store); needs || stale != 0 {
			t.Fatalf("a consistent vault was reported inconsistent: %s (stale %d)", reason, stale)
		}
	})

	t.Run("record with live members but no row", func(t *testing.T) {
		id, atoms, repo, store := seed(t)
		if err := store.WriteMeta(ctx, entities.Cluster{ID: ulid.Make(), Atoms: []ulid.ULID{id}}); err != nil {
			t.Fatal(err)
		}
		if needs, _, _ := check(t, atoms, repo, store); !needs {
			t.Fatal("a record with live members and no row was not reported")
		}
	})

	t.Run("record whose members are all gone is stale", func(t *testing.T) {
		_, atoms, repo, store := seed(t)
		rec := entities.Cluster{ID: ulid.Make(), Atoms: []ulid.ULID{ulid.Make()}, Created: now, Updated: now}
		if err := repo.SaveCluster(ctx, entities.Cluster{ID: rec.ID, Created: now, Updated: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.WriteMeta(ctx, rec); err != nil {
			t.Fatal(err)
		}
		needs, reason, stale := check(t, atoms, repo, store)
		if needs {
			t.Fatalf("a stale record was reported as an inconsistency: %s", reason)
		}
		if stale != 1 {
			t.Fatalf("stale = %d, want 1", stale)
		}
	})

	t.Run("row with live members but no record", func(t *testing.T) {
		id, atoms, repo, store := seed(t)
		if err := repo.SaveCluster(ctx, entities.Cluster{ID: ulid.Make(), Atoms: []ulid.ULID{id}, Created: now, Updated: now}); err != nil {
			t.Fatal(err)
		}
		if needs, _, _ := check(t, atoms, repo, store); !needs {
			t.Fatal("a row with live members and no record was not reported")
		}
	})

	t.Run("empty row without record is stale", func(t *testing.T) {
		_, atoms, repo, store := seed(t)
		if err := repo.SaveCluster(ctx, entities.Cluster{ID: ulid.Make(), Created: now, Updated: now}); err != nil {
			t.Fatal(err)
		}
		if needs, reason, _ := check(t, atoms, repo, store); needs {
			t.Fatalf("an empty row without a record was reported: %s", reason)
		}
	})

	t.Run("stale record with a live membership is inconsistent", func(t *testing.T) {
		id, atoms, repo, store := seed(t)
		rec := entities.Cluster{ID: ulid.Make(), Atoms: []ulid.ULID{ulid.Make()}, Created: now, Updated: now}
		if err := repo.SaveCluster(ctx, entities.Cluster{ID: rec.ID, Atoms: []ulid.ULID{id}, Created: now, Updated: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.WriteMeta(ctx, rec); err != nil {
			t.Fatal(err)
		}
		if needs, reason, _ := check(t, atoms, repo, store); !needs {
			t.Fatalf("a row with live members behind a stale record was not reported: %s", reason)
		}
	})

	t.Run("record names a claim that is gone", func(t *testing.T) {
		id, atoms, repo, store := seed(t)
		rec := entities.Cluster{ID: ulid.Make(), Atoms: []ulid.ULID{id, ulid.Make()}, Created: now, Updated: now}
		// The row mirrors only the member that still exists, so the table agrees with the live text.
		if err := repo.SaveCluster(ctx, entities.Cluster{ID: rec.ID, Atoms: []ulid.ULID{id}, Created: now, Updated: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.WriteMeta(ctx, rec); err != nil {
			t.Fatal(err)
		}
		needs, reason, _ := check(t, atoms, repo, store)
		if needs {
			t.Fatalf("a stale member was reported as an inconsistency: %s", reason)
		}
	})

	t.Run("membership disagrees with the record", func(t *testing.T) {
		id, atoms, repo, store := seed(t)
		other := ulid.Make()
		if err := atoms.SaveAtom(ctx, entities.Atom{
			ID: other, Topic: "caching", Claim: "Another claim.", Body: "b", Type: entities.AtomFact,
			Status: entities.AtomActive, Path: other.String() + ".md",
			Sources:   []entities.AtomSource{{Hash: "feed000000000000000000000000000000000000000000000000000000000000", NodeID: "0.1"}},
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		recID := ulid.Make()
		if err := repo.SaveCluster(ctx, entities.Cluster{ID: recID, Atoms: []ulid.ULID{other}, Created: now, Updated: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.WriteMeta(ctx, entities.Cluster{ID: recID, Atoms: []ulid.ULID{id}, Created: now, Updated: now}); err != nil {
			t.Fatal(err)
		}
		if needs, _, _ := check(t, atoms, repo, store); !needs {
			t.Fatal("a membership that contradicts its record was not reported")
		}
	})
}
