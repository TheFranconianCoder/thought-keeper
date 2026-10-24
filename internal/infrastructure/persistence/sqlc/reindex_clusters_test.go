package sqlc

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	atomstore "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/atoms"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/clusters"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// TestReindexRemovesClusterRowsWithoutRecords is the state a reader creates by deleting clusters/
// and keeping the index: the vault says there are no clusters, the cache says 6,000. The start-up
// consistency check must ask for a reindex, and the reindex must remove the orphaned rows instead of
// leaving every claim "already clustered" forever.
func TestReindexRemovesClusterRowsWithoutRecords(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	_, atoms, index, clusterRepo, atomID := seedVault(t)
	rec, err := clusterRepo.ListClusters(ctx, 10, 0)
	if err != nil || len(rec) != 1 {
		t.Fatalf("seed cluster: %v %d", err, len(rec))
	}

	reindex := usecases.ReindexVault{
		Store: objects.New(t.TempDir()), Index: index, Atoms: atoms,
		AtomStore:    atomstore.New(t.TempDir()),
		ClusterStore: clusters.New(t.TempDir()), ClusterRepo: clusterRepo,
		Clock: testClock{},
	}
	needs, reason, err := reindex.NeedsRepair(ctx)
	if err != nil {
		t.Fatalf("needs repair: %v", err)
	}
	if !needs {
		t.Fatal("a cluster row without its record was not reported as inconsistent")
	}
	t.Logf("reason: %s", reason)

	if _, err := reindex.Execute(ctx); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if n, err := clusterRepo.CountClusters(ctx); err != nil || n != 0 {
		t.Fatalf("clusters after reindex = %d (%v), want 0", n, err)
	}
	if _, err := atoms.GetAtom(ctx, atomID); err == nil {
		t.Fatal("the atom row survived although its source object was gone")
	}
}
