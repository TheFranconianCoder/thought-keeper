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

func TestVariantGroupsCoverDerivationAndInflection(t *testing.T) {
	chaos := variantGroups([]string{"chaos"}, []string{"de"})
	if len(chaos) != 1 {
		t.Fatalf("groups = %v", chaos)
	}
	query := ftsVariantQuery(chaos, " OR ")
	if !strings.Contains(query, `"chaos"`) || !strings.Contains(query, `"chao"*`) {
		t.Fatalf("query = %q, want exact chaos and the chao* family", query)
	}

	chaotic := variantGroups([]string{"chaotisch"}, []string{"de"})
	q2 := ftsVariantQuery(chaotic, " OR ")
	if !strings.Contains(q2, `"chaot"*`) || !strings.Contains(q2, `"chao"*`) {
		t.Fatalf("query = %q, want the German stem and the shared prefix", q2)
	}
}

func TestVariantGroupsCoverEnglishInflection(t *testing.T) {
	groups := variantGroups([]string{"caching"}, []string{"en"})
	q := ftsVariantQuery(groups, " OR ")
	if !strings.Contains(q, `"cach"*`) {
		t.Fatalf("query = %q, want the caching/cache family", q)
	}
}

func TestVariantGroupsCoverUmlautFamilies(t *testing.T) {
	groups := variantGroups([]string{"Häuser"}, []string{"de"})
	q := ftsVariantQuery(groups, " OR ")
	if !strings.Contains(q, `"haus"*`) && !strings.Contains(q, `"hau"*`) {
		t.Fatalf("query = %q, want a prefix that also matches Haus", q)
	}
}

func TestSearchFindsDerivationalVariant(t *testing.T) {
	ctx := context.Background()
	db, atoms, sources, _, atomID := seedVault(t)
	if !persistence.EnsureFTS(db, slog.Default()) {
		t.Skip("no FTS5")
	}
	base, err := atoms.GetAtom(ctx, atomID)
	if err != nil {
		t.Fatal(err)
	}
	hash := base.Sources[0].Hash
	if err := sources.SetSourceLanguage(ctx, hash, "de"); err != nil {
		t.Fatal(err)
	}
	now := testClock{}.Now()
	for _, a := range []entities.Atom{
		{ID: ulid.Make(), Topic: "chaos", Path: "eng/chaos.md", Claim: "Chaos entsteht aus kleinen Abweichungen.", Body: "Chaos ist ein Zustand.", Status: entities.AtomActive, Sources: []entities.AtomSource{{Hash: hash, NodeID: "0.1"}}, CreatedAt: now, UpdatedAt: now},
		{ID: ulid.Make(), Topic: "chaos", Path: "eng/chaotisch.md", Claim: "Ein chaotisches System ist schwer vorherzusagen.", Body: "Chaotisch bedeutet ungeordnet.", Status: entities.AtomActive, Sources: []entities.AtomSource{{Hash: hash, NodeID: "0.1"}}, CreatedAt: now, UpdatedAt: now},
	} {
		if err := atoms.SaveAtom(ctx, a); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	search := NewSearchRepository(db, true)
	hits, err := search.Search(ctx, "Chaos", "atoms", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	foundChaotic := false
	for _, h := range hits {
		if strings.Contains(h.Title, "chaotisches") {
			foundChaotic = true
		}
	}
	if !foundChaotic {
		t.Fatalf("derivational variant not found: %+v", hits)
	}
}
