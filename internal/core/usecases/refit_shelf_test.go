package usecases

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
)

// refitCorpus is the claim total of the refit tests. A topic is judged by its share of it, so the
// fixtures need a corpus of the size a shelf is built for: at 100,000 claims the thin floor is ten claims
// and the catch-all ceiling 2,000. The observation names only the topics a test is about.
const refitCorpus = 100000

// refitLimits are the shipped defaults of vocabulary.shelf.max_share and min_share.
var refitLimits = ShelfLimits{MaxShare: 0.02, MinShare: 0.0001}

// refitFixture is a settled topics vocabulary with the given slots and the observation of one look.
func refitFixture(shelf []string, target int, observed ...partialentities.NameUses) (Vocabulary, *shelfVocab) {
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace: entities.VocabularyTopics,
		Shelf:     shelf,
		Target:    target,
		Absorbed:  map[string]string{},
	}
	uc := Vocabulary{Vocab: store, Clock: shelfClock{}, Shelf: refitLimits}
	return uc, store
}

func healthOf(target int, observed ...partialentities.NameUses) partialentities.VocabularyHealth {
	return partialentities.VocabularyHealth{
		Namespace: string(entities.VocabularyTopics),
		Target:    target,
		Uses:      refitCorpus,
		Observed:  observed,
	}
}

// TestRefitShelfPromotesASubjectThatEarnedASlot: the shelf follows the corpus. A name with the mass of
// a slot takes it from the coldest one — that is the only way a subject the shelf never had can appear
// without someone pressing a button.
func TestRefitShelfPromotesASubjectThatEarnedASlot(t *testing.T) {
	uc, store := refitFixture([]string{"caching", "databases"}, 2,
		partialentities.NameUses{Name: "tp-link-router", Uses: 400},
		partialentities.NameUses{Name: "caching", Uses: 100},
		partialentities.NameUses{Name: "databases", Uses: 20},
	)
	res, err := uc.RefitShelf(healthOf(2,
		partialentities.NameUses{Name: "tp-link-router", Uses: 400},
		partialentities.NameUses{Name: "caching", Uses: 100},
		partialentities.NameUses{Name: "databases", Uses: 20},
	), 2)
	if err != nil {
		t.Fatalf("refit: %v", err)
	}
	if !slices.Equal(res.Promoted, []string{"tp-link-router"}) {
		t.Errorf("promoted = %v, want the hot subject", res.Promoted)
	}
	if !slices.Equal(res.Evicted, []string{"databases"}) {
		t.Errorf("evicted = %v, want the coldest slot", res.Evicted)
	}
	if got := store.v[entities.VocabularyTopics].Shelf; !slices.Equal(got, []string{"caching", "tp-link-router"}) {
		t.Errorf("shelf = %v", got)
	}
}

// TestRefitShelfUsesAFreeSlot: while the shelf is below its target there is nothing to evict.
func TestRefitShelfUsesAFreeSlot(t *testing.T) {
	uc, store := refitFixture([]string{"caching"}, 5,
		partialentities.NameUses{Name: "caching", Uses: 100},
		partialentities.NameUses{Name: "tp-link-router", Uses: 40},
	)
	res, err := uc.RefitShelf(healthOf(5,
		partialentities.NameUses{Name: "caching", Uses: 100},
		partialentities.NameUses{Name: "tp-link-router", Uses: 40},
	), 5)
	if err != nil {
		t.Fatalf("refit: %v", err)
	}
	if len(res.Evicted) != 0 {
		t.Errorf("evicted = %v, want none: the shelf has room", res.Evicted)
	}
	if got := store.v[entities.VocabularyTopics].Shelf; len(got) != 2 {
		t.Errorf("shelf = %v, want the promoted name beside caching", got)
	}
}

// TestRefitShelfDoesNotChurnOnASmallMargin: a name with three uses must not trade places with one that
// has two on every refit — that moves claims, costs a retopic pass and buys nothing.
func TestRefitShelfDoesNotChurnOnASmallMargin(t *testing.T) {
	observed := []partialentities.NameUses{
		{Name: "caching", Uses: 100},
		{Name: "databases", Uses: 20},
		{Name: "misc", Uses: 30},
	}
	uc, store := refitFixture([]string{"caching", "databases"}, 2, observed...)
	writes := store.writes
	res, err := uc.RefitShelf(healthOf(2, observed...), 2)
	if err != nil {
		t.Fatalf("refit: %v", err)
	}
	if len(res.Promoted) != 0 || len(res.Evicted) != 0 {
		t.Errorf("promoted/evicted = %v/%v, want nothing below the margin", res.Promoted, res.Evicted)
	}
	if store.writes != writes {
		t.Error("an unchanged refit rewrote the vocabulary file")
	}
}

