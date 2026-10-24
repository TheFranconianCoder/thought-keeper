package usecases_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

// TestURLAddressMigrationMovesTheSourceAndItsClaims: a URL source used to be addressed by the hash of
// its FIRST snapshot, which is a fact about content that is a moving target — and the reason a
// refreshed page became a second source. The address is the URL now, and the cleanup task moves the
// sources that are still on the old one.
//
// It is a RENAME, and that is what the test pins: the row keeps its id, the snapshot and the derived
// files come along, and the claims keep everything but the address they cite. Nothing is derived again.
func TestURLAddressMigrationMovesTheSourceAndItsClaims(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	const url = "https://example.com/book"

	// A source on the OLD address: the hash of the snapshot, as it used to be.
	oldHash := strings.Repeat("ab", 32)
	src := entities.Source{
		ID: ulid.Make(), Hash: oldHash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: oldHash[:2] + "/" + oldHash[2:4] + "/" + oldHash + ".md",
		URL:  url, Title: "The Book", Bytes: 20, TreeState: entities.TreeBuilt,
	}
	if _, err := store.PutAt(oldHash, ".md", []byte("---\nurl: "+url+"\n---\n\nbody\n")); err != nil {
		t.Fatalf("seed object: %v", err)
	}
	if err := idx.UpsertSource(ctx, src); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	atom := entities.Atom{
		ID: ulid.Make(), Topic: "eng", Path: "eng/a.md", Claim: "a claim", Body: "b",
		Status: entities.AtomActive, Sources: []entities.AtomSource{{Hash: oldHash, NodeID: "0.1"}},
	}
	atoms := &fakeAtomRepo{saved: []entities.Atom{atom}}
	atomStore := &fakeAtomStore{m: map[string]entities.Atom{}}

	migrate := usecases.MigrateURLAddresses{
		Index: idx, Store: store, Atoms: atoms, AtomStore: atomStore,
	}

	// The preview says what is pending and touches nothing.
	res, err := migrate.Execute(ctx, false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Moved != 0 || len(res.Pending) != 1 {
		t.Fatalf("preview = %+v, want one pending URL and no move", res)
	}

	res, err = migrate.Execute(ctx, true)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	want := entities.URLHash(url)
	if res.Moved != 1 || res.Atoms != 1 {
		t.Fatalf("migrate = %+v, want one move and one re-pointed claim", res)
	}
	if len(res.Pending) != 0 {
		t.Errorf("the URL is still pending: %v", res.Pending)
	}

	// The address IS the URL's hash, and the row kept its id.
	moved, err := idx.GetSourceByHash(ctx, want)
	if err != nil {
		t.Fatalf("the source is not at its URL address: %v", err)
	}
	if moved.ID != src.ID {
		t.Error("the row lost its id: every provenance row points at the id")
	}
	// The snapshot came along, byte for byte.
	rc, err := store.Open(want, ".md")
	if err != nil {
		t.Fatalf("open moved snapshot: %v", err)
	}
	defer func() { _ = rc.Close() }()
	b, _ := io.ReadAll(rc)
	if !strings.Contains(string(b), "body") {
		t.Errorf("the snapshot did not come along: %s", b)
	}
	// The claim cites the new address and nothing else changed.
	got := atomStore.m[atom.ID.String()]
	if len(got.Sources) != 1 || got.Sources[0].Hash != want || got.Sources[0].NodeID != "0.1" {
		t.Errorf("the claim's provenance = %+v, want the new address and the same node", got.Sources)
	}
	if got.Claim != atom.Claim {
		t.Error("a rename changed what the claim says")
	}

	// Idempotent: a second pass has nothing to do.
	res, err = migrate.Execute(ctx, true)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if res.Moved != 0 || len(res.Pending) != 0 {
		t.Errorf("the migration is not idempotent: %+v", res)
	}
}
