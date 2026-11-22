package usecases

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// shelfClock is a clock that never moves; a shelf write only needs an instant.
type shelfClock struct{}

func (shelfClock) Now() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) }

// shelfVocab is the vocabulary file of a test: what was read back, and how often it was written.
type shelfVocab struct {
	v      map[entities.VocabularyNamespace]entities.Vocabulary
	writes int
}

func newShelfVocab() *shelfVocab {
	return &shelfVocab{v: map[entities.VocabularyNamespace]entities.Vocabulary{}}
}

func (s *shelfVocab) Read(ns entities.VocabularyNamespace) (entities.Vocabulary, error) {
	if v, ok := s.v[ns]; ok {
		return v, nil
	}
	return entities.Vocabulary{Namespace: ns, Absorbed: map[string]string{}}, nil
}

func (s *shelfVocab) Write(v entities.Vocabulary) error {
	s.writes++
	s.v[v.Namespace] = v
	return nil
}

// shelfTopics is the observed catalog a shelf build reads. Only ListClaimTopics is used; the rest of
// the repository is never reached, so the embedded nil interface is honest.
type shelfTopics struct {
	interfaces.AtomRepository
	topics []entities.TopicUse
	reads  int
}

func (s *shelfTopics) ListClaimTopics(context.Context) ([]entities.TopicUse, error) {
	s.reads++
	return s.topics, nil
}

func topicsTarget(ns entities.VocabularyNamespace) int {
	if ns == entities.VocabularyTopics {
		return 3
	}
	return 200
}

// TestEnsureShelfBuildsTheMostUsedNames: a catalog that runs past its target gets a shelf of the most
// used names, written once, with no model and no gate.
func TestEnsureShelfBuildsTheMostUsedNames(t *testing.T) {
	store := newShelfVocab()
	atoms := &shelfTopics{topics: []entities.TopicUse{
		{Topic: "caching", Count: 40},
		{Topic: "compilers", Count: 9},
		{Topic: "databases", Count: 7},
		{Topic: "rare-name", Count: 1},
	}}
	uc := Vocabulary{Vocab: store, Atoms: atoms, Clock: shelfClock{}}

	res, err := uc.EnsureShelf(context.Background(), topicsTarget)
	if err != nil {
		t.Fatalf("ensure shelf: %v", err)
	}
	if len(res.Built) != 1 || res.Built[0] != "topics" {
		t.Fatalf("built = %v, want topics", res.Built)
	}
	v := store.v[entities.VocabularyTopics]
	if len(v.Shelf) != 3 {
		t.Fatalf("shelf = %v, want 3 names", v.Shelf)
	}
	if v.Shelf[0] != "caching" || v.Target != 3 {
		t.Errorf("shelf = %v target %d, want the most used first and target 3", v.Shelf, v.Target)
	}
	if store.writes != 1 {
		t.Errorf("writes = %d, want 1", store.writes)
	}
}

// TestEnsureShelfSortsTheCatalogByUses is the regression for the measured vault: the index answers in
// alphabetical order, and a build that sliced that order produced a shelf of the alphabetically first
// names — names that covered 38 % of the claims while the most used ones covered 82 %. The shelf must
// be the most USED names, whatever order the catalog arrives in.
func TestEnsureShelfSortsTheCatalogByUses(t *testing.T) {
	store := newShelfVocab()
	atoms := &shelfTopics{topics: []entities.TopicUse{
		{Topic: "aardvark", Count: 1},
		{Topic: "xenon", Count: 20},
		{Topic: "yak", Count: 50},
		{Topic: "zebra", Count: 90},
	}}
	uc := Vocabulary{Vocab: store, Atoms: atoms, Clock: shelfClock{}}

	if _, err := uc.EnsureShelf(context.Background(), topicsTarget); err != nil {
		t.Fatalf("ensure shelf: %v", err)
	}
	got := store.v[entities.VocabularyTopics].Shelf
	want := []string{"zebra", "yak", "xenon"}
	if !slices.Equal(got, want) {
		t.Errorf("shelf = %v, want the most used names %v", got, want)
	}
}

// TestEnsureShelfRebuildsAShelfFromTheOldRule: a shelf without `shelf_built:` was built from an
// unsorted catalog. It cannot be repaired by a refit — it never held a real target — so it is rebuilt
// once from the catalog as it is now. The verdicts made against the old names are dropped with it, or
// the tail they closed would stay closed against a shelf it was never asked about.
func TestEnsureShelfRebuildsAShelfFromTheOldRule(t *testing.T) {
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace:  entities.VocabularyTopics,
		Shelf:      []string{"aardvark", "abacus"},
		Target:     2,
		ShelfBuilt: 0,
		Decided:    []string{"tail-001"},
		Relabelled: []string{"tail-002"},
		Absorbed:   map[string]string{"cache-strategies": "caching"},
	}
	atoms := &shelfTopics{topics: []entities.TopicUse{
		{Topic: "aardvark", Count: 1},
		{Topic: "xenon", Count: 20},
		{Topic: "yak", Count: 50},
		{Topic: "zebra", Count: 90},
	}}
	uc := Vocabulary{Vocab: store, Atoms: atoms, Clock: shelfClock{}}

	res, err := uc.EnsureShelf(context.Background(), topicsTarget)
	if err != nil {
		t.Fatalf("ensure shelf: %v", err)
	}
	if !slices.Equal(res.Rebuilt, []string{string(entities.VocabularyTopics)}) {
		t.Fatalf("rebuilt = %v, want topics", res.Rebuilt)
	}
	got := store.v[entities.VocabularyTopics]
	if !slices.Equal(got.Shelf, []string{"zebra", "yak", "xenon"}) {
		t.Errorf("shelf = %v, want the most used names", got.Shelf)
	}
	if got.ShelfBuilt != shelfBuildVersion {
		t.Errorf("shelf_built = %d, want %d", got.ShelfBuilt, shelfBuildVersion)
	}
	if len(got.Decided) != 0 || len(got.Relabelled) != 0 {
		t.Errorf("the old verdicts survived the rebuild: decided=%v relabelled=%v", got.Decided, got.Relabelled)
	}
	if got.Absorbed["cache-strategies"] != "caching" {
		t.Errorf("the alias table was lost: %v", got.Absorbed)
	}
}

