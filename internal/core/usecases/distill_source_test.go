package usecases_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// fakeGateway replays a scripted sequence of responses.
type fakeGateway struct {
	responses []interfaces.AgentResponse
	i         int
	requests  []interfaces.AgentRequest
}

func (g *fakeGateway) Complete(_ context.Context, req interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	g.requests = append(g.requests, req)
	if g.i >= len(g.responses) {
		return interfaces.AgentResponse{Message: interfaces.AgentMessage{Role: "assistant", Content: "done"}}, nil
	}
	r := g.responses[g.i]
	g.i++
	return r, nil
}

// fakeAtomStore is the file store stand-in. Parts are derived in parallel, so it needs the same
// thread-safety the real store has (files) — without it the tests race instead of proving anything.
type fakeAtomStore struct {
	// usage is the atom→pages back-reference (the <id>.meta record).
	mu sync.Mutex
	m  map[string]entities.Atom
}

func (s *fakeAtomStore) Write(_ context.Context, a entities.Atom) (entities.Atom, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Addressed by the ID, as the real store does.
	a.Path = a.ID.String() + ".md"
	s.m[a.ID.String()] = a
	return a, nil
}

func (s *fakeAtomStore) Read(context.Context, ulid.ULID) (entities.Atom, error) {
	return entities.Atom{}, nil
}

func (s *fakeAtomStore) Trash(_ context.Context, a entities.Atom) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, a.ID.String())
	return nil
}

func (s *fakeAtomStore) Remove(_ context.Context, a entities.Atom) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, a.ID.String())
	return nil
}

func (s *fakeAtomStore) Scan(context.Context) ([]entities.Atom, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]entities.Atom, 0, len(s.m))
	for _, a := range s.m {
		out = append(out, a)
	}
	return out, nil, nil
}

type fakeAtomRepo struct {
	mu    sync.Mutex
	saved []entities.Atom
}

func (r *fakeAtomRepo) SaveAtom(_ context.Context, a entities.Atom) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.saved {
		if r.saved[i].ID == a.ID {
			r.saved[i] = a
			return nil
		}
	}
	r.saved = append(r.saved, a)
	return nil
}

func (r *fakeAtomRepo) GetAtom(_ context.Context, id ulid.ULID) (entities.Atom, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.saved {
		if a.ID == id {
			return a, nil
		}
	}
	return entities.Atom{}, nil
}

func (r *fakeAtomRepo) ListAtoms(_ context.Context, topic string, limit, _ int) ([]entities.Atom, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]entities.Atom, 0, len(r.saved))
	for _, a := range r.saved {
		if topic != "" && a.Topic != topic {
			continue
		}
		out = append(out, a)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *fakeAtomRepo) ListAtomsBySource(context.Context, string) ([]entities.Atom, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]entities.Atom(nil), r.saved...), nil
}

func (r *fakeAtomRepo) ListAtomsByTagStub(context.Context, string, int) ([]entities.Atom, error) {
	return nil, nil
}

// StreamAtomTexts walks the stored claims in id order, one at a time — the association pass'
// whole-vault read.
func (r *fakeAtomRepo) StreamAtomTexts(_ context.Context, fn func(entities.Atom) error) error {
	r.mu.Lock()
	out := append([]entities.Atom(nil), r.saved...)
	r.mu.Unlock()
	slices.SortFunc(out, byAtomID)
	for _, a := range out {
		if err := fn(entities.Atom{ID: a.ID, Topic: a.Topic, Claim: a.Claim, Body: a.Body}); err != nil {
			return err
		}
	}
	return nil
}

// StreamClaimIdentities walks the stored claims' identity in id order (the duplicate check's read).
func (r *fakeAtomRepo) StreamClaimIdentities(_ context.Context, fn func(entities.Atom) error) error {
	r.mu.Lock()
	out := append([]entities.Atom(nil), r.saved...)
	r.mu.Unlock()
	slices.SortFunc(out, byAtomID)
	for _, a := range out {
		row := entities.Atom{
			ID: a.ID, Topic: a.Topic, Claim: a.Claim, Body: a.Body, Type: a.Type, Status: a.Status,
		}
		row.Sources = append([]entities.AtomSource(nil), a.Sources...)
		if err := fn(row); err != nil {
			return err
		}
	}
	return nil
}

