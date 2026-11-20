package usecases

import (
	"context"
	"fmt"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// coarsenAtoms is the claim index a coarsening reads and renames: a topic's claims, and the topics
// that exist right now.
type coarsenAtoms struct {
	interfaces.AtomRepository
	byTopic map[string][]entities.Atom
	renames int
}

func newCoarsenAtoms(catalog map[string]int) *coarsenAtoms {
	a := &coarsenAtoms{byTopic: map[string][]entities.Atom{}}
	for topic, n := range catalog {
		for range n {
			a.byTopic[topic] = append(a.byTopic[topic], entities.Atom{ID: ulid.Make(), Topic: topic})
		}
	}
	return a
}

func (a *coarsenAtoms) ListAtoms(_ context.Context, topic string, limit, _ int) ([]entities.Atom, error) {
	out := append([]entities.Atom(nil), a.byTopic[topic]...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (a *coarsenAtoms) RenameAtomTopics(_ context.Context, topics map[ulid.ULID]string) error {
	a.renames++
	next := make(map[string][]entities.Atom, len(a.byTopic))
	for _, list := range a.byTopic {
		for _, atom := range list {
			if topic, ok := topics[atom.ID]; ok {
				atom.Topic = topic
			}
			next[atom.Topic] = append(next[atom.Topic], atom)
		}
	}
	a.byTopic = next
	return nil
}

func (a *coarsenAtoms) ListClaimTopics(context.Context) ([]entities.TopicUse, error) {
	out := make([]entities.TopicUse, 0, len(a.byTopic))
	for topic, list := range a.byTopic {
		out = append(out, entities.TopicUse{Topic: topic, Count: len(list)})
	}
	return out, nil
}

// coarsenFixture is a topics coarsening over a fake catalog: no files, no model unless a test scripts
// an answer.
func coarsenFixture(atoms *coarsenAtoms, store *shelfVocab, gateway interfaces.AgentGateway, target int) Vocabulary {
	return Vocabulary{
		Atoms: atoms, Vocab: store, Clock: shelfClock{}, Gateway: gateway, Model: "test-model",
		Namespace: entities.VocabularyTopics, Apply: true, Target: target, TopNames: 20,
	}
}

// TestCoarsenKeepsTheDeterministicAliases is the regression guard for the stale write: the shelf was
// written back from the vocabulary read BEFORE the families were merged, which silently dropped every
// alias the deterministic stage had just recorded.
func TestCoarsenKeepsTheDeterministicAliases(t *testing.T) {
	ctx := context.Background()
	atoms := newCoarsenAtoms(map[string]int{
		"caching":            5,
		"databases":          4,
		"cache-invalidation": 3,
		"invalidation-cache": 1, // the same words in another order: one deterministic family
	})
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace:   entities.VocabularyTopics,
		Absorbed:    map[string]string{},
		BroadTokens: []string{"generic"},
	}
	gw := &sequenceGateway{} // nothing is left to ask; a call would fail the test

	res, err := coarsenFixture(atoms, store, gw, 3).Coarsen(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("coarsen: %v", err)
	}
	if res.Families != 1 {
		t.Fatalf("families = %d, want 1", res.Families)
	}
	got := store.v[entities.VocabularyTopics]
	if to := got.Absorbed["invalidation-cache"]; to != "cache-invalidation" {
		t.Errorf("the deterministic alias was dropped: %q -> %q (absorbed %v)",
			"invalidation-cache", to, got.Absorbed)
	}
	if len(got.Shelf) != 3 {
		t.Errorf("shelf = %v, want the 3 most used names", got.Shelf)
	}
	if res.Remaining != 0 {
		t.Errorf("remaining = %d, want 0: every name is on the shelf", res.Remaining)
	}
}

// TestCoarsenReportsWhatItFiled: the number left is what the vault still shows outside the shelf, not
// a count of the batches walked. A model that files two names of a batch must not look like it filed
// the whole batch.
func TestCoarsenReportsWhatItFiled(t *testing.T) {
	ctx := context.Background()
	catalog := map[string]int{"shelf-a": 50, "shelf-b": 40, "shelf-c": 30}
	for i := 1; i <= 507; i++ {
		catalog[fmt.Sprintf("tail-%03d", i)] = 1
	}
	atoms := newCoarsenAtoms(catalog)
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace:   entities.VocabularyTopics,
		Absorbed:    map[string]string{},
		BroadTokens: []string{"tail", "shelf"},
	}
	gw := &sequenceGateway{answers: []string{
		`{"mapping":{"tail-001":"shelf-a","tail-002":"shelf-a"}}`,
		`{"mapping":{}}`,
		`{"mapping":{}}`,
		`{"mapping":{}}`,
	}}

	res, err := coarsenFixture(atoms, store, gw, 3).Coarsen(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("coarsen: %v", err)
	}
	if res.Mapped != 2 {
		t.Errorf("mapped = %d, want 2", res.Mapped)
	}
	if want := 507 - 2; res.Remaining != want {
		t.Errorf("remaining = %d, want %d (the batches are not progress)", res.Remaining, want)
	}
	// Three batches in a row that file nothing stop the pass: a fourth ask would be pointless.
	if gw.calls != 4 {
		t.Errorf("model calls = %d, want 4 (one filing, then the stop)", gw.calls)
	}
}

// TestCoarsenStopsWhenNothingCanBeFiled: a tail the shelf cannot honestly cover must not keep a model
// busy for the whole batch budget.
func TestCoarsenStopsWhenNothingCanBeFiled(t *testing.T) {
	ctx := context.Background()
	catalog := map[string]int{"shelf-a": 50, "shelf-b": 40, "shelf-c": 30}
	for i := 1; i <= 400; i++ {
		catalog[fmt.Sprintf("tail-%03d", i)] = 1
	}
	atoms := newCoarsenAtoms(catalog)
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace:   entities.VocabularyTopics,
		Absorbed:    map[string]string{},
		BroadTokens: []string{"tail", "shelf"},
	}
	gw := &sequenceGateway{answers: []string{`{"mapping":{}}`, `{"mapping":{}}`, `{"mapping":{}}`}}

	res, err := coarsenFixture(atoms, store, gw, 3).Coarsen(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("coarsen: %v", err)
	}
	if gw.calls != coarsenEmptyBatches {
		t.Errorf("model calls = %d, want %d", gw.calls, coarsenEmptyBatches)
	}
	if res.Mapped != 0 || res.Remaining != 400 {
		t.Errorf("mapped = %d remaining = %d, want 0/400", res.Mapped, res.Remaining)
	}
}

// TestAskShelfFilesOnlyWhatItAsked: a name the model invented, or one only half of the shelf could
// take, must not rename a claim outside the batch.
func TestAskShelfFilesOnlyWhatItAsked(t *testing.T) {
	gw := &sequenceGateway{answers: []string{
		`{"mapping":{"ghost":"caching","databases":"caching","testing":"not-on-the-shelf"}}`,
	}}
	uc := Vocabulary{Gateway: gw, Model: "test-model"}
	answer, err := uc.askShelf(context.Background(),
		[]string{"caching", "databases"}, []string{"databases"})
	if err != nil {
		t.Fatalf("ask shelf: %v", err)
	}
	if len(answer.mapping) != 1 || answer.mapping["databases"] != "caching" {
		t.Errorf("mapping = %v, want only the asked name filed onto the shelf", answer.mapping)
	}
}
