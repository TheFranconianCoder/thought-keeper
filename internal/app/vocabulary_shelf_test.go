package app

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
	"github.com/TheFranconianCoder/thought-keeper/internal/registry"
)

// shelfAppAtoms is the observed catalog a shelf build reads. Only ListClaimTopics is reached.
type shelfAppAtoms struct {
	interfaces.AtomRepository
	topics []entities.TopicUse
	reads  int
}

func (s *shelfAppAtoms) ListClaimTopics(context.Context) ([]entities.TopicUse, error) {
	s.reads++
	return s.topics, nil
}

// shelfAppVocab is the vocabulary file of a test: what was written, and how often.
type shelfAppVocab struct {
	v      map[entities.VocabularyNamespace]entities.Vocabulary
	writes int
}

func (s *shelfAppVocab) Read(ns entities.VocabularyNamespace) (entities.Vocabulary, error) {
	if v, ok := s.v[ns]; ok {
		return v, nil
	}
	return entities.Vocabulary{Namespace: ns, Absorbed: map[string]string{}}, nil
}

func (s *shelfAppVocab) Write(v entities.Vocabulary) error {
	s.writes++
	s.v[v.Namespace] = v
	return nil
}

type shelfAppClock struct{}

func (shelfAppClock) Now() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) }

// newShelfApp is the smallest App the vocabulary tick needs: the running config (a target of three
// names), the vocabulary use case over fakes, and no hub.
func newShelfApp(t *testing.T, agentAvailable bool) (*App, *shelfAppVocab, *shelfAppAtoms) {
	t.Helper()
	a := &App{
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		quit: make(chan struct{}),
	}
	cfg := config.Defaults()
	cfg.OpenAI.BlockedWindows = nil // the fixture is about the shelf, not the hour
	cfg.Vocabulary.Topics.MaxNames = 3
	a.setConfig(cfg)
	store := &shelfAppVocab{v: map[entities.VocabularyNamespace]entities.Vocabulary{}}
	// The steady state: the vocabulary already knows every name in use, so the report finds nothing
	// fresh and the tick is about the shelf, not a curation.
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace:   entities.VocabularyTopics,
		Canonical:   []string{"caching", "compilers", "databases", "rare-name"},
		Absorbed:    map[string]string{},
		BroadTokens: []string{"generic"},
	}
	atoms := &shelfAppAtoms{topics: []entities.TopicUse{
		{Topic: "caching", Count: 40},
		{Topic: "compilers", Count: 9},
		{Topic: "databases", Count: 7},
		{Topic: "rare-name", Count: 1},
	}}
	uc := usecases.Vocabulary{Vocab: store, Atoms: atoms, Clock: shelfAppClock{}}
	a.depsPtr.Store(&registry.Deps{VocabularyCurate: uc, AgentAvailable: agentAvailable})
	return a, store, atoms
}

// TestVocabularyTickBuildsTheShelfWithoutAnAgent: the shelf is a migration, not a model call, so a
// vault with no API key still gets its bounded vocabulary.
func TestVocabularyTickBuildsTheShelfWithoutAnAgent(t *testing.T) {
	a, store, atoms := newShelfApp(t, false)
	a.requestVocabularyIfDue("test")

	v, ok := store.v[entities.VocabularyTopics]
	if !ok {
		t.Fatal("no shelf was written")
	}
	if len(v.Shelf) != 3 || v.Target != 3 {
		t.Errorf("shelf = %v target %d, want 3", v.Shelf, v.Target)
	}
	if atoms.reads != 1 {
		t.Errorf("catalog reads = %d, want 1", atoms.reads)
	}
	if a.shelfLast.IsZero() {
		t.Error("the shelf cooldown was not stamped")
	}
}

// TestVocabularyTickLooksOncePerCooldown: a second tick inside the cooldown neither rewrites the shelf
// nor re-reads the catalog — the look, not only the write, is what the clock bounds.
func TestVocabularyTickLooksOncePerCooldown(t *testing.T) {
	a, store, atoms := newShelfApp(t, true)
	a.requestVocabularyIfDue("test")
	writes, reads := store.writes, atoms.reads
	if reads < 2 {
		t.Fatalf("catalog reads = %d, want one for the shelf and one for the report", reads)
	}
	a.requestVocabularyIfDue("test")
	if store.writes != writes {
		t.Errorf("writes = %d, want %d", store.writes, writes)
	}
	if atoms.reads != reads {
		t.Errorf("catalog reads = %d, want %d (the report is on the cooldown)", atoms.reads, reads)
	}
}

// TestVocabularyTickDoesNotRepeatAReportThatFoundNothing: the cooldown is stamped by the look itself,
// so a report with nothing fresh does not run the group-by over every claim on the next tick.
func TestVocabularyTickDoesNotRepeatAReportThatFoundNothing(t *testing.T) {
	a, _, _ := newShelfApp(t, true)
	// A shelf that already fits, so the first tick's only catalog read is the report's.
	store := a.depsPtr.Load().VocabularyCurate.Vocab.(*shelfAppVocab)
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace:   entities.VocabularyTopics,
		Shelf:       []string{"caching", "compilers", "databases"},
		Target:      3,
		Absorbed:    map[string]string{},
		Canonical:   []string{"caching", "compilers", "databases", "rare-name"},
		BroadTokens: []string{"generic"},
	}
	a.requestVocabularyIfDue("test")
	if a.vocabLast.IsZero() {
		t.Fatal("the report cooldown was not stamped")
	}
	atoms := a.depsPtr.Load().VocabularyCurate.Atoms.(*shelfAppAtoms)
	reads := atoms.reads
	a.requestVocabularyIfDue("test")
	if atoms.reads != reads {
		t.Errorf("the report ran again (%d -> %d)", reads, atoms.reads)
	}
}
