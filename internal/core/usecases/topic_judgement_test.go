package usecases

import (
	"fmt"
	"slices"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// judgedCatalog is a catalog judged by refitLimits over a corpus of refitCorpus claims. The names are
// given with their counts; the rest of the corpus is not in the catalog but counts toward the total.
func judgedCatalog(topics ...entities.TopicUse) *topicCatalog {
	c := newTopicCatalog(topics)
	c.judgeBy(refitLimits, refitCorpus)
	return c
}

// TestCandidatesSkipCatchAllAndThin: a part is never offered a name that means everything or a name
// nobody uses. A generic word that happens to share the text's words must not pull claims toward it.
func TestCandidatesSkipCatchAllAndThin(t *testing.T) {
	c := judgedCatalog(
		entities.TopicUse{Topic: "software", Count: 3000},
		entities.TopicUse{Topic: "caching", Count: 100},
		entities.TopicUse{Topic: "cache-warming", Count: 100},
		entities.TopicUse{Topic: "cache-rare", Count: 1},
	)
	got := c.candidates("software caching cache", 5)
	if slices.Contains(got, "software") || slices.Contains(got, "cache-rare") {
		t.Errorf("candidates = %v, want no catch-all and no thin name", got)
	}
	if !slices.Contains(got, "caching") {
		t.Errorf("candidates = %v, want the in-band names", got)
	}
}

// TestNearDuplicateNeverMapsOntoAnOutlier: "cache-invalidation-strategy" is a spelling of
// "cache-invalidation" only when that name is a subject. A catch-all is not a subject, so the claim keeps
// its own proposal instead of being pushed into it.
func TestNearDuplicateNeverMapsOntoAnOutlier(t *testing.T) {
	c := judgedCatalog(
		entities.TopicUse{Topic: "cache-invalidation", Count: 3000},
		entities.TopicUse{Topic: "caching", Count: 100},
	)
	got := c.resolve(nil, "cache-invalidation-strategy", "")
	if got.Mapped || got.Name != "cache-invalidation-strategy" {
		t.Errorf("resolution = %+v, want the proposal kept, not mapped onto the catch-all", got)
	}
}

// TestBudgetMapsOnlyOntoAnInBandName: over the budget a new subject is mapped onto the best fitting
// name — and only one inside the band. The catch-all fits the text best, and it is not a target.
func TestBudgetMapsOnlyOntoAnInBandName(t *testing.T) {
	c := judgedCatalog(
		entities.TopicUse{Topic: "software", Count: 3000},
		entities.TopicUse{Topic: "caching", Count: 100},
	)
	c.budget = 2
	got := c.resolve(nil, "tp-link-router-setup", "software caching")
	if got.Name != "caching" || !got.Mapped || got.Why != "budget" {
		t.Errorf("resolution = %+v, want the in-band caching", got)
	}
}

// TestOverBudgetWithNoInBandNameKeepsTheProposal: when no in-band name fits, the claim is not forced
// into an outlier. The proposal stays as a new name, and the refit and the coarsening judge it later.
func TestOverBudgetWithNoInBandNameKeepsTheProposal(t *testing.T) {
	c := judgedCatalog(
		entities.TopicUse{Topic: "software", Count: 3000},
	)
	c.budget = 1
	got := c.resolve(nil, "novel-subject", "software")
	if got.Mapped || got.Name != "novel-subject" {
		t.Errorf("resolution = %+v, want the proposal kept", got)
	}
}

// TestAnExistingOutlierIsKeptAsProposed: a proposal that names an existing catch-all is kept as proposed.
// It is not moved to another name to make the count look better; the refit evicts it and the retopic pass
// re-derives its claims from their text.
func TestAnExistingOutlierIsKeptAsProposed(t *testing.T) {
	c := judgedCatalog(
		entities.TopicUse{Topic: "software", Count: 3000},
		entities.TopicUse{Topic: "caching", Count: 100},
	)
	c.budget = 2
	got := c.resolve(nil, "software", "caching")
	if got.Name != "software" || got.Mapped {
		t.Errorf("resolution = %+v, want the existing name kept", got)
	}
}

// TestCoarseningOffersOnlyInBandShelfNames: the coarsening files the tail onto shelf names a claim may
// carry. A catch-all or a thin shelf name is not offered, and the shelf itself stays whole for the record.
func TestCoarseningOffersOnlyInBandShelfNames(t *testing.T) {
	observed := []partialentities.NameUses{
		{Name: "software", Uses: 3000},
		{Name: "caching", Uses: 100},
		{Name: "rare-name", Uses: 1},
	}
	got := inBandNames([]string{"software", "caching", "rare-name", "unused"}, observed, refitCorpus, refitLimits)
	if !slices.Equal(got, []string{"caching"}) {
		t.Errorf("in-band names = %v, want only caching", got)
	}
	if kept := keepNames([]string{"software", "caching"}, map[string]bool{"caching": true}); !slices.Equal(kept, []string{"caching"}) {
		t.Errorf("kept = %v, want caching", kept)
	}
}

// TestRetopicCatalogJudgesByShare: a repair resolves onto the shelf, so the shelf catalog judges its
// names the same way the derivation does. The fillers only make the total a realistic corpus.
func TestRetopicCatalogJudgesByShare(t *testing.T) {
	observed := make([]entities.TopicUse, 0, 2+969)
	observed = append(observed,
		entities.TopicUse{Topic: "software", Count: 3000},
		entities.TopicUse{Topic: "caching", Count: 100},
	)
	for i := range 969 {
		observed = append(observed, entities.TopicUse{Topic: fmt.Sprintf("filler-%03d", i), Count: 100})
	}
	c := shelfCatalogOf([]string{"software", "caching"}, observed, refitLimits)
	got := c.candidates("software caching", 5)
	if slices.Contains(got, "software") {
		t.Errorf("the repair offered a catch-all: %v", got)
	}
}