// TestRefitShelfKeepsWhatTheVaultDecided: a refit moves the shelf, nothing else. The alias table, the
// judged names and the re-derived ones are decisions, not derived values.
func TestRefitShelfKeepsWhatTheVaultDecided(t *testing.T) {
	uc, store := refitFixture([]string{"caching", "databases"}, 2,
		partialentities.NameUses{Name: "caching", Uses: 100},
		partialentities.NameUses{Name: "databases", Uses: 20},
		partialentities.NameUses{Name: "tp-link-router", Uses: 200},
	)
	v := store.v[entities.VocabularyTopics]
	v.Absorbed = map[string]string{"cache-invalidation": "caching"}
	v.Decided = []string{"moral-psychology"}
	v.Relabelled = []string{"tp-link-router"}
	v.Canonical = []string{"caching", "databases", "tp-link-router"}
	store.v[entities.VocabularyTopics] = v

	if _, err := uc.RefitShelf(healthOf(2,
		partialentities.NameUses{Name: "tp-link-router", Uses: 200},
		partialentities.NameUses{Name: "caching", Uses: 100},
		partialentities.NameUses{Name: "databases", Uses: 20},
	), 2); err != nil {
		t.Fatalf("refit: %v", err)
	}
	got := store.v[entities.VocabularyTopics]
	if got.Absorbed["cache-invalidation"] != "caching" {
		t.Errorf("the alias table was dropped: %v", got.Absorbed)
	}
	if !slices.Equal(got.Decided, []string{"moral-psychology"}) || !slices.Equal(got.Relabelled, []string{"tp-link-router"}) {
		t.Errorf("a decision was lost: decided %v relabelled %v", got.Decided, got.Relabelled)
	}
	if len(got.Canonical) != 3 {
		t.Errorf("canonical = %v, want the curation record untouched", got.Canonical)
	}
}

// TestRefitShelfNeverPromotesASingleton: one claim is not a subject, and a shelf of one-offs is the
// catalog the shelf exists to prevent.
func TestRefitShelfNeverPromotesASingleton(t *testing.T) {
	uc, store := refitFixture([]string{"caching"}, 5)
	writes := store.writes
	res, err := uc.RefitShelf(healthOf(5,
		partialentities.NameUses{Name: "caching", Uses: 100},
		partialentities.NameUses{Name: "one-off", Uses: 1},
	), 5)
	if err != nil {
		t.Fatalf("refit: %v", err)
	}
	if len(res.Promoted) != 0 || store.writes != writes {
		t.Errorf("a singleton was promoted: %v", res.Promoted)
	}
}

// retopicAtoms is the claim corpus the retopic pass reads: real pages, so the pass has to respect the
// offset it asks for.
type retopicAtoms struct {
	interfaces.AtomRepository
	byTopic map[string][]entities.Atom
	saved   int
	reads   int
}

func newRetopicAtoms(catalog map[string]int) *retopicAtoms {
	a := &retopicAtoms{byTopic: map[string][]entities.Atom{}}
	for topic, n := range catalog {
		for i := range n {
			a.byTopic[topic] = append(a.byTopic[topic], entities.Atom{
				ID: ulid.Make(), Topic: topic, Claim: fmt.Sprintf("%s claim %d", topic, i+1),
				Body: "a body that says what the claim is about",
			})
		}
	}
	return a
}

func (a *retopicAtoms) ListClaimTopics(context.Context) ([]entities.TopicUse, error) {
	out := make([]entities.TopicUse, 0, len(a.byTopic))
	for topic, list := range a.byTopic {
		if len(list) > 0 {
			out = append(out, entities.TopicUse{Topic: topic, Count: len(list)})
		}
	}
	return out, nil
}

func (a *retopicAtoms) ListAtoms(_ context.Context, topic string, limit, offset int) ([]entities.Atom, error) {
	a.reads++
	list := a.byTopic[topic]
	if offset >= len(list) {
		return nil, nil
	}
	list = list[offset:]
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return append([]entities.Atom(nil), list...), nil
}

func (a *retopicAtoms) GetAtom(_ context.Context, id ulid.ULID) (entities.Atom, error) {
	for _, list := range a.byTopic {
		for _, at := range list {
			if at.ID == id {
				return at, nil
			}
		}
	}
	return entities.Atom{}, fmt.Errorf("atom %s not found", id)
}

