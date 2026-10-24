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
// index still agrees with the vault, and it must notice every disagreement the association layer can
// have — a record without a row, a row without a record, a record naming a claim that is gone, and
// a membership table that contradicts its record. This check must never be dropped: it is what
// keeps the derived layer from serving a stale past.
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
	check := func(t *testing.T, atoms *AtomRepository, repo *ClusterRepository, store *clusters.Store) (bool, string) {
		t.Helper()
		uc := usecases.ReindexVault{Atoms: atoms, ClusterRepo: repo, ClusterStore: store}
		needs, reason, err := uc.NeedsRepair(ctx)
		if err != nil {
			t.Fatalf("needs repair: %v", err)
		}
		return needs, reason
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
		if needs, reason := check(t, atoms, repo, store); needs {
			t.Fatalf("a consistent vault was reported inconsistent: %s", reason)
		}
	})

	t.Run("record without row", func(t *testing.T) {
		_, atoms, repo, store := seed(t)
		if err := store.WriteMeta(ctx, entities.Cluster{ID: ulid.Make()}); err != nil {
			t.Fatal(err)
		}
		if needs, _ := check(t, atoms, repo, store); !needs {
			t.Fatal("a record without its row was not reported")
		}
	})

	t.Run("row without record", func(t *testing.T) {
		id, atoms, repo, store := seed(t)
		if err := repo.SaveCluster(ctx, entities.Cluster{ID: ulid.Make(), Atoms: []ulid.ULID{id}, Created: now, Updated: now}); err != nil {
			t.Fatal(err)
		}
		if needs, _ := check(t, atoms, repo, store); !needs {
			t.Fatal("a row without its record was not reported")
		}
	})

	t.Run("record names a claim that is gone", func(t *testing.T) {
		id, atoms, repo, store := seed(t)
		rec := entities.Cluster{ID: ulid.Make(), Atoms: []ulid.ULID{id, ulid.Make()}, Created: now, Updated: now}
		// The row mirrors only the member that still exists, so the table itself does not disagree.
		if err := repo.SaveCluster(ctx, entities.Cluster{ID: rec.ID, Atoms: []ulid.ULID{id}, Created: now, Updated: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.WriteMeta(ctx, rec); err != nil {
			t.Fatal(err)
		}
		needs, reason := check(t, atoms, repo, store)
		if !needs || reason == "" {
			t.Fatal("a record naming a vanished claim was not reported")
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
		if needs, _ := check(t, atoms, repo, store); !needs {
			t.Fatal("a membership that contradicts its record was not reported")
		}
	})
}