// AtomTextsForIDs returns the text of exactly these claims.
func (r *fakeAtomRepo) AtomTextsForIDs(_ context.Context, ids []ulid.ULID) ([]entities.Atom, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := make(map[ulid.ULID]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []entities.Atom
	for _, a := range r.saved {
		if want[a.ID] {
			out = append(out, entities.Atom{ID: a.ID, Topic: a.Topic, Claim: a.Claim, Body: a.Body})
		}
	}
	slices.SortFunc(out, byAtomID)
	return out, nil
}

// AtomCount is the claim total.
func (r *fakeAtomRepo) AtomCount(context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.saved), nil
}

// SourcesForAtoms returns the provenance of exactly these claims.
func (r *fakeAtomRepo) SourcesForAtoms(_ context.Context, ids []ulid.ULID) (map[ulid.ULID][]entities.AtomSource, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := make(map[ulid.ULID]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := map[ulid.ULID][]entities.AtomSource{}
	for _, a := range r.saved {
		if want[a.ID] && len(a.Sources) > 0 {
			out[a.ID] = append(out[a.ID], a.Sources...)
		}
	}
	return out, nil
}

// ListOldestAtoms is the queue read: the stored claims by their update time, bounded.
func (r *fakeAtomRepo) ListOldestAtoms(_ context.Context, limit int) ([]entities.Atom, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]entities.Atom(nil), r.saved...)
	slices.SortStableFunc(out, func(a, b entities.Atom) int {
		if d := a.UpdatedAt.Compare(b.UpdatedAt); d != 0 {
			return d
		}
		return bytes.Compare(a.ID[:], b.ID[:])
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *fakeAtomRepo) AtomIDs(context.Context) ([]ulid.ULID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ulid.ULID, 0, len(r.saved))
	for _, a := range r.saved {
		out = append(out, a.ID)
	}
	slices.SortFunc(out, func(a, b ulid.ULID) int { return bytes.Compare(a[:], b[:]) })
	return out, nil
}

func (r *fakeAtomRepo) AtomAddresses(context.Context) ([]entities.Atom, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]entities.Atom, 0, len(r.saved))
	for _, a := range r.saved {
		out = append(out, entities.Atom{ID: a.ID, Path: a.Path})
	}
	slices.SortFunc(out, byAtomID)
	return out, nil
}

// byAtomID is the index order the projection queries promise.
func byAtomID(a, b entities.Atom) int { return bytes.Compare(a.ID[:], b.ID[:]) }

func (r *fakeAtomRepo) ListClaimTopics(context.Context) ([]entities.TopicUse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return []entities.TopicUse{{Topic: "caching", Count: len(r.saved)}}, nil
}

func (r *fakeAtomRepo) SupersedeAtomsBySource(context.Context, string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int64
	for i := range r.saved {
		r.saved[i].Status = entities.AtomSuperseded
		n++
	}
	return n, nil
}

func (r *fakeAtomRepo) DeleteAtom(_ context.Context, id ulid.ULID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, a := range r.saved {
		if a.ID == id {
			r.saved = append(r.saved[:i], r.saved[i+1:]...)
			break
		}
	}
	return nil
}

func (r *fakeAtomRepo) CountAtomSources(context.Context, ulid.ULID) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return 1, nil
}

func (r *fakeAtomRepo) NodeRefsBySource(context.Context, string) (map[ulid.ULID][]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := map[ulid.ULID][]string{}
	for _, a := range r.saved {
		for _, src := range a.Sources {
			if src.NodeID != "" {
				m[a.ID] = append(m[a.ID], src.NodeID)
			}
		}
	}
	return m, nil
}

func (r *fakeAtomRepo) TagMapStub(context.Context) (map[ulid.ULID][]string, error) { return nil, nil }

type fakeRunRepo struct {
	run   entities.Run
	steps []entities.RunStep
}

func (r *fakeRunRepo) CreateRun(_ context.Context, run entities.Run) error {
	r.run = run
	return nil
}

func (r *fakeRunRepo) FinishRun(_ context.Context, run entities.Run) error {
	r.run = run
	return nil
}

func (r *fakeRunRepo) AddStep(_ context.Context, s entities.RunStep) error {
	r.steps = append(r.steps, s)
	return nil
}

func (r *fakeRunRepo) GetRun(context.Context, ulid.ULID) (entities.Run, error) {
	return r.run, nil
}

func (r *fakeRunRepo) ListSteps(context.Context, ulid.ULID) ([]entities.RunStep, error) {
	return r.steps, nil
}

