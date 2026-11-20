package usecases

import (
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// TestShelfCatalogIsTheShelf: the derivation's catalog is the bounded target vocabulary, not the
// observed names — that is what stops a new claim from inventing a fine name beside the shelf.
func TestShelfCatalogIsTheShelf(t *testing.T) {
	shelf := []string{"caching", "databases", "llm-agents"}
	observed := []entities.TopicUse{
		{Topic: "caching", Count: 7},
		{Topic: "cache-invalidation", Count: 9},
		{Topic: "databases", Count: 2},
	}
	got := shelfCatalog(shelf, observed)
	if len(got) != len(shelf) {
		t.Fatalf("catalog = %v, want exactly the shelf", got)
	}
	for i, name := range shelf {
		if got[i].Topic != name {
			t.Errorf("catalog[%d] = %q, want %q", i, got[i].Topic, name)
		}
	}
	if got[0].Count != 7 || got[2].Count != 0 {
		t.Errorf("counts = %d/%d, want the vault's count and 0 for an unused shelf name", got[0].Count, got[2].Count)
	}
}

// TestOffShelfNameIsMappedOntoTheShelf: with the shelf as the catalog, a proposal the model invents is
// filed under the best fitting shelf name by the existing budget path.
func TestOffShelfNameIsMappedOntoTheShelf(t *testing.T) {
	shelf := []string{"caching", "databases", "llm-agents"}
	cat := newTopicCatalog(shelfCatalog(shelf, []entities.TopicUse{{Topic: "caching", Count: 4}}))
	if len(cat.names) != 3 {
		t.Fatalf("catalog = %v, want 3 shelf names", cat.names)
	}
	for _, name := range cat.names {
		if name == "cache-invalidation" {
			t.Fatalf("an observed name leaked into the shelf catalog: %v", cat.names)
		}
	}
	// The budget is the shelf size, so an off-shelf proposal always takes the mapping path.
	cat.budget = len(shelf)

	res := cat.resolve(nil, "databases-sqlite-tuning", "databases tuning and the sqlite page cache")
	if res.Name != "databases" {
		t.Errorf("resolved %q to %q, want the shelf name databases", "databases-sqlite-tuning", res.Name)
	}
	if !res.Mapped || res.Why != "budget" {
		t.Errorf("resolution = %+v, want a budget mapping onto the shelf", res)
	}
	// A name that IS on the shelf passes through untouched.
	if same := cat.resolve(nil, "caching", "caching strategies"); same.Name != "caching" || same.Mapped {
		t.Errorf("a shelf name was rewritten: %+v", same)
	}
}
