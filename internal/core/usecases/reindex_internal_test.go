package usecases

import (
	"context"
	"errors"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// derivedStore is the file store half the rebuild needs. The embedded interface supplies every
// method these tests never call; a call to one of them panics, which is the point: `derived` must not
// reach for anything else.
type derivedStore struct {
	interfaces.AtomStore
	atoms      []entities.Atom
	unreadable []string
	trashed    []ulid.ULID
	removed    []ulid.ULID
}

func (s *derivedStore) Scan(context.Context) ([]entities.Atom, []string, error) {
	return s.atoms, s.unreadable, nil
}

func (s *derivedStore) Trash(_ context.Context, a entities.Atom) error {
	s.trashed = append(s.trashed, a.ID)
	return nil
}

func (s *derivedStore) Remove(_ context.Context, a entities.Atom) error {
	s.removed = append(s.removed, a.ID)
	return nil
}

func (s *derivedStore) Write(_ context.Context, a entities.Atom) (entities.Atom, error) {
	return a, nil
}

// derivedRepo is the index half: the rows the rebuild reads and the writes it makes.
type derivedRepo struct {
	interfaces.AtomRepository
	rows    []entities.Atom
	saved   []ulid.ULID
	deleted []ulid.ULID
	saveErr error
}

func (r *derivedRepo) AtomAddresses(context.Context) ([]entities.Atom, error) {
	return r.rows, nil
}

func (r *derivedRepo) DeleteAtom(_ context.Context, id ulid.ULID) error {
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *derivedRepo) SaveAtom(_ context.Context, a entities.Atom) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, a.ID)
	return nil
}

func derivedFixture(store *derivedStore, repo *derivedRepo) Maintenance {
	return Maintenance{AtomStore: store, Atoms: repo}
}

// TestDerivedRefusesAnUnreadableClaimFile: a file the rebuild cannot read is a reason to stop, never
// a reason to delete. The old pass skipped it, dropped its row and removed the file.
func TestDerivedRefusesAnUnreadableClaimFile(t *testing.T) {
	store := &derivedStore{unreadable: []string{"atoms/ab/cd/broken.md"}}
	repo := &derivedRepo{}
	uc := derivedFixture(store, repo)

	_, err := uc.derived(context.Background(), ReindexResult{}, map[string]entities.SourceRecord{})
	if err == nil {
		t.Fatal("derived accepted an unreadable claim file")
	}
	if len(store.trashed) != 0 || len(store.removed) != 0 || len(repo.saved) != 0 || len(repo.deleted) != 0 {
		t.Fatalf("an unreadable file still changed something: trashed=%d removed=%d saved=%d deleted=%d",
			len(store.trashed), len(store.removed), len(repo.saved), len(repo.deleted))
	}
}

// TestDerivedRefusesWithoutRecords: the source records are what tells a claim from an orphan. Without
// them every claim would look lost, so the pass must not run at all.
func TestDerivedRefusesWithoutRecords(t *testing.T) {
	store := &derivedStore{atoms: []entities.Atom{{ID: ulid.Make(), Sources: []entities.AtomSource{{Hash: "x"}}}}}
	repo := &derivedRepo{}
	uc := derivedFixture(store, repo)

	if _, err := uc.derived(context.Background(), ReindexResult{}, nil); err == nil {
		t.Fatal("derived ran without the source records")
	}
	if len(store.trashed) != 0 {
		t.Fatalf("trashed %d claim(s) without the records", len(store.trashed))
	}
}

// TestDerivedDoesNotTrashOnACanceledContext: this is the bug that emptied the trash into the vault.
// Once the rebuild's context is gone, nothing may be trashed — the work simply stops.
func TestDerivedDoesNotTrashOnACanceledContext(t *testing.T) {
	store := &derivedStore{atoms: []entities.Atom{{
		ID: ulid.Make(), Claim: "a claim", Sources: []entities.AtomSource{{Hash: "gone"}},
	}}}
	repo := &derivedRepo{}
	uc := derivedFixture(store, repo)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := uc.derived(ctx, ReindexResult{}, map[string]entities.SourceRecord{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("derived = %v, want context.Canceled", err)
	}
	if len(store.trashed) != 0 {
		t.Fatalf("a canceled rebuild trashed %d claim(s)", len(store.trashed))
	}
}

// TestDerivedTrashesOnlyWhenTheVaultHasNoRecord: the RECORD decides, not the index. A source the
// index does not know yet is not a source the vault lost.
func TestDerivedTrashesOnlyWhenTheVaultHasNoRecord(t *testing.T) {
	orphan := entities.Atom{ID: ulid.Make(), Claim: "orphan", Sources: []entities.AtomSource{{Hash: "gone"}}}
	kept := entities.Atom{ID: ulid.Make(), Claim: "kept", Sources: []entities.AtomSource{{Hash: "here"}}}
	store := &derivedStore{atoms: []entities.Atom{orphan, kept}}
	repo := &derivedRepo{}
	uc := derivedFixture(store, repo)

	res, err := uc.derived(context.Background(), ReindexResult{}, map[string]entities.SourceRecord{"here": {}})
	if err != nil {
		t.Fatalf("derived: %v", err)
	}
	if len(store.trashed) != 1 || store.trashed[0] != orphan.ID {
		t.Fatalf("trashed = %v, want only the orphan", store.trashed)
	}
	if len(repo.saved) != 1 || repo.saved[0] != kept.ID {
		t.Fatalf("saved = %v, want only the kept claim", repo.saved)
	}
	if res.AtomsOrphaned != 1 || res.Atoms != 1 {
		t.Fatalf("res = %+v, want one orphan and one indexed claim", res)
	}
}

// TestDerivedDropsAStaleRowAndKeepsItsFile: a row without a file is stale, but the rebuild only ever
// removes the ROW. The file is the vault; a repair that deletes it destroys what it was repairing.
func TestDerivedDropsAStaleRowAndKeepsItsFile(t *testing.T) {
	stale := entities.Atom{ID: ulid.Make(), Path: "ab/cd/stale.md"}
	store := &derivedStore{}
	repo := &derivedRepo{rows: []entities.Atom{stale}}
	uc := derivedFixture(store, repo)

	res, err := uc.derived(context.Background(), ReindexResult{}, map[string]entities.SourceRecord{})
	if err != nil {
		t.Fatalf("derived: %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != stale.ID {
		t.Fatalf("deleted rows = %v, want the stale one", repo.deleted)
	}
	if len(store.removed) != 0 {
		t.Fatalf("the rebuild removed %d file(s) from the vault", len(store.removed))
	}
	if res.AtomsRemoved != 1 {
		t.Fatalf("AtomsRemoved = %d, want 1", res.AtomsRemoved)
	}
}

// TestDerivedCountsAnUnindexedAtomAsSkipped: an atom that could not be written is a PARTIAL pass, and
// Reindex uses that count to skip the part re-arm that would re-distill the whole document.
func TestDerivedCountsAnUnindexedAtomAsSkipped(t *testing.T) {
	a := entities.Atom{ID: ulid.Make(), Claim: "c", Sources: []entities.AtomSource{{Hash: "here"}}}
	store := &derivedStore{atoms: []entities.Atom{a}}
	repo := &derivedRepo{saveErr: errors.New("database is locked")}
	uc := derivedFixture(store, repo)

	res, err := uc.derived(context.Background(), ReindexResult{}, map[string]entities.SourceRecord{"here": {}})
	if err != nil {
		t.Fatalf("derived: %v", err)
	}
	if res.Skipped != 1 || res.Atoms != 0 {
		t.Fatalf("res = %+v, want one skipped and no indexed claim", res)
	}
}
