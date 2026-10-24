package sqlc

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// testClock is a fixed clock: the index stores Unix milliseconds, and a test that uses the wall clock
// makes an ordering assertion flaky.
type testClock struct{}

func (testClock) Now() time.Time { return time.UnixMilli(1_700_000_000_000).UTC() }

// seedVault creates a migrated index with one source, one claim and one cluster. It is the smallest
// consistent vault the repository tests can start from.
func seedVault(t *testing.T) (*sql.DB, *AtomRepository, *SourceRepository, *ClusterRepository, ulid.ULID) {
	t.Helper()
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	index := NewSourceRepository(db)
	atoms := NewAtomRepository(db)
	clusters := NewClusterRepository(db)

	now := testClock{}.Now()
	hash := "7f3a1c0000000000000000000000000000000000000000000000000000000000"
	if err := index.UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: hash + ".md", Title: "A source", Bytes: 10, TreeState: entities.TreeBuilt,
		ImportedAt: now, Meta: "{}",
	}); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	atomID := ulid.Make()
	if err := atoms.SaveAtom(ctx, entities.Atom{
		ID: atomID, Topic: "eng", Path: "eng/a-claim.md", Claim: "A claim", Body: "b",
		Status: entities.AtomActive, Sources: []entities.AtomSource{{Hash: hash, NodeID: "0.1"}},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save atom: %v", err)
	}
	if err := clusters.SaveCluster(ctx, entities.Cluster{
		ID: ulid.Make(), Atoms: []ulid.ULID{atomID}, Created: now, Updated: now,
	}); err != nil {
		t.Fatalf("save cluster: %v", err)
	}
	return db, atoms, index, clusters, atomID
}
