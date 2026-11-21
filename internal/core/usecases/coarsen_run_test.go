package usecases

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

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
		// One at a time: the scripted gateway answers in call order, and parallelism would make which
		// batch gets which answer a race. TestCoarsenAsksInParallel covers the concurrency itself.
		Parallel: 1,
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
	// The number left is the names nobody has judged: the batch asked 150, filed 2 and settled the
	// other 148. It is NOT 505 — a batch is not a count of the batches walked, but it is a count of
	// the names it took out of the tail.
	if res.Judged != 148 {
		t.Errorf("judged = %d, want 148", res.Judged)
	}
	if want := 507 - 150; res.Remaining != want {
		t.Errorf("remaining = %d, want %d", res.Remaining, want)
	}
	// Three questions in a row that map nothing end the pass: a fourth ask would be pointless.
	if gw.calls != 4 {
		t.Errorf("model calls = %d, want 4 (one filing, then the stop)", gw.calls)
	}
}

// TestCoarsenStopsWhenNothingCanBeFiled: a tail the shelf cannot honestly cover must not keep a model
// busy for the whole batch budget — three answers that map nothing end the pass.
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
	if gw.calls != coarsenFailedAsks {
		t.Errorf("model calls = %d, want %d", gw.calls, coarsenFailedAsks)
	}
	if res.Mapped != 0 || res.Remaining != 400 {
		t.Errorf("mapped = %d remaining = %d, want 0/400", res.Mapped, res.Remaining)
	}
	// An answer that maps nothing is NOT a decision: it must not settle names the model never judged,
	// or one broken completion would poison the whole tail.
	if res.Judged != 0 || len(store.v[entities.VocabularyTopics].Decided) != 0 {
		t.Errorf("an empty answer settled names: judged = %d decided = %v",
			res.Judged, store.v[entities.VocabularyTopics].Decided)
	}
}

// TestCoarsenSettlesADeclinedNameOnce: a name the model passed over is judged and kept, so the NEXT
// pass asks about the rest of the tail instead of the same names again. That is what makes the
// automatic pass converge: without it every pass re-asked its whole tail.
func TestCoarsenSettlesADeclinedNameOnce(t *testing.T) {
	ctx := context.Background()
	catalog := map[string]int{"shelf-a": 50, "shelf-b": 40, "shelf-c": 30}
	for i := 1; i <= 200; i++ {
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
		`{"mapping":{"tail-001":"shelf-a"}}`, // 1 filed, 149 settled
		`{"mapping":{}}`,                     // the second batch of the pass maps nothing and is not a decision
		`{"mapping":{"tail-151":"shelf-a"}}`, // the next pass asks only what is left
	}}
	uc := coarsenFixture(atoms, store, gw, 3)

	first, err := uc.Coarsen(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if first.Judged != 149 {
		t.Fatalf("judged = %d, want 149", first.Judged)
	}
	if want := 200 - 150; first.Remaining != want {
		t.Errorf("remaining = %d, want %d", first.Remaining, want)
	}
	decided := store.v[entities.VocabularyTopics].Decided
	if !slices.Contains(decided, "tail-002") {
		t.Errorf("a declined name was not recorded as judged: %v", decided)
	}

	second, err := uc.Coarsen(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if second.Asked != 50 {
		t.Errorf("the next pass asked %d names, want the 50 that were left", second.Asked)
	}
	if second.Mapped != 1 {
		t.Errorf("mapped = %d, want 1 (tail-151 filed)", second.Mapped)
	}
}

// flakyGateway fails a fixed number of calls before it answers, which is what a call at the timeout
// edge looks like from inside the pass.
type flakyGateway struct {
	failures int
	calls    int
	answer   string
}

func (g *flakyGateway) Complete(context.Context, interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	g.calls++
	if g.calls <= g.failures {
		return interfaces.AgentResponse{}, errors.New("context deadline exceeded")
	}
	return interfaces.AgentResponse{Message: interfaces.AgentMessage{Role: "assistant", Content: g.answer}}, nil
}

// TestCoarsenSurvivesAFailedQuestion: one call at the timeout edge must not throw away the pass. The
// batch stays open (its names are not judged) and the next question is asked.
func TestCoarsenSurvivesAFailedQuestion(t *testing.T) {
	ctx := context.Background()
	catalog := map[string]int{"shelf-a": 50, "shelf-b": 40, "shelf-c": 30}
	for i := 1; i <= 300; i++ {
		catalog[fmt.Sprintf("tail-%03d", i)] = 1
	}
	atoms := newCoarsenAtoms(catalog)
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace:   entities.VocabularyTopics,
		Absorbed:    map[string]string{},
		BroadTokens: []string{"tail", "shelf"},
	}
	gw := &flakyGateway{failures: 1, answer: `{"mapping":{"tail-151":"shelf-a"}}`}

	res, err := coarsenFixture(atoms, store, gw, 3).Coarsen(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("a failed question ended the pass: %v", err)
	}
	if gw.calls != 2 {
		t.Errorf("calls = %d, want 2 (the failed one, then the next batch)", gw.calls)
	}
	if res.Mapped != 1 {
		t.Errorf("mapped = %d, want 1", res.Mapped)
	}
	if res.Judged != 149 {
		t.Errorf("judged = %d, want only the answered batch (149)", res.Judged)
	}
}

