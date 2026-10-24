package usecases_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc"
)

// trashSpy is the atom store half the duplicate test needs: a collapse trashes FILES, and the test
// asserts which claim went to the trash.
type trashSpy struct {
	interfaces.AtomStore
	trashed []ulid.ULID
}

func (t *trashSpy) Trash(_ context.Context, a entities.Atom) error {
	t.trashed = append(t.trashed, a.ID)
	return nil
}

// TestCollapseDuplicateAtomsReadsTheIndex: the duplicate check used to walk and decode every atom
// FILE (a ~770 MB spike on a 170k-claim vault, asked twice at start-up) to answer a 200-byte
// question. It now streams the INDEX; only the collided hashes are loaded whole, which is what a
// collapse needs anyway. Same key as before: text, topic, type, status and provenance — a claim
// that says the same but came from another outline node is NOT a copy.
func TestCollapseDuplicateAtomsReadsTheIndex(t *testing.T) {
	ctx := context.Background()
	db, openErr := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1_700_000_000_000).UTC()
	hash := "c0ffee0000000000000000000000000000000000000000000000000000000000"
	if err := sqlc.NewSourceRepository(db).UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: hash + ".md", Title: "src", Bytes: 3, TreeState: entities.TreeBuilt,
		ImportedAt: now, Meta: "{}",
	}); err != nil {
		t.Fatalf("source: %v", err)
	}
	repo := sqlc.NewAtomRepository(db)

	mk := func(node string) entities.Atom {
		id := ulid.Make()
		return entities.Atom{
			ID: id, Topic: "caching", Claim: "A cache entry must be invalidated when its source changes.",
			Body: "The entry carries the source's version.", Type: entities.AtomFact, Status: entities.AtomActive,
			Path: id.String() + ".md", Sources: []entities.AtomSource{{Hash: hash, NodeID: node}},
			CreatedAt: now, UpdatedAt: now,
		}
	}
	copyA, copyB := mk("0.1"), mk("0.1") // the same claim, twice
	neighbour := mk("0.2")               // same words, different provenance: not a copy
	for _, a := range []entities.Atom{copyA, copyB, neighbour} {
		if err := repo.SaveAtom(ctx, a); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	store := &trashSpy{}
	uc := usecases.CollapseDuplicateAtoms{Atoms: store, AtomRepo: repo}

	got, err := uc.Detect(ctx)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if got.Groups != 1 || got.Files != 2 {
		t.Fatalf("detect = %+v, want ONE group of TWO copies (the shared-words claim is not a copy)", got)
	}

	collapsed, err := uc.Execute(ctx)
	if err != nil {
		t.Fatalf("collapse: %v", err)
	}
	if collapsed.Collapsed != 1 || len(store.trashed) != 1 {
		t.Fatalf("collapse = %+v, trashed %d files, want one", collapsed, len(store.trashed))
	}
	// The kept copy is the one with the lowest id; the other row is gone.
	kept := copyA
	if copyB.ID.String() < copyA.ID.String() {
		kept = copyB
	}
	if _, err := repo.GetAtom(ctx, kept.ID); err != nil {
		t.Errorf("the kept copy is gone: %v", err)
	}
	if _, err := repo.GetAtom(ctx, neighbour.ID); err != nil {
		t.Errorf("the shared-words claim was deleted: %v", err)
	}
	after, detectErr := uc.Detect(ctx)
	if detectErr != nil || after.Groups != 0 {
		t.Fatalf("detect after collapse = %+v (%v), want none", after, detectErr)
	}
}
