package sqlc

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	atomstore "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/atoms"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// TestSaveAtomKeepsProvenanceWhenSourceIsMissing is the regression guard for the silent
// skip that made a source show zero claims: an unknown source hash must be an error, and
// the error must not wipe the provenance (and tags) the atom already had.
func TestSaveAtomKeepsProvenanceWhenSourceIsMissing(t *testing.T) {
	ctx := context.Background()
	_, atoms, _, _, atomID := seedVault(t)

	before, err := atoms.GetAtom(ctx, atomID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(before.Sources) != 1 {
		t.Fatalf("precondition: atom has %d sources, want 1", len(before.Sources))
	}

	bad := before
	bad.Sources = []entities.AtomSource{{Hash: "not-an-indexed-source", NodeID: "0.1"}}
	bad.Claim = "a rewritten claim"
	if err := atoms.SaveAtom(ctx, bad); err == nil {
		t.Fatal("saving an atom with an unknown source hash must fail")
	}

	after, err := atoms.GetAtom(ctx, atomID)
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if len(after.Sources) != 1 || after.Sources[0].Hash != before.Sources[0].Hash {
		t.Errorf("provenance was destroyed: %+v", after.Sources)
	}
	if after.Claim != before.Claim {
		t.Errorf("the failed write was not rolled back: claim = %q", after.Claim)
	}
}

// TestReindexRestoresAtomProvenanceFromFiles is the repair path: the atom's Markdown file
// keeps its `sources:`, so a reindex puts the provenance back in the index even after the
// rows were lost.
func TestReindexRestoresAtomProvenanceFromFiles(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := objects.New(t.TempDir())
	index := NewSourceRepository(db)
	atomsRepo := NewAtomRepository(db)
	atomStore := atomstore.New(t.TempDir())

	src, err := usecases.IntakeSource{Store: store, Index: index, Clock: testClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "a.md", Reader: strings.NewReader("# A\nbody\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}

	atomID := ulid.Make()
	if err := atomsRepo.SaveAtom(ctx, entities.Atom{
		ID: atomID, Topic: "eng", Path: "eng/a.md", Claim: "a claim", Body: "body",
		Status:    entities.AtomActive,
		Sources:   []entities.AtomSource{{Hash: src.Hash, NodeID: "0.1", OCR: true}},
		CreatedAt: testClock{}.Now(), UpdatedAt: testClock{}.Now(),
	}); err != nil {
		t.Fatalf("save atom: %v", err)
	}
	// The pipeline writes the file first; it is what carries the provenance.
	a, err := atomsRepo.GetAtom(ctx, atomID)
	if err != nil {
		t.Fatalf("get atom: %v", err)
	}
	if _, err := atomStore.Write(ctx, a); err != nil {
		t.Fatalf("write atom file: %v", err)
	}

	// Damage: the source row is deleted and re-added, so its atom_sources rows cascade away.
	if err := index.DeleteSourceByHash(ctx, src.Hash); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	if err := index.UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: src.Hash, Kind: entities.SourceKind(src.Kind), Ext: src.Ext,
		Path: src.Path, Title: src.Title, Bytes: src.Bytes, TreeState: entities.TreeState(src.TreeState),
		ImportedAt: src.ImportedAt, Meta: "{}",
	}); err != nil {
		t.Fatalf("re-add source: %v", err)
	}
	if got, err := atomsRepo.GetAtom(ctx, atomID); err != nil {
		t.Fatalf("get damaged: %v", err)
	} else if len(got.Sources) != 0 {
		t.Fatalf("precondition: provenance still present: %+v", got.Sources)
	}

	reindex := usecases.ReindexVault{
		Store: store, Index: index, Atoms: atomsRepo,
		AtomStore: atomStore, Clock: testClock{}, Log: slog.Default(),
	}
	res, err := reindex.Execute(ctx)
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if res.Skipped != 0 {
		t.Errorf("reindex skipped %d file(s): %+v", res.Skipped, res)
	}

	got, err := atomsRepo.GetAtom(ctx, atomID)
	if err != nil {
		t.Fatalf("get restored: %v", err)
	}
	if len(got.Sources) != 1 || got.Sources[0].Hash != src.Hash || got.Sources[0].NodeID != "0.1" || !got.Sources[0].OCR {
		t.Errorf("provenance not restored: %+v", got.Sources)
	}
	if n, err := atomsRepo.ListAtomsBySource(ctx, src.Hash); err != nil {
		t.Fatalf("list by source: %v", err)
	} else if len(n) != 1 || n[0].ID != atomID {
		t.Errorf("the source still shows %d claims, want 1", len(n))
	}
}