func (a *retopicAtoms) SaveAtom(_ context.Context, at entities.Atom) error {
	a.saved++
	// The old topic loses the claim, the new one gains it.
	for topic, list := range a.byTopic {
		for i, other := range list {
			if other.ID == at.ID {
				a.byTopic[topic] = append(append([]entities.Atom{}, list[:i]...), list[i+1:]...)
				break
			}
		}
	}
	a.byTopic[at.Topic] = append(a.byTopic[at.Topic], at)
	return nil
}

// TestRebranchWorksTheClaimsTheShelfCouldNotTake: the retopic pass is the second half of the reduction.
// It takes a name outside the shelf, re-derives its claims from their own text, resolves every answer
// onto the shelf — and records the name, so it is not a question again.
func TestRebranchWorksTheClaimsTheShelfCouldNotTake(t *testing.T) {
	ctx := context.Background()
	atoms := newRetopicAtoms(map[string]int{
		"caching":        10,
		"tp-link-router": 2,
	})
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace: entities.VocabularyTopics,
		Shelf:     []string{"caching", "networking"},
		Target:    2,
		Absorbed:  map[string]string{},
	}
	gw := &sequenceGateway{answers: []string{
		`{"claims":[{"n":1,"topic":"caching"},{"n":2,"topic":"caching"}]}`,
	}}
	uc := Claims{
		Atoms: atoms, Vocab: store, Gateway: gw, Model: "test-model", Clock: shelfClock{},
		Apply: true, Parallel: 1,
	}
	res, err := uc.Rebranch(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("retopic: %v", err)
	}
	if res.Names != 1 || res.Changed != 2 {
		t.Errorf("names/changed = %d/%d, want 1/2", res.Names, res.Changed)
	}
	if res.Remaining != 0 {
		t.Errorf("remaining = %d, want the queue empty", res.Remaining)
	}
	if !slices.Contains(store.v[entities.VocabularyTopics].Relabelled, "tp-link-router") {
		t.Errorf("the name was not recorded as re-derived: %v",
			store.v[entities.VocabularyTopics].Relabelled)
	}
	if n := len(atoms.byTopic["tp-link-router"]); n != 0 {
		t.Errorf("%d claim(s) still carry the fine name", n)
	}
	if n := len(atoms.byTopic["caching"]); n != 12 {
		t.Errorf("caching has %d claims, want 12 (10 + the two re-derived)", n)
	}
	// A second pass has nothing left to ask: the recorded name is the memory.
	res2, err := uc.Rebranch(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if res2.Names != 0 || res2.Remaining != 0 {
		t.Errorf("the second pass asked again: names %d remaining %d", res2.Names, res2.Remaining)
	}
}

// TestRebranchStopsOnItsBudget: a pass that ran out of time hands the name back (its claims were not
// attempted), so the next pass continues instead of pretending the work is done.
func TestRebranchStopsOnItsBudget(t *testing.T) {
	ctx := context.Background()
	atoms := newRetopicAtoms(map[string]int{
		"caching":        10,
		"tp-link-router": 2,
	})
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace: entities.VocabularyTopics,
		Shelf:     []string{"caching"},
		Absorbed:  map[string]string{},
	}
	gw := &sequenceGateway{answers: []string{`{"claims":[]}`}}
	uc := Claims{
		Atoms: atoms, Vocab: store, Gateway: gw, Model: "test-model",
		Clock: &advancingClock{step: time.Minute},
		Apply: true, Budget: time.Minute, Parallel: 1,
	}
	res, err := uc.Rebranch(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("retopic: %v", err)
	}
	if res.Names != 0 || res.Remaining != 1 {
		t.Errorf("names/remaining = %d/%d, want 0/1 (the budget is spent before the name)", res.Names, res.Remaining)
	}
	if len(store.v[entities.VocabularyTopics].Relabelled) != 0 {
		t.Error("a name that was never asked about was recorded as re-derived")
	}
}

// TestRefitShelfReopensAnEvictedName: an evicted name is WORK again. Its old verdicts were made against
// a shelf that no longer exists, so keeping them would park its claims off the shelf for good — the one
// way this mechanism could silently stop reducing.
func TestRefitShelfReopensAnEvictedName(t *testing.T) {
	uc, store := refitFixture([]string{"caching", "databases"}, 2,
		partialentities.NameUses{Name: "caching", Uses: 100},
		partialentities.NameUses{Name: "databases", Uses: 1},
		partialentities.NameUses{Name: "tp-link-router", Uses: 200},
	)
	v := store.v[entities.VocabularyTopics]
	v.Decided = []string{"databases", "moral-psychology"}
	v.Relabelled = []string{"databases"}
	store.v[entities.VocabularyTopics] = v

	if _, err := uc.RefitShelf(healthOf(2,
		partialentities.NameUses{Name: "tp-link-router", Uses: 200},
		partialentities.NameUses{Name: "caching", Uses: 100},
		partialentities.NameUses{Name: "databases", Uses: 1},
	), 2); err != nil {
		t.Fatalf("refit: %v", err)
	}
	got := store.v[entities.VocabularyTopics]
	if slices.Contains(got.Decided, "databases") || slices.Contains(got.Relabelled, "databases") {
		t.Errorf("the evicted name kept its verdicts: decided %v relabelled %v", got.Decided, got.Relabelled)
	}
	if !slices.Contains(got.Decided, "moral-psychology") {
		t.Errorf("an unrelated verdict was dropped: %v", got.Decided)
	}
}

