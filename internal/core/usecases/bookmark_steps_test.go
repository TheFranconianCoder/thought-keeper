package usecases_test

import (
	"context"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/sourcemeta"
)

// TestResetOfABookmarkRestoresItsSkippedLights: a bookmark has no derivation, but its record still says
// WHY the document steps do not apply — "skipped · bookmark" is what keeps it out of the vault-wide
// barrier. A reset keeps only the capture entry, so a collected link that was reset came back looking
// like a document whose outline was owed: the pump queued a tree build, the build returned without
// writing anything, and the retry held the OUTLINE stage — and with it the distill of every source in
// the vault — for as long as the app ran.
func TestResetOfABookmarkRestoresItsSkippedLights(t *testing.T) {
	ctx := context.Background()
	hash := entities.URLHash("https://example.com/blog/")
	idx := newFakeIndex()
	idx.m[hash] = entities.Source{
		ID: ulid.Make(), Hash: hash, Title: "Blog", Kind: entities.SourceKindURL, Ext: ".md",
		URL: "https://example.com/blog/", Bookmark: true,
	}
	steps := sourcemeta.NewTracker(sourcemeta.New(t.TempDir()), nil, nil)
	if err := steps.Record(ctx, hash, entities.StepCapture, entities.StepStatus{State: entities.StepOK}); err != nil {
		t.Fatalf("record capture: %v", err)
	}

	reset := usecases.Derivation{
		Atoms: &fakeAtomRepo{}, AtomStore: &fakeAtomStore{m: map[string]entities.Atom{}},
		PartRepo: &fakeSections{m: map[string]entities.PartState{}}, Index: idx,
		Steps: steps, Clock: fixedClock{},
	}
	if _, err := reset.Reset(ctx, hash); err != nil {
		t.Fatalf("reset: %v", err)
	}

	rec, err := steps.Steps(hash)
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	if got := rec.State(entities.StepOutline); got != entities.StepSkipped {
		t.Errorf("outline = %s, want skipped (a bookmark has no document)", got)
	}
	if got := rec.State(entities.StepDistill); got != entities.StepSkipped {
		t.Errorf("distill = %s, want skipped (a bookmark has no document)", got)
	}
	if got := rec.State(entities.StepCapture); got != entities.StepOK {
		t.Errorf("capture = %s, want ok (a reset keeps the capture)", got)
	}
}

// TestReAddingACollectedLinkKeepsItsDecision: POST /add of an address the vault already has is a
// refresh, not a re-classification. The reader decided once (by not confirming the list) and a fetch
// cannot decide for them. Recomputing the answer for a KNOWN source wrote "not a bookmark" into the
// record while the row kept "collected link": the payload, the tree build and the sweep read one
// answer, the record the other, and the reset that followed cleared the two skipped lights — which is
// how one collected link pinned the pipeline for the whole vault.
func TestReAddingACollectedLinkKeepsItsDecision(t *testing.T) {
	ctx := context.Background()
	// The page is a card grid whose excerpts are long: a newsletter archive. Until the reader answers,
	// it is a collected link (see TestAddURLKeepsACardGridOutOfTheDistillQueue).
	links := cards(blogURL, 9, "")
	markdown := strings.Repeat("card excerpt text ", 100) + " " + strings.Join(itemURLsOf(links), " ")
	uc, _, idx, _ := newListingUseCase(t, fakeLinks{blogURL: links}, markdown)
	steps := sourcemeta.NewTracker(sourcemeta.New(t.TempDir()), nil, nil)
	uc.Steps = steps

	first, err := uc.AddManual(ctx, blogURL, false, usecases.ListingAuto)
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	if !first.Bookmark {
		t.Fatal("the card grid was not collected as a link")
	}
	if _, err := uc.AddManual(ctx, blogURL, false, usecases.ListingAuto); err != nil {
		t.Fatalf("second add: %v", err)
	}

	stored, err := idx.GetSourceByHash(ctx, entities.URLHash(blogURL))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !stored.Bookmark {
		t.Error("the index stopped calling the collected link a bookmark")
	}
	id, err := steps.Identity(entities.URLHash(blogURL))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if !id.Bookmark {
		t.Error("the record lost the bookmark flag while the index kept it — a reindex would make this a document")
	}
}
