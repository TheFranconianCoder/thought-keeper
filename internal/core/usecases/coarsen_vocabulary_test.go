package usecases

import (
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// TestShelfForBatchIsNarrowed: the prompt must carry the shelf HEAD plus the names that share a word
// with the batch — not all 1,000 names for every 600 filed ones.
func TestShelfForBatchIsNarrowed(t *testing.T) {
	shelf := []string{"caching", "kubernetes", "llm-agents", "databases", "testing"}
	index := map[string][]string{}
	for _, name := range shelf {
		for _, token := range uniqueTokens(name) {
			index[token] = append(index[token], name)
		}
	}
	batch := []string{"cache-invalidation", "kubernetes-operator"}
	got := shelfForBatch(batch, []string{"caching"}, index, 10)
	joined := strings.Join(got, ",")
	for _, want := range []string{"caching", "kubernetes"} {
		if !strings.Contains(joined, want) {
			t.Errorf("shelf for the batch lacks %q: %v", want, got)
		}
	}
	for _, unwanted := range []string{"testing", "databases"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("shelf carries an unrelated name %q: %v", unwanted, got)
		}
	}
	// The cap is honoured.
	if capped := shelfForBatch(batch, shelf, index, 2); len(capped) != 2 {
		t.Errorf("cap ignored: %d names", len(capped))
	}
}

// TestTopObservedKeepsTheOrder: the shelf is built from the most used names, in the catalog's order.
func TestTopObservedKeepsTheOrder(t *testing.T) {
	observed := []partialentities.NameUses{
		{Name: "caching", Uses: 900},
		{Name: "kubernetes", Uses: 400},
		{Name: "testing", Uses: 300},
	}
	got := topObserved(observed, 2)
	if strings.Join(got, ",") != "caching,kubernetes" {
		t.Errorf("topObserved = %v", got)
	}
	if all := topObserved(observed, 0); len(all) != 3 {
		t.Errorf("a zero limit must keep every name, got %v", all)
	}
}
