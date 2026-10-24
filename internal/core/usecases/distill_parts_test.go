package usecases_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"strings"
	"sync"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// fakeSections records part states in memory.
type fakeSections struct {
	mu sync.Mutex
	m  map[string]entities.PartState
}

func (f *fakeSections) SetPartState(_ context.Context, _ ulid.ULID, s entities.PartState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]entities.PartState{}
	}
	f.m[s.NodeID] = s
	return nil
}

func (f *fakeSections) PartStates(context.Context, ulid.ULID) (map[string]entities.PartState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]entities.PartState{}
	maps.Copy(out, f.m)
	return out, nil
}

func (f *fakeSections) PruneWithoutText(context.Context) (int64, error) { return 0, nil }

func (f *fakeSections) ClearParts(context.Context, ulid.ULID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m = nil
	return nil
}

// TestDistillSkipsOnlyFinishedParts: the work unit is ONE text-bearing node and only a recorded
// `done` is skipped on a resume. A part with no record is derived again even though atoms citing it
// exist — and those atoms go first, so a half-done part is re-derived as a whole instead of
// half-duplicated. "The model stopped calling tools" is not evidence of completion.
func TestDistillSkipsOnlyFinishedParts(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("cd", 32)

	nodes := []entities.SourceNode{
		{NodeID: "0", Depth: 0, Title: "Book"},
		{NodeID: "1", Depth: 1, Title: "First chapter", Text: "chapter one"},
		{NodeID: "1.1", Depth: 2, Title: "First part", Text: "text one"},
		{NodeID: "2", Depth: 1, Title: "Second chapter", Text: "chapter two"},
		{NodeID: "2.1", Depth: 2, Title: "Second part", Text: "text two"},
	}
	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{hash: nodes}}

	// Node 1.1 carries an atom from an earlier run that never recorded it as finished: no record.
	seeded := entities.Atom{
		ID: ulid.Make(), Topic: "eng", Path: "eng/old.md", Claim: "old claim", Body: "b",
		Status: entities.AtomActive, Sources: []entities.AtomSource{{Hash: hash, NodeID: "1.1"}},
	}
	atomRepo := &fakeAtomRepo{saved: []entities.Atom{seeded}}
	atomStore := &fakeAtomStore{m: map[string]entities.Atom{}}
	parts := &fakeSections{}
	logs := &fakeSourceLog{}
	idx := newFakeIndex()
	idx.m[hash] = entities.Source{ID: ulid.Make(), Hash: hash, Title: "Book", Kind: entities.SourceKindPDF, Ext: ".pdf"}

	gw := &nodeIDAwareGateway{}
	az := usecases.DistillSource{
		Index: idx, Trees: trees, Atoms: atomStore, AtomRepo: atomRepo,
		PartRepo: parts, SourceLog: logs, PartsPerRun: 8, Parallel: 2,
		Gateway: gw, Model: "test", MaxIterations: 4, Clock: fixedClock{},
	}
	if _, err := az.Distill(ctx, hash, usecases.NewStepRecorder(ctx, &fakeRunRepo{}, ulid.Make(), nil)); err != nil {
		t.Fatalf("distill: %v", err)
	}

	// Every text-bearing node became its own work item.
	for _, id := range []string{"1", "1.1", "2", "2.1"} {
		if st, ok := parts.m[id]; !ok || st.State != entities.PartDone {
			t.Errorf("part %s: state = %+v, want done", id, st)
		}
	}
	// The unrecorded part was re-derived as a whole: its old atom is gone, not duplicated.
	for _, a := range atomRepo.saved {
		if a.Claim == "old claim" {
			t.Errorf("the unrecorded part was not re-derived first: %+v", a)
		}
	}
	if !logs.contains("redoing an unfinished part") || !logs.contains("part=1.1") {
		t.Errorf("the log does not name the part it re-derived:\n%s", logs.joined())
	}

	// A second run asks for nothing: every part is recorded, so nothing is re-derived.
	gw2 := &nodeIDAwareGateway{}
	az.Gateway = gw2
	if _, err := az.Distill(ctx, hash, usecases.NewStepRecorder(ctx, &fakeRunRepo{}, ulid.Make(), nil)); err != nil {
		t.Fatalf("second distill: %v", err)
	}
	if len(gw2.asked) != 0 {
		t.Errorf("a recorded part was derived again: %v", gw2.asked)
	}
}

// TestDistillMirrorsSourceRelativePartsIntoTheRecord: a document that correctly produced no claim
// still records its finished part in the .meta. The early "all parts are metadata" success path used
// to return before the mirror, leaving a record that knew no part — and the pipeline then re-derived
// the same source for ever because the record could not prove it was complete.
func TestDistillMirrorsSourceRelativePartsIntoTheRecord(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("ef", 32)
	idx := newFakeIndex()
	idx.m[hash] = entities.Source{
		ID: ulid.Make(), Hash: hash, Title: "Links", Kind: entities.SourceKindMD, Ext: ".md",
	}
	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{hash: {
		{NodeID: "0.1", Depth: 1, Title: "Links", Text: "a list of links"},
	}}}
	parts := &fakeSections{m: map[string]entities.PartState{
		"0.1": {NodeID: "0.1", State: entities.PartDone, Reason: "source_relative", UpdatedAt: fixedClock{}.Now()},
	}}
	steps := &fakeSteps{}
	az := usecases.DistillSource{
		Index: idx, Trees: trees, Atoms: &fakeAtomStore{m: map[string]entities.Atom{}},
		AtomRepo: &fakeAtomRepo{}, PartRepo: parts, Steps: steps,
		PartsPerRun: 4, Parallel: 1, Model: "test", MaxIterations: 4, Clock: fixedClock{},
	}
	if _, err := az.Distill(ctx, hash, usecases.NewStepRecorder(ctx, &fakeRunRepo{}, ulid.Make(), nil)); err != nil {
		t.Fatalf("distill: %v", err)
	}
	rec, ok := steps.parts[hash]["0.1"]
	if !ok || rec.State != entities.PartDone || rec.Reason != "source_relative" {
		t.Fatalf("the part record was not mirrored into the .meta: %+v", steps.parts[hash])
	}
}

