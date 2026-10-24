package sqlc

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"

	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

func TestSearchFindsClaim(t *testing.T) {
	ctx := context.Background()
	db, _, _, _, _ := seedVault(t)
	if !persistence.EnsureFTS(db, slog.Default()) {
		t.Skip("no FTS5")
	}

	search := NewSearchRepository(db, true)
	hits, err := search.Search(ctx, "claim", "", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].Title != "A claim" || hits[0].Kind != "atom" {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestSearchHandlesSymbolTerms(t *testing.T) {
	ctx := context.Background()
	db, atoms, _, _, atomID := seedVault(t)
	if !persistence.EnsureFTS(db, slog.Default()) {
		t.Skip("no FTS5")
	}
	base, err := atoms.GetAtom(ctx, atomID)
	if err != nil {
		t.Fatal(err)
	}
	hash := base.Sources[0].Hash
	now := testClock{}.Now()
	for _, a := range []entities.Atom{
		{ID: ulid.Make(), Topic: "cplusplus", Path: "eng/cpp.md", Claim: "C++ is a complex language with manual memory management.", Body: "The language leaves memory management to the programmer.", Status: entities.AtomActive, Sources: []entities.AtomSource{{Hash: hash, NodeID: "0.1"}}, CreatedAt: now, UpdatedAt: now},
		{ID: ulid.Make(), Topic: "go", Path: "eng/go.md", Claim: "Go values simplicity and readability.", Body: "The language has a garbage collector.", Status: entities.AtomActive, Sources: []entities.AtomSource{{Hash: hash, NodeID: "0.1"}}, CreatedAt: now, UpdatedAt: now},
	} {
		if err := atoms.SaveAtom(ctx, a); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	search := NewSearchRepository(db, true)
	hits, err := search.Search(ctx, "C++ language complexity", "", 20)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	found := false
	for _, h := range hits {
		if strings.Contains(h.Title, "C++") {
			found = true
		}
	}
	if !found {
		t.Fatalf("C++ claim not found: %+v", hits)
	}
}