// advancingClock moves forward on every read, so a budget can be exercised without waiting.
type advancingClock struct {
	mu   sync.Mutex
	now  time.Time
	step time.Duration
}

func (c *advancingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(c.step)
	return c.now
}

// TestCoarsenYieldsWhenTheBudgetIsSpent: the pass stops ASKING and applies what is in flight, so the
// exclusive gate goes back to the document steps and the app can queue the next pass.
func TestCoarsenYieldsWhenTheBudgetIsSpent(t *testing.T) {
	ctx := context.Background()
	catalog := map[string]int{"shelf-a": 50, "shelf-b": 40, "shelf-c": 30}
	for i := 1; i <= 600; i++ {
		catalog[fmt.Sprintf("tail-%03d", i)] = 1
	}
	atoms := newCoarsenAtoms(catalog)
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace:   entities.VocabularyTopics,
		Absorbed:    map[string]string{},
		BroadTokens: []string{"tail", "shelf"},
	}
	// Every answer files one name, so no batch counts as a failure and only the budget can stop it.
	gw := &sequenceGateway{answers: []string{
		`{"mapping":{"tail-001":"shelf-a"}}`, `{"mapping":{"tail-151":"shelf-a"}}`,
		`{"mapping":{"tail-301":"shelf-a"}}`, `{"mapping":{"tail-451":"shelf-a"}}`,
	}}
	uc := coarsenFixture(atoms, store, gw, 3)
	uc.Clock = &advancingClock{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC), step: time.Minute}
	uc.Budget = 2 * time.Minute
	uc.Parallel = 1

	res, err := uc.Coarsen(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("coarsen: %v", err)
	}
	if gw.calls != 2 {
		t.Errorf("calls = %d, want 2: the budget stops the pass after the questions in flight", gw.calls)
	}
	if res.Asked != 300 {
		t.Errorf("asked = %d, want 300 (the two batches it launched)", res.Asked)
	}
	if res.Remaining >= 600 {
		t.Errorf("remaining = %d: the applied batches must be gone from the tail", res.Remaining)
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

// TestShelfVariantIsLiteral pins the rule that keeps the deterministic pass honest: a variant is the
// shelf name plus a modifier at either end, never a shared subject word.
func TestShelfVariantIsLiteral(t *testing.T) {
	shelf := map[string]bool{
		"sample-size":             true,
		"code-generation":         true,
		"evolutionary-psychology": true,
		"memory-layout":           true,
		"floating-point":          true,
		"ps":                      true,
		"agent-orchestration":     true,
		"reverse-engineering":     true,
		"caching":                 true,
	}
	cases := []struct {
		name string
		want string
	}{
		{"sample-size-insensitivity", "sample-size"},
		{"go-code-generation", "code-generation"},
		{"compiler-code-generation", "code-generation"},
		{"struct-memory-layout", "memory-layout"},
		{"decimal-floating-point-rounding", "floating-point"},
		{"multi-agent-orchestration", "agent-orchestration"},
		{"reverse-engineering-tools", "reverse-engineering"},
		// Not variants: a different subject that happens to share a word.
		{"moral-psychology", ""},
		{"pattern-recognition", ""},
		{"use-case-relationships", ""},
		{"caching-strategies", ""}, // the shelf name is one word: too broad to claim
		{"sample-size", ""},        // the name itself is not a variant
	}
	for _, c := range cases {
		got, ok := shelfVariant(c.name, shelf)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("shelfVariant(%q) = %q,%v want %q", c.name, got, ok, c.want)
		}
	}
}