// TestDistillResumeKeepsThePreviousDerivation: continuing (reset=false) must not clean, so a
// retry after an abort keeps its progress; reset=true replaces the derivation.
func TestDistillResumeKeepsThePreviousDerivation(t *testing.T) {
	ctx := context.Background()
	// The hash is the content address: the index row and the stored object must agree, or the
	// reset's tree build cannot find its bytes.
	body := "# Chapter\ntext\n"
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])

	setup := func() (*fakeAtomRepo, usecases.DocumentRun) {
		nodes := []entities.SourceNode{
			{NodeID: "0", Depth: 0, Title: "Doc"},
			{NodeID: "1", Depth: 1, Title: "Chapter", Text: "text"},
		}
		trees := &fakeTreeStore{m: map[string][]entities.SourceNode{hash: nodes}}
		// The reset run rebuilds the tree INSIDE the run, so the bytes must really be in the
		// store — otherwise the reset cannot work and the test would pass on a swallowed error.
		store := objects.New(t.TempDir())
		if _, err := store.Put(ctx, ".md", strings.NewReader(body)); err != nil {
			t.Fatalf("store the source: %v", err)
		}
		atomStore := &fakeAtomStore{m: map[string]entities.Atom{}}
		atomRepo := &fakeAtomRepo{saved: []entities.Atom{{
			ID: ulid.Make(), Topic: "eng", Path: "eng/old.md", Claim: "old", Body: "b",
			Status: entities.AtomActive, Sources: []entities.AtomSource{{Hash: hash, NodeID: "1.1"}},
		}}}
		idx := newFakeIndex()
		idx.m[hash] = entities.Source{ID: ulid.Make(), Hash: hash, Title: "Doc", Kind: entities.SourceKindMD, Ext: ".md"}
		gw := &fakeGateway{responses: []interfaces.AgentResponse{completeResponse(), completeResponse()}}
		distill := usecases.DistillSource{
			Index: idx, Trees: trees, Atoms: atomStore, AtomRepo: atomRepo,
			Gateway: gw, Model: "test", MaxIterations: 4, PartsPerRun: 4, Clock: fixedClock{},
		}
		run := usecases.DocumentRun{
			// A reset run wipes the tree and builds it again inside the run; the continue path
			// (what this test exercises) never touches it.
			Reset: usecases.ResetSource{
				Index: idx, Trees: trees, Clock: fixedClock{},
				Clean: usecases.CleanSource{
					Atoms: atomRepo, AtomStore: atomStore, Index: idx, Clock: fixedClock{},
				},
			},
			BuildTree: usecases.BuildTree{
				Store: store, Index: idx, Trees: trees,
				Extractor: extract.New(gateways.ExecRunner{}, extract.Config{}), Clock: fixedClock{},
			},
			Distill: distill, Runs: &fakeRunRepo{}, Clock: fixedClock{}, Model: "test",
		}
		return atomRepo, run
	}

	atomRepo, run := setup()
	if _, err := run.Execute(ctx, ulid.Make(), hash, false); err != nil {
		t.Fatalf("continuing run: %v", err)
	}
	if len(atomRepo.saved) != 1 {
		t.Errorf("continuing removed the previous derivation: %d atom(s) left", len(atomRepo.saved))
	}

	atomRepo, run = setup()
	// A reset run whose model writes nothing FAILS (a document with parts always has claims) — the
	// point here is what the reset did to the previous derivation, not the model's answer.
	if _, err := run.Execute(ctx, ulid.Make(), hash, true); err == nil {
		t.Log("reset run succeeded")
	}
	if len(atomRepo.saved) != 0 {
		t.Errorf("an explicit reset must replace the derivation, %d atom(s) left", len(atomRepo.saved))
	}
}

// TestResetClearsTheSourcesLog: a reset throws the whole derivation away, and the log of that
// derivation goes with it. Otherwise the log of the NEXT derivation starts with the story of a
// document that no longer exists — and "what did this run do?" cannot be answered from it.
func TestResetClearsTheSourcesLog(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("ab", 32)
	idx := newFakeIndex()
	idx.m[hash] = entities.Source{
		ID: ulid.Make(), Hash: hash, Title: "Doc", Kind: entities.SourceKindMD, Ext: ".md",
	}
	logs := &fakeSourceLog{}
	reset := usecases.ResetSource{
		Index: idx, Trees: &fakeTreeStore{m: map[string][]entities.SourceNode{}},
		SourceLog: logs, Clock: fixedClock{},
		Clean: usecases.CleanSource{
			Atoms: &fakeAtomRepo{}, AtomStore: &fakeAtomStore{m: map[string]entities.Atom{}},
			Index: idx, Clock: fixedClock{},
		},
	}
	if _, err := reset.Execute(ctx, hash); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if len(logs.removed) != 1 || logs.removed[0] != hash {
		t.Errorf("removed = %v, want the source's log to be dropped", logs.removed)
	}
	// The reset writes its own line afterwards, so the log describes the new derivation.
	if !logs.contains("derivation reset") {
		t.Errorf("the reset did not record itself:\n%s", logs.joined())
	}
}
