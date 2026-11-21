package app

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/config"
	"github.com/TheFranconianCoder/thought-keeper/internal/registry"
)

// coarsenTickApp is a vault whose shelf is written and whose tail is not: two names sit outside it,
// and the catalog knows nothing newer. That is exactly the state of a vault that predates the shelf,
// and the state in which the app has to work without a press.
func coarsenTickApp(t *testing.T) (*App, *shelfAppVocab) {
	t.Helper()
	a := &App{
		log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		quit:            make(chan struct{}),
		vocabularyQueue: make(chan runJob, 4),
		maint:           map[ulid.ULID]maintJob{},
	}
	cfg := config.Defaults()
	cfg.OpenAI.BlockedWindows = nil // the fixture is about the tail, not the hour
	cfg.Vocabulary.Topics.MaxNames = 3
	a.setConfig(cfg)
	store := &shelfAppVocab{v: map[entities.VocabularyNamespace]entities.Vocabulary{}}
	store.v[entities.VocabularyTopics] = entities.Vocabulary{
		Namespace:   entities.VocabularyTopics,
		Canonical:   []string{"caching", "compilers", "databases"},
		Shelf:       []string{"caching", "compilers", "databases"},
		Target:      3,
		Absorbed:    map[string]string{},
		BroadTokens: []string{"generic"},
	}
	atoms := &shelfAppAtoms{topics: []entities.TopicUse{
		{Topic: "caching", Count: 40},
		{Topic: "compilers", Count: 9},
		{Topic: "databases", Count: 7},
		{Topic: "tp-link-router", Count: 1},
		{Topic: "moral-psychology", Count: 1},
	}}
	uc := usecases.Vocabulary{Vocab: store, Atoms: atoms, Clock: shelfAppClock{}}
	a.depsPtr.Store(&registry.Deps{
		VocabularyCurate: uc,
		AgentAvailable:   true,
		StartRun:         usecases.Runs{Store: &fakeRuns{}, Clock: shelfAppClock{}},
		RebranchClaims: usecases.Claims{
			Atoms: atoms, Gateway: &quietGateway{}, Model: "test-model",
		},
	})
	return a, store
}

// TestVocabularyTickCoarsensByItself is the whole point of the change: a tail outside the shelf is
// housekeeping, not a button. The reconcile tick asks for a pass on its own, with the configured
// budget, and takes the latch the curation also reads.
func TestVocabularyTickCoarsensByItself(t *testing.T) {
	a, _ := coarsenTickApp(t)
	a.requestVocabularyIfDue("test")

	select {
	case job := <-a.vocabularyQueue:
		if !job.coarsen {
			t.Fatal("the tick queued a curation, want a coarsening")
		}
		if job.namespace != entities.VocabularyTopics {
			t.Errorf("namespace = %q, want topics", job.namespace)
		}
		want := config.Defaults().Vocabulary.Coarsen.CoarsenDuration()
		if job.budget != want || want <= 0 {
			t.Errorf("budget = %v, want %v (the automatic pass must hand the gate back)", job.budget, want)
		}
	default:
		t.Fatal("nothing was queued: the coarsening has to run by itself")
	}
	if !a.vocabActive {
		t.Error("the pass did not take the one-at-a-time latch")
	}
}

// TestVocabularyTickRetopicsWhenCoarseningIsOff: the knobs are real switches, and they are separate.
// With the bulk pass off the reduction does not stop — the per-claim pass takes it, which is slower and
// more precise. Only with BOTH off does the tick fall back to the drift curation.
func TestVocabularyTickRetopicsWhenCoarseningIsOff(t *testing.T) {
	a, _ := coarsenTickApp(t)
	cfg := a.conf()
	cfg.Vocabulary.Coarsen.Automatic = false
	a.setConfig(cfg)
	a.requestVocabularyIfDue("test")

	select {
	case job := <-a.vocabularyQueue:
		if job.coarsen {
			t.Fatal("the coarsening ran although the knob is off")
		}
		if !job.rebranch {
			t.Fatal("the reduction stopped when the bulk pass was switched off")
		}
	default:
		t.Fatal("nothing was queued")
	}
}

// TestVocabularyTickCuratesWhenTheReductionIsOff: with both reduction stages off, the tick is what it
// was before them — the drift curation, for names that are new.
func TestVocabularyTickCuratesWhenTheReductionIsOff(t *testing.T) {
	a, _ := coarsenTickApp(t)
	cfg := a.conf()
	cfg.Vocabulary.Coarsen.Automatic = false
	cfg.Vocabulary.Retopic.Automatic = false
	a.setConfig(cfg)
	a.requestVocabularyIfDue("test")

	select {
	case job := <-a.vocabularyQueue:
		if job.coarsen || job.rebranch {
			t.Fatal("a reduction pass ran although both knobs are off")
		}
	default:
		t.Fatal("nothing was queued: the two new names still need a curation")
	}
}