// TestTailStateWithoutAShelfAnswersNoWork: no target, no tail. A vault that never built a shelf is not a
// vault whose every claim is off the shelf — that would queue a repair pass that can never finish.
func TestTailStateWithoutAShelfAnswersNoWork(t *testing.T) {
	observed := []partialentities.NameUses{
		{Name: "caching", Uses: 10},
		{Name: "tp-link-router", Uses: 2},
	}
	st := tailStateOf(observed, entities.Vocabulary{Namespace: entities.VocabularyTopics})
	if st.remaining != 2 || st.retopic != 0 || st.offShelf != 0 {
		t.Errorf("tail = %+v, want the catalog as remaining and nothing off a shelf that does not exist", st)
	}
}

// TestRebranchBatchesAcrossNames is the throughput guard: the question is the CLAIM's text, not the
// name it was filed under, so one call takes claims from several names. Asking one name's claims at a
// time spent a call per name — on the measured vault that was 45,000 calls for 341,000 claims.
func TestRebranchBatchesAcrossNames(t *testing.T) {
	ctx := context.Background()
	atoms := newRetopicAtoms(map[string]int{
		"caching": 10,
		"n-one":   2,
		"n-two":   2,
		"n-three": 1,
	})
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace: entities.VocabularyTopics,
		Shelf:     []string{"caching"},
		Absorbed:  map[string]string{},
	}
	gw := &sequenceGateway{answers: []string{
		`{"claims":[{"n":1,"topic":"caching"},{"n":2,"topic":"caching"},{"n":3,"topic":"caching"},{"n":4,"topic":"caching"},{"n":5,"topic":"caching"}]}`,
	}}
	uc := Claims{
		Atoms: atoms, Vocab: store, Gateway: gw, Model: "test-model", Clock: shelfClock{},
		Apply: true, Parallel: 1,
	}
	res, err := uc.Rebranch(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("retopic: %v", err)
	}
	if gw.calls != 1 {
		t.Errorf("model calls = %d, want 1 for the three names' five claims", gw.calls)
	}
	if res.Names != 3 || res.Examined != 5 || res.Remaining != 0 {
		t.Errorf("names/examined/remaining = %d/%d/%d, want 3/5/0", res.Names, res.Examined, res.Remaining)
	}
	if len(store.v[entities.VocabularyTopics].Relabelled) != 3 {
		t.Errorf("relabelled = %v, want the three names", store.v[entities.VocabularyTopics].Relabelled)
	}
}

// TestCoarsenReopensTheTailUnderANewRule: a verdict made under the old "may be left out" rule has to be
// asked again, or the tail it settled is closed for good. A verdict about a SHELF name is untouched:
// the rule is about filing, not about the shelf.
func TestCoarsenReopensTheTailUnderANewRule(t *testing.T) {
	ctx := context.Background()
	catalog := map[string]int{"shelf-a": 50, "shelf-b": 40, "shelf-c": 30}
	for i := 1; i <= 4; i++ {
		catalog[fmt.Sprintf("tail-%03d", i)] = 1
	}
	atoms := newCoarsenAtoms(catalog)
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace:   entities.VocabularyTopics,
		Shelf:       []string{"shelf-a", "shelf-b", "shelf-c"},
		Target:      3,
		ShelfBuilt:  shelfBuildVersion,
		Absorbed:    map[string]string{},
		BroadTokens: []string{"tail", "shelf"},
		Decided:     []string{"shelf-a", "tail-001", "tail-002"},
	}
	gw := &sequenceGateway{answers: []string{`{"mapping":{}}`}}

	res, err := coarsenFixture(atoms, store, gw, 3).Coarsen(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("coarsen: %v", err)
	}
	if gw.calls == 0 {
		t.Error("the settled tail was not asked again")
	}
	if res.Remaining != 4 {
		t.Errorf("remaining = %d, want the four tail names open again", res.Remaining)
	}
	got := store.v[entities.VocabularyTopics]
	if got.Settle != coarsenSettleVersion {
		t.Errorf("settle = %d, want %d", got.Settle, coarsenSettleVersion)
	}
	if !slices.Contains(got.Decided, "shelf-a") {
		t.Errorf("a verdict about a shelf name was dropped: %v", got.Decided)
	}
	for _, name := range []string{"tail-001", "tail-002"} {
		if slices.Contains(got.Decided, name) {
			t.Errorf("%q kept its old verdict: %v", name, got.Decided)
		}
	}
}

