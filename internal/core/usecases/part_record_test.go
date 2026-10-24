package usecases_test

import (
	"context"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/sourcemeta"
	treestore "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/tree"
)

// TestPartRecordsLiveInTheVaultAndComeBack: the part record is the ONE piece of derived state the
// index cannot rebuild (the claims are files, the outline is a file, the rendering is a file — but
// "this part was finished" is a fact about a run). If it only lived in the database, a vault whose
// index was recreated would show green lights over "0 of 161 parts done" — which is exactly the
// confusion this test pins down: the record is written next to the source and a reindex restores it.
func TestPartRecordsLiveInTheVaultAndComeBack(t *testing.T) {
	ctx := context.Background()
	sourcesDir := t.TempDir()
	store := objects.New(sourcesDir)
	idx := newFakeIndex()
	trees := treestore.New(sourcesDir)
	extractor := extract.New(gateways.ExecRunner{}, extract.Config{})

	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "notes.md", Reader: strings.NewReader("# Caching\ncaching is subtle\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if _, err := (usecases.BuildTree{Store: store, Index: idx, Trees: trees, Extractor: extractor, Clock: fixedClock{}}).
		Execute(ctx, src.Hash); err != nil {
		t.Fatalf("outline: %v", err)
	}

	// The distill step records its parts through the tracker (this is what the use case does).
	meta := sourcemeta.New(sourcesDir)
	steps := sourcemeta.NewTracker(meta, idx, nil)
	node := idx.nodes[src.ID][1].NodeID
	if err := steps.SetParts(src.Hash, map[string]entities.PartRecord{
		node: {State: entities.PartDone, Atoms: 3, At: fixedClock{}.Now()},
	}); err != nil {
		t.Fatalf("set parts: %v", err)
	}

	rec, err := meta.Read(src.Hash)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if len(rec.Parts) != 1 || rec.Parts[node].State != entities.PartDone {
		t.Fatalf("the part record is not in the source's .meta file: %+v", rec.Parts)
	}

	// A RECREATED INDEX: the part records are gone, the vault still has them. The claim that cites
	// the part is part of that truth: a done record without an atom is not done.
	atom := entities.Atom{
		ID: ulid.Make(), Claim: "caching is subtle", Body: "body", Status: entities.AtomActive,
		Sources: []entities.AtomSource{{Hash: src.Hash, NodeID: node}},
	}
	atomRepo := &fakeAtomRepo{saved: []entities.Atom{atom}}
	atomStore := &fakeAtomStore{m: map[string]entities.Atom{atom.ID.String(): atom}}
	parts := &fakeParts{}
	reindex := usecases.ReindexVault{
		Store: store, Index: idx, Atoms: atomRepo, AtomStore: atomStore,
		Steps: steps, Parts: parts, Clock: fixedClock{},
	}
	if _, err := reindex.Execute(ctx); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	got, ok := parts.m[src.ID][node]
	if !ok || got.State != entities.PartDone || got.Atoms != 3 {
		t.Fatalf("reindex did not restore the part record: %+v", parts.m)
	}

	// Now the ATOMS ARE GONE (the user kept sources/ and deleted atoms/). A reindex must invalidate
	// the done part records instead of leaving "24/25 distilled" over no claims.
	atomRepo.saved = nil
	atomStore.m = map[string]entities.Atom{}
	if _, err := reindex.Execute(ctx); err != nil {
		t.Fatalf("reindex after deleting atoms: %v", err)
	}
	got, ok = parts.m[src.ID][node]
	if !ok || got.State != entities.PartIncomplete || got.Reason != "missing_atoms" {
		t.Fatalf("stale part record was not invalidated: %+v", got)
	}

	// A reset throws the derivation away — the part record goes with it (stale "done" records would
	// skip parts that no longer exist).
	if err := steps.Reset(src.Hash); err != nil {
		t.Fatalf("reset: %v", err)
	}
	after, _ := meta.Read(src.Hash)
	if len(after.Parts) != 0 {
		t.Errorf("the part records survived a reset: %+v", after.Parts)
	}
}

// fakeParts records the part states a reindex restores.
type fakeParts struct {
	m map[ulid.ULID]map[string]entities.PartState
}

func (f *fakeParts) SetPartState(_ context.Context, sourceID ulid.ULID, s entities.PartState) error {
	if f.m == nil {
		f.m = map[ulid.ULID]map[string]entities.PartState{}
	}
	if f.m[sourceID] == nil {
		f.m[sourceID] = map[string]entities.PartState{}
	}
	f.m[sourceID][s.NodeID] = s
	return nil
}

func (f *fakeParts) PartStates(_ context.Context, sourceID ulid.ULID) (map[string]entities.PartState, error) {
	return f.m[sourceID], nil
}

func (f *fakeParts) PruneWithoutText(context.Context) (int64, error) { return 0, nil }

func (f *fakeParts) ClearParts(_ context.Context, sourceID ulid.ULID) error {
	delete(f.m, sourceID)
	return nil
}
