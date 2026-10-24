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
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/atoms"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/vocabulary"
)

// TestRebranchClaimsRelabelsFromTheClaimText is the repair for a collapsed branch vocabulary: the label
// is re-derived from the CLAIM, because the claim text is the only evidence that survives a merge. No
// claim is rewritten and no page is touched — only the branch changes — and the branch vocabulary is
// rebuilt from what the claims now say (the alias table that recorded the collapse goes with it).
func TestRebranchClaimsRelabelsFromTheClaimText(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.UnixMilli(1_700_000_000_000).UTC()
	atomStore := atoms.New(filepath.Join(root, "atoms"))
	atomRepo := sqlc.NewAtomRepository(db)
	vocabStore := vocabulary.New(filepath.Join(root, "vocabulary"))

	claims := []string{
		"HTTP calls in Go are one-liners thanks to the managed memory.",
		"A context cancels work when its deadline passes.",
		"Goroutines are cheap user-space threads.",
	}
	var ids []ulid.ULID
	for i, claim := range claims {
		stored, wErr := atomStore.Write(ctx, entities.Atom{
			ID: ulid.Make(), Topic: "go", Path: "x.md", Claim: claim, Body: "b",
			Status: entities.AtomActive, CreatedAt: now.Add(time.Duration(i) * time.Second),
			UpdatedAt: now.Add(time.Duration(i) * time.Second),
		})
		if wErr != nil {
			t.Fatalf("write atom: %v", wErr)
		}
		if err := atomRepo.SaveAtom(ctx, stored); err != nil {
			t.Fatalf("save atom: %v", err)
		}
		ids = append(ids, stored.ID)
	}
	runID, err := usecases.StartRun{Runs: sqlc.NewRunRepository(db), Clock: fixedClock{}}.
		Execute(ctx, entities.RunVocabulary, "test", entities.Step(""), "rebranch")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}

	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		finalResponse(`{"claims":[{"n":1,"topic":"go-http"},{"n":2,"topic":"go-context"},{"n":3,"topic":"go"}]}`),
	}}
	uc := usecases.RebranchClaims{
		Atoms: atomRepo, AtomStore: atomStore, Vocab: vocabStore, Gateway: gw, Model: "test",
		Runs: sqlc.NewRunRepository(db), Clock: fixedClock{}, Apply: true,
	}
	res, err := uc.Execute(ctx, runID)
	if err != nil {
		t.Fatalf("rebranch: %v", err)
	}
	if res.Examined != 3 || res.Changed != 2 {
		t.Errorf("result = %+v, want 3 examined and 2 changed", res)
	}
	want := []string{"go-http", "go-context", "go"}
	for i, id := range ids {
		a, rErr := atomStore.Read(ctx, id)
		if rErr != nil {
			t.Fatalf("read atom %d: %v", i, rErr)
		}
		if a.Topic != want[i] {
			t.Errorf("claim %d branch = %q, want %q", i, a.Topic, want[i])
		}
		if a.Claim != claims[i] {
			t.Errorf("the claim was rewritten: %q", a.Claim)
		}
	}
	// The branch vocabulary mirrors what the claims now say, and the alias table of the collapse is gone.
	v, err := vocabStore.Read(entities.VocabularyTopics)
	if err != nil {
		t.Fatalf("read vocabulary: %v", err)
	}
	if len(v.Canonical) != 3 || len(v.Absorbed) != 0 {
		t.Errorf("vocabulary = %+v, want the three names and no absorbed table", v)
	}
}