// TestCoarsenIsBoundedByProgress: an automatic loop that stops must be able to say why, and it must
// stop. Two looks that leave the tail where they found it end the automatic pass until the tail
// moves — the same rule the cluster step follows.
func TestCoarsenIsBoundedByProgress(t *testing.T) {
	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if !a.coarsenDue(100) {
		t.Fatal("the first look must ask for a pass")
	}
	if !a.coarsenDue(100) {
		t.Error("one pass has not run yet; the second look may ask")
	}
	if a.coarsenDue(100) {
		t.Error("two looks that did not move the tail must stop the automatic pass")
	}
	if !a.coarsenDue(90) {
		t.Error("a tail that moved must earn the next pass again")
	}
	if a.coarsenDue(0) {
		t.Error("a settled tail is not a reason to ask")
	}
	if a.coarsenLeft != 0 || a.coarsenStalled != 0 {
		t.Errorf("a settled tail must clear the state, got left=%d stalled=%d",
			a.coarsenLeft, a.coarsenStalled)
	}
}

// TestTailDueFinishesTheSmallNamespaceFirst: forty source tags must not wait behind forty thousand
// topics, so the smallest tail is the one a pass takes.
func TestTailDueFinishesTheSmallNamespaceFirst(t *testing.T) {
	report := []partialentities.VocabularyHealth{
		{Namespace: string(entities.VocabularyTopics), Remaining: 45310},
		{Namespace: string(entities.VocabularySourceTags), Remaining: 41},
		{Namespace: string(entities.VocabularyWikiTags), Remaining: 0},
	}
	ns, remaining := tailDue(report)
	if ns != entities.VocabularySourceTags || remaining != 41 {
		t.Errorf("tailDue = %q/%d, want the smallest tail (source-tags/41)", ns, remaining)
	}
	if ns, n := tailDue([]partialentities.VocabularyHealth{{Remaining: 0}}); ns != "" || n != 0 {
		t.Errorf("a settled vault reported %q/%d", ns, n)
	}
}

// settledTickApp is a vault whose bulk pass finished its tail (every name outside the shelf is judged)
// and whose claims are still off the shelf — the state the retopic stage exists for.
func settledTickApp(t *testing.T) (*App, *shelfAppVocab) {
	t.Helper()
	a, store := coarsenTickApp(t)
	v := store.v[entities.VocabularyTopics]
	v.Decided = []string{"tp-link-router", "moral-psychology"}
	store.v[entities.VocabularyTopics] = v
	return a, store
}

// quietGateway answers nothing; a scheduling test never runs the pass itself.
type quietGateway struct{}

func (quietGateway) Complete(context.Context, interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	return interfaces.AgentResponse{}, nil
}

// TestVocabularyTickRetopicsWhenTheBulkPassIsDone: the reduction to the shelf has two stages, and the
// second one has to start by itself too. With the names judged and the claims still fine, the tick
// queues the per-claim repair — not a curation, and not nothing.
func TestVocabularyTickRetopicsWhenTheBulkPassIsDone(t *testing.T) {
	a, _ := settledTickApp(t)
	a.requestVocabularyIfDue("test")

	select {
	case job := <-a.vocabularyQueue:
		if !job.rebranch {
			t.Fatal("the tick queued something other than the retopic pass")
		}
		want := config.Defaults().Vocabulary.Retopic.RetopicDuration()
		if job.budget != want || want <= 0 {
			t.Errorf("budget = %v, want %v", job.budget, want)
		}
	default:
		t.Fatal("nothing was queued: the retopic stage has to run by itself")
	}
	if !a.vocabActive {
		t.Error("the pass did not take the one-at-a-time latch")
	}
}

// TestRetopicIsBoundedByProgress: the queue shrinks because a name is recorded as re-derived, so a pass
// that does not move it is broken — and two of those stop the stage with a log line.
func TestRetopicIsBoundedByProgress(t *testing.T) {
	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if !a.retopicDue(50) {
		t.Fatal("the first look must ask for a pass")
	}
	if !a.retopicDue(50) {
		t.Error("one pass has not run yet; the second look may ask")
	}
	if a.retopicDue(50) {
		t.Error("two looks that did not move the queue must stop the stage")
	}
	if !a.retopicDue(40) {
		t.Error("a queue that moved must earn the next pass again")
	}
	if a.retopicDue(0) {
		t.Error("a finished queue is not a reason to ask")
	}
	if a.retopicLeft != 0 || a.retopicStalled != 0 {
		t.Errorf("a finished queue must clear the state, got left=%d stalled=%d", a.retopicLeft, a.retopicStalled)
	}
}

// TestRefitShelvesWaitsForTheInterval: a settled vocabulary re-synthesizes its shelf, but not on every
// tick — the report already knows what it would change, and a daily record write is enough.
func TestRefitShelvesWaitsForTheInterval(t *testing.T) {
	a, store := settledTickApp(t)
	report := []partialentities.VocabularyHealth{{
		Namespace: string(entities.VocabularyTopics), Shelf: 3, Target: 3,
		Observed: []partialentities.NameUses{
			{Name: "tp-link-router", Uses: 40},
			{Name: "caching", Uses: 10},
			{Name: "compilers", Uses: 2},
			{Name: "databases", Uses: 1},
		},
	}}
	// The first look is due; the second one is not, and the file must not move again.
	a.refitShelves(report, "test")
	first := store.v[entities.VocabularyTopics].Shelf
	if len(first) != 3 {
		t.Fatalf("shelf = %v, want three names", first)
	}
	writesAfterFirst := store.writes
	a.refitShelves(report, "test")
	if store.writes != writesAfterFirst {
		t.Error("a second refit inside the interval rewrote the vocabulary")
	}
	if a.shelfRefitLast.IsZero() {
		t.Error("the refit clock was not stamped")
	}
}