// TestRefitShelfEvictsACatchAllEvenWithRoom: a topic that carries more than the catch-all share of the
// claims distinguishes nothing, and it gives its slot up while the shelf still has room. Its claims are
// re-derived by the retopic pass, which the eviction queues.
func TestRefitShelfEvictsACatchAllEvenWithRoom(t *testing.T) {
	uc, store := refitFixture([]string{"caching", "software"}, 5,
		partialentities.NameUses{Name: "software", Uses: 3000},
		partialentities.NameUses{Name: "caching", Uses: 100},
	)
	res, err := uc.RefitShelf(healthOf(5,
		partialentities.NameUses{Name: "software", Uses: 3000},
		partialentities.NameUses{Name: "caching", Uses: 100},
	), 5)
	if err != nil {
		t.Fatalf("refit: %v", err)
	}
	if !slices.Equal(res.Evicted, []string{"software"}) {
		t.Errorf("evicted = %v, want the catch-all", res.Evicted)
	}
	if got := store.v[entities.VocabularyTopics].Shelf; !slices.Equal(got, []string{"caching"}) {
		t.Errorf("shelf = %v, want only caching", got)
	}
}

// TestRefitShelfNeverPromotesACatchAll: the most used name is not automatically a subject. A candidate
// above the catch-all share is skipped, even with free slots, so a generic word cannot earn a slot by
// mass alone.
func TestRefitShelfNeverPromotesACatchAll(t *testing.T) {
	uc, store := refitFixture([]string{"caching"}, 5,
		partialentities.NameUses{Name: "caching", Uses: 100},
	)
	res, err := uc.RefitShelf(healthOf(5,
		partialentities.NameUses{Name: "software", Uses: 3000},
		partialentities.NameUses{Name: "caching", Uses: 100},
	), 5)
	if err != nil {
		t.Fatalf("refit: %v", err)
	}
	if len(res.Promoted) != 0 {
		t.Errorf("promoted = %v, want no catch-all", res.Promoted)
	}
	if got := store.v[entities.VocabularyTopics].Shelf; !slices.Equal(got, []string{"caching"}) {
		t.Errorf("shelf = %v", got)
	}
}

// TestRefitShelfEvictsAThinShelfName: a slot held by a name below the thin share is a slot nobody
// uses. It gives up its place even while the shelf has room, and it is never kept by mass alone.
func TestRefitShelfEvictsAThinShelfName(t *testing.T) {
	uc, store := refitFixture([]string{"caching", "rare-name"}, 5,
		partialentities.NameUses{Name: "caching", Uses: 100},
		partialentities.NameUses{Name: "rare-name", Uses: 5},
	)
	res, err := uc.RefitShelf(healthOf(5,
		partialentities.NameUses{Name: "caching", Uses: 100},
		partialentities.NameUses{Name: "rare-name", Uses: 5},
	), 5)
	if err != nil {
		t.Fatalf("refit: %v", err)
	}
	if !slices.Equal(res.Evicted, []string{"rare-name"}) {
		t.Errorf("evicted = %v, want the thin name", res.Evicted)
	}
	if got := store.v[entities.VocabularyTopics].Shelf; !slices.Equal(got, []string{"caching"}) {
		t.Errorf("shelf = %v", got)
	}
}

// TestRefitShelfLimitsAreOffWhenZero: a zero limit is off, so an unconfigured use case (a test or a
// tool without the knobs) never judges a topic by share.
func TestRefitShelfLimitsAreOffWhenZero(t *testing.T) {
	uc, _ := refitFixture([]string{"caching"}, 5,
		partialentities.NameUses{Name: "caching", Uses: 100},
	)
	uc.Shelf = ShelfLimits{}
	res, err := uc.RefitShelf(healthOf(5,
		partialentities.NameUses{Name: "software", Uses: 3000},
		partialentities.NameUses{Name: "caching", Uses: 100},
	), 5)
	if err != nil {
		t.Fatalf("refit: %v", err)
	}
	if !slices.Equal(res.Promoted, []string{"software"}) {
		t.Errorf("promoted = %v, want the name with no share rule", res.Promoted)
	}
}
