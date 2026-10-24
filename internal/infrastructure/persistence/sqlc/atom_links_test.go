package sqlc

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// TestAtomLinksResolveByClaim: an atom's [[Claim Text]] link is an edge to the atom whose claim is
// that sentence. It was stored raw and never resolved before, so the claim graph was red forever.
func TestAtomLinksResolveByClaim(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.UnixMilli(1_700_000_000_000).UTC()
	hash := strings.Repeat("cd", 32)
	sources := NewSourceRepository(db)
	atoms := NewAtomRepository(db)
	if err := sources.UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: hash + ".md", Title: "src", Bytes: 3, TreeState: entities.TreeBuilt,
		ImportedAt: now, Meta: "{}",
	}); err != nil {
		t.Fatalf("source: %v", err)
	}

	target := ulid.Make()
	from := ulid.Make()
	base := entities.Atom{
		Topic: "eng", Body: "b", Status: entities.AtomActive,
		CreatedAt: now, UpdatedAt: now,
		Sources: []entities.AtomSource{{Hash: hash, NodeID: "0.1"}},
	}
	targetAtom := base
	targetAtom.ID, targetAtom.Claim, targetAtom.Path = target, "The target claim.", "a.md"
	if err := atoms.SaveAtom(ctx, targetAtom); err != nil {
		t.Fatalf("target: %v", err)
	}
	fromAtom := base
	fromAtom.ID, fromAtom.Claim, fromAtom.Path = from, "The linking claim.", "b.md"
	fromAtom.Links = []entities.Link{{RawTarget: "The target claim."}}
	if err := atoms.SaveAtom(ctx, fromAtom); err != nil {
		t.Fatalf("from: %v", err)
	}

	if n, err := atoms.ResolveLinks(ctx); err != nil || n != 1 {
		t.Fatalf("resolve links = %d, %v; want 1", n, err)
	}
	list, err := atoms.ListAtoms(ctx, "eng", 10, 0)
	if err != nil {
		t.Fatalf("list atoms: %v", err)
	}
	for _, a := range list {
		if a.ID != from {
			continue
		}
		if len(a.Links) != 1 || a.Links[0].ToID != target {
			t.Fatalf("link = %+v, want target %s", a.Links, target)
		}
		return
	}
	t.Fatal("the linking atom was not listed")
}