// TestEnsureShelfIsIdempotentAndDoesNotObserveTwice: the steady state is one file read. A shelf that
// exists and fits is a record of a decision, so it is not rebuilt, and the catalog is not read again.
func TestEnsureShelfIsIdempotentAndDoesNotObserveTwice(t *testing.T) {
	store := newShelfVocab()
	atoms := &shelfTopics{topics: []entities.TopicUse{
		{Topic: "caching", Count: 40},
		{Topic: "compilers", Count: 9},
		{Topic: "databases", Count: 7},
		{Topic: "rare-name", Count: 1},
	}}
	uc := Vocabulary{Vocab: store, Atoms: atoms, Clock: shelfClock{}}

	if _, err := uc.EnsureShelf(context.Background(), topicsTarget); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	reads := atoms.reads
	res, err := uc.EnsureShelf(context.Background(), topicsTarget)
	if err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if len(res.Built) != 0 || len(res.Trimmed) != 0 {
		t.Errorf("a second lookout wrote again: %+v", res)
	}
	if atoms.reads != reads {
		t.Errorf("the catalog was read again (%d -> %d)", reads, atoms.reads)
	}
	if store.writes != 1 {
		t.Errorf("writes = %d, want 1", store.writes)
	}
}

// TestEnsureShelfTrimsWhenTheTargetShrinks: the knob is a decision the reader can move down, and the
// shelf follows it on the next look.
func TestEnsureShelfTrimsWhenTheTargetShrinks(t *testing.T) {
	store := newShelfVocab()
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace:  entities.VocabularyTopics,
		Shelf:      []string{"caching", "compilers", "databases", "rare-name"},
		Target:     4,
		ShelfBuilt: shelfBuildVersion,
		Absorbed:   map[string]string{},
	}
	atoms := &shelfTopics{topics: []entities.TopicUse{
		{Topic: "caching", Count: 40},
		{Topic: "compilers", Count: 9},
		{Topic: "databases", Count: 7},
		{Topic: "rare-name", Count: 1},
	}}
	uc := Vocabulary{Vocab: store, Atoms: atoms, Clock: shelfClock{}}

	res, err := uc.EnsureShelf(context.Background(), topicsTarget)
	if err != nil {
		t.Fatalf("ensure shelf: %v", err)
	}
	if len(res.Trimmed) != 1 {
		t.Fatalf("trimmed = %v, want topics", res.Trimmed)
	}
	got := store.v[entities.VocabularyTopics]
	if len(got.Shelf) != 3 || got.Target != 3 {
		t.Errorf("shelf = %v target %d, want 3", got.Shelf, got.Target)
	}
	for _, name := range got.Shelf {
		if name == "rare-name" {
			t.Errorf("the least used name survived the trim: %v", got.Shelf)
		}
	}
}

// TestEnsureShelfLeavesACatalogThatFitsAlone: 33 wiki tags against a target of 200 are not a long tail,
// and a shelf built from them would be noise.
func TestEnsureShelfLeavesACatalogThatFitsAlone(t *testing.T) {
	store := newShelfVocab()
	atoms := &shelfTopics{topics: []entities.TopicUse{{Topic: "caching", Count: 3}}}
	uc := Vocabulary{Vocab: store, Atoms: atoms, Clock: shelfClock{}}

	res, err := uc.EnsureShelf(context.Background(), func(entities.VocabularyNamespace) int { return 200 })
	if err != nil {
		t.Fatalf("ensure shelf: %v", err)
	}
	if len(res.Built) != 0 || store.writes != 0 {
		t.Errorf("a catalog that fits was bounded anyway: %+v", res)
	}
}

// TestEnsureShelfIsTopicsOnly: the tag namespaces' write path does not read a shelf, so a shelf there
// would be a record nothing consults.
func TestEnsureShelfIsTopicsOnly(t *testing.T) {
	store := newShelfVocab()
	atoms := &shelfTopics{topics: []entities.TopicUse{{Topic: "caching", Count: 3}}}
	uc := Vocabulary{Vocab: store, Atoms: atoms, Clock: shelfClock{}}

	if _, err := uc.EnsureShelf(context.Background(), func(entities.VocabularyNamespace) int { return 1 }); err != nil {
		t.Fatalf("ensure shelf: %v", err)
	}
	if _, ok := store.v[entities.VocabularySourceTags]; ok {
		t.Error("a source-tags shelf was written")
	}
	if atoms.reads != 1 {
		t.Errorf("catalog reads = %d, want 1 for the topics namespace", atoms.reads)
	}
}