func (r *fakeRunRepo) AbandonRunningRuns(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}

// TestDistillRefusesAnOutlineThatReferencesMissingText: a page rewritten under its outline leaves a
// node pointing past the snapshot. Deriving the other parts and calling the source finished is what
// left it "3 of 4 parts" for ever; the run says the outline is stale, and the app rebuilds it.
func TestDistillRefusesAnOutlineThatReferencesMissingText(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{}}
	extractor := extract.New(gateways.ExecRunner{}, extract.Config{})

	src, err := usecases.Sources{Store: store, Index: idx, Clock: fixedClock{}}.Intake(ctx,
		usecases.IntakeInput{Name: "notes.md", Reader: strings.NewReader("# Caching\ncaching is subtle\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if _, err := (usecases.Trees{Store: store, Index: idx, Trees: trees, Extractor: extractor, Clock: fixedClock{}}).Build(ctx, src.Hash); err != nil {
		t.Fatalf("build tree: %v", err)
	}

	// The page was rewritten under the outline: the reference now points past the text.
	nodes := trees.m[src.Hash]
	found := false
	for i := range nodes {
		if nodes[i].TextHash == "" {
			continue
		}
		nodes[i].Text, nodes[i].TextStart, nodes[i].TextEnd = "", 0, 1<<20
		found = true
		break
	}
	if !found {
		t.Fatal("the fixture has no text-bearing node")
	}

	az := usecases.Derivation{Store: store, Index: idx, Trees: trees, Clock: fixedClock{}}
	rec := usecases.NewStepRecorder(ctx, &fakeRunRepo{}, ulid.Make(), nil)
	if _, err := az.Distill(ctx, src.Hash, rec); !errors.Is(err, types.ErrOutlineStale) {
		t.Fatalf("distill = %v, want ErrOutlineStale", err)
	}
}

func TestDistillWritesAtomWithProvenance(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{}}
	extractor := extract.New(gateways.ExecRunner{}, extract.Config{})

	src, err := usecases.Sources{Store: store, Index: idx, Clock: fixedClock{}}.Intake(ctx, usecases.IntakeInput{Name: "notes.md", Reader: strings.NewReader("# Caching\ncaching is subtle\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if _, err := (usecases.Trees{Store: store, Index: idx, Trees: trees, Extractor: extractor, Clock: fixedClock{}}).Build(ctx, src.Hash); err != nil {
		t.Fatalf("build tree: %v", err)
	}

	writeArgs := `{"topic":"caching","claim":"Caching is only safe when the key covers every input.",` +
		`"body":"If two inputs share a key, one value is wrong.","type":"claim","nodes":["0.1"]}`
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		claimsResponse(writeArgs), // the write pass: the claims as data
		completeResponse(),        // the audit: nothing is missing
	}}

	atomStore := &fakeAtomStore{m: map[string]entities.Atom{}}
	atomRepo := &fakeAtomRepo{}
	runRepo := &fakeRunRepo{}
	az := usecases.Derivation{
		Index: idx, Trees: trees, AtomStore: atomStore, Atoms: atomRepo,
		Gateway: gw, Model: "test-model", MaxIterations: 4, Clock: fixedClock{},
	}
	rec := usecases.NewStepRecorder(ctx, runRepo, ulid.Make(), nil)

	res, err := az.Distill(ctx, src.Hash, rec)
	if err != nil {
		t.Fatalf("distill: %v", err)
	}
	if res.Atoms != 1 || res.TotalTokens != 40 {
		t.Errorf("result = %+v, want 1 atom / 40 tokens (the answer and the audit)", res)
	}
	if len(atomRepo.saved) != 1 {
		t.Fatalf("saved atoms = %d, want 1", len(atomRepo.saved))
	}
	atom := atomRepo.saved[0]
	if atom.Topic != "caching" || !strings.Contains(atom.Claim, "Caching is only safe") {
		t.Errorf("atom = %+v", atom)
	}
	if len(atom.Sources) != 1 || atom.Sources[0].Hash != src.Hash || atom.Sources[0].NodeID != "0.1" {
		t.Errorf("provenance = %+v", atom.Sources)
	}
	// Tools only READ: the write is the app's, from the answer. No write tool call is recorded — the
	// claim itself is the record.
	for _, st := range runRepo.steps {
		if st.Tool == "write_atom" {
			t.Errorf("a write tool was called: %+v", st)
		}
	}
}