// TestCoarsenFilesVariantsWithoutTheModel: the clear variants are filed in one deterministic pass, so
// the model only sees what a rule cannot decide.
func TestCoarsenFilesVariantsWithoutTheModel(t *testing.T) {
	ctx := context.Background()
	atoms := newCoarsenAtoms(map[string]int{
		"sample-size":               50,
		"code-generation":           40,
		"caching":                   30,
		"sample-size-insensitivity": 2,
		"go-code-generation":        1,
		"moral-psychology":          1,
	})
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace: entities.VocabularyTopics,
		Absorbed:  map[string]string{},
		// The near-duplicate detector is taken out of the picture: the shared words are broad here, so
		// this test is about the VARIANT rule, not about stage 1.
		BroadTokens: []string{"sample", "size", "code", "generation", "moral", "psychology"},
	}
	gw := &sequenceGateway{answers: []string{`{"mapping":{}}`}}

	res, err := coarsenFixture(atoms, store, gw, 3).Coarsen(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("coarsen: %v", err)
	}
	if res.Mapped != 2 {
		t.Errorf("mapped = %d, want the 2 variants", res.Mapped)
	}
	got := store.v[entities.VocabularyTopics]
	if got.Absorbed["sample-size-insensitivity"] != "sample-size" {
		t.Errorf("variant alias missing: %v", got.Absorbed)
	}
	if got.Absorbed["go-code-generation"] != "code-generation" {
		t.Errorf("prefix variant alias missing: %v", got.Absorbed)
	}
	if _, wrote := got.Absorbed["moral-psychology"]; wrote {
		t.Errorf("a shared subject word was filed as a variant: %v", got.Absorbed)
	}
	if res.Remaining != 1 {
		t.Errorf("remaining = %d, want 1 (moral-psychology for the model)", res.Remaining)
	}
	if gw.calls != 1 {
		t.Errorf("model calls = %d, want 1: only the undecidable name is asked", gw.calls)
	}
}

// parallelGateway answers a fixed JSON, tracks how many calls are in flight at once and waits, so a
// test can see the questions run side by side instead of one after another.
type parallelGateway struct {
	mu       sync.Mutex
	inflight int
	peak     int
	calls    int
	delay    time.Duration
	answer   string
}

func (g *parallelGateway) Complete(context.Context, interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	g.mu.Lock()
	g.inflight++
	g.calls++
	if g.inflight > g.peak {
		g.peak = g.inflight
	}
	g.mu.Unlock()
	time.Sleep(g.delay)
	g.mu.Lock()
	g.inflight--
	g.mu.Unlock()
	return interfaces.AgentResponse{Message: interfaces.AgentMessage{Role: "assistant", Content: g.answer}}, nil
}

// TestCoarsenAsksInParallel: the questions are in flight together; the answers are applied in batch
// order. An answer takes minutes, so this is what makes a press finish in an afternoon.
func TestCoarsenAsksInParallel(t *testing.T) {
	ctx := context.Background()
	catalog := map[string]int{"shelf-a": 50, "shelf-b": 40, "shelf-c": 30}
	for i := 1; i <= 400; i++ {
		catalog[fmt.Sprintf("topic-%03d", i)] = 1
	}
	atoms := newCoarsenAtoms(catalog)
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace:   entities.VocabularyTopics,
		Absorbed:    map[string]string{},
		BroadTokens: []string{"topic", "shelf"},
	}
	gw := &parallelGateway{delay: 120 * time.Millisecond, answer: `{"mapping":{}}`}

	uc := coarsenFixture(atoms, store, gw, 3)
	uc.Parallel = 4
	res, err := uc.Coarsen(ctx, ulid.Make())
	if err != nil {
		t.Fatalf("coarsen: %v", err)
	}
	if gw.calls != 3 {
		t.Errorf("model calls = %d, want 3 batches (150+150+100)", gw.calls)
	}
	if gw.peak < 2 {
		t.Errorf("peak in flight = %d, want the batches asked together", gw.peak)
	}
	if res.Remaining != 400 || res.Mapped != 0 {
		t.Errorf("mapped = %d remaining = %d, want 0/400", res.Mapped, res.Remaining)
	}
}
