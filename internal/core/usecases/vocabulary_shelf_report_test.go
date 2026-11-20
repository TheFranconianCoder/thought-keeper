package usecases

import (
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// TestHealthCountsWhatTheCoarseningStillHasToFile: the number is the names in use that are neither on
// the shelf nor absorbed — what a coarsening press would rewrite.
func TestHealthCountsWhatTheCoarseningStillHasToFile(t *testing.T) {
	observed := []partialentities.NameUses{
		{Name: "caching", Uses: 5},
		{Name: "compilers", Uses: 3},
		{Name: "cache-invalidation", Uses: 2},
	}
	vocab := entities.Vocabulary{
		Namespace: entities.VocabularyTopics,
		Shelf:     []string{"caching"},
		Absorbed:  map[string]string{"compilers": "caching"},
	}
	h := (Vocabulary{TopNames: 20}).health(observed, vocab, entities.VocabularyTopics)
	if h.Shelf != 1 {
		t.Errorf("shelf = %d, want 1", h.Shelf)
	}
	if h.Remaining != 1 {
		t.Errorf("remaining = %d, want 1 (cache-invalidation)", h.Remaining)
	}
}

// TestHealthWithoutAShelfHasTheWholeCatalogRemaining: a vault that never built one is all tail, and the
// report says so instead of reading as done.
func TestHealthWithoutAShelfHasTheWholeCatalogRemaining(t *testing.T) {
	observed := []partialentities.NameUses{
		{Name: "caching", Uses: 5},
		{Name: "compilers", Uses: 3},
	}
	h := (Vocabulary{TopNames: 20}).health(observed, entities.Vocabulary{Namespace: entities.VocabularyTopics, Absorbed: map[string]string{}}, entities.VocabularyTopics)
	if h.Shelf != 0 {
		t.Errorf("shelf = %d, want 0", h.Shelf)
	}
	if h.Remaining != 2 {
		t.Errorf("remaining = %d, want the whole catalog (2)", h.Remaining)
	}
}
