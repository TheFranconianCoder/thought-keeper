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

// TestSearchFindsAClaimByItsIDPrefix: the id on the claim detail is the one thing a reader copies,
// so a (partial) paste of it must find that claim — even though the stored id is a 16-byte blob and
// the reader sees a 26-character ULID.
func TestSearchFindsAClaimByItsIDPrefix(t *testing.T) {
	ctx := context.Background()
	db, _, _, _, atomID := seedVault(t)
	if !persistence.EnsureFTS(db, slog.Default()) {
		t.Skip("no FTS5")
	}
	search := NewSearchRepository(db, true)
	prefix := atomID.String()[:10]

	hits, err := search.Search(ctx, prefix, "atoms", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != atomID.String() || hits[0].Kind != "atom" {
		t.Fatalf("hits = %+v, want the claim %s", hits, atomID)
	}

	// A prefix that matches nothing must not turn into a scan of every claim.
	if hits, err := search.Search(ctx, "ZZZZZZ", "atoms", 10); err != nil || len(hits) != 0 {
		t.Fatalf("unknown prefix: hits=%+v err=%v", hits, err)
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

// TestSearchExactDoesNotApproximateAName: the "mentions X" channel must require every word. A
// Sebastian who is not a Bauer must not turn into a match for "Sebastian Bauer".
func TestSearchExactDoesNotApproximateAName(t *testing.T) {
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
		{ID: ulid.Make(), Topic: "people", Path: "people/cockburn.md", Claim: "Alistair Cockburn is a co-author of the Agile Manifesto.", Body: "Alistair Cockburn originated the Hexagonal Architecture style.", Status: entities.AtomActive, Sources: []entities.AtomSource{{Hash: hash, NodeID: "0.1"}}, CreatedAt: now, UpdatedAt: now},
		{ID: ulid.Make(), Topic: "people", Path: "people/sebastian.md", Claim: "Sebastián Ismael speaks at Agile conferences.", Body: "Sebastián Ismael is a software professional from Argentina.", Status: entities.AtomActive, Sources: []entities.AtomSource{{Hash: hash, NodeID: "0.1"}}, CreatedAt: now, UpdatedAt: now},
	} {
		if err := atoms.SaveAtom(ctx, a); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	search := NewSearchRepository(db, true)
	hits, err := search.SearchExact(ctx, "Alistair Cockburn", "atoms", 10)
	if err != nil {
		t.Fatalf("exact search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("an exact name was not found")
	}
	hits, err = search.SearchExact(ctx, "Sebastian Bauer", "atoms", 10)
	if err != nil {
		t.Fatalf("exact search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("an absent name was approximated: %+v", hits)
	}
}

// TestSearchInSourceKeepsTheBoundary: scoped Ask must not see another source's claim even when the
// global index matches both.
func TestSearchInSourceKeepsTheBoundary(t *testing.T) {
	ctx := context.Background()
	db, atoms, sources, _, _ := seedVault(t)
	if !persistence.EnsureFTS(db, slog.Default()) {
		t.Skip("no FTS5")
	}
	other := strings.Repeat("b", 64)
	now := testClock{}.Now()
	if err := sources.UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: other, Kind: entities.SourceKindMD, Ext: ".md",
		Path: other + ".md", Title: "Another source", Bytes: 10, TreeState: entities.TreeBuilt,
		ImportedAt: now, Meta: "{}",
	}); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if err := atoms.SaveAtom(ctx, entities.Atom{
		ID: ulid.Make(), Topic: "eng", Path: "eng/b-claim.md", Claim: "A claim from the other source",
		Body: "b", Status: entities.AtomActive,
		Sources:   []entities.AtomSource{{Hash: other, NodeID: "0.1"}},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save atom: %v", err)
	}

	search := NewSearchRepository(db, true)
	hits, err := search.SearchInSource(ctx, "claim", other, 10)
	if err != nil {
		t.Fatalf("search in source: %v", err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Title, "other source") {
		t.Fatalf("hits = %+v, want only the scoped source's claim", hits)
	}
}
