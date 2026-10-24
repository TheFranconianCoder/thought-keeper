package usecases_test

import (
	"context"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// summaryStore captures the sidecar a summary run writes; the rest of the source store is untouched.
type summaryStore struct {
	interfaces.SourceStore
	written map[string]string
	removed []string
}

func (s *summaryStore) PutRendered(hash, suffix string, content []byte) error {
	if s.written == nil {
		s.written = map[string]string{}
	}
	s.written[hash+suffix] = string(content)
	return nil
}

func (s *summaryStore) RemoveRendered(hash, suffix string) error {
	s.removed = append(s.removed, hash+suffix)
	return nil
}

// summaryGateway answers with a fixed summary and remembers the prompt it was given.
type summaryGateway struct {
	answer string
	prompt string
}

func (g *summaryGateway) Complete(_ context.Context, req interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	for _, m := range req.Messages {
		if m.Role == "user" {
			g.prompt = m.Content
		}
	}
	return interfaces.AgentResponse{
		Message:      interfaces.AgentMessage{Role: "assistant", Content: g.answer},
		FinishReason: "stop", TotalTokens: 42,
	}, nil
}

func summarySource(t *testing.T, idx *fakeIndex, hash string, state entities.StepState) {
	t.Helper()
	if err := idx.UpsertSource(context.Background(), entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindMD, Ext: ".md", Path: hash + ".md",
		Title: "Shape Up", Language: "en", Bytes: 10, ImportedAt: fixedClock{}.Now(),
		Steps: entities.StepSet{entities.StepDistill: {State: state}},
	}); err != nil {
		t.Fatalf("source: %v", err)
	}
}

// TestSourceSummaryWritesFromTheClaims: the summary is a Markdown sidecar written from the claims —
// the prompt carries them and the model's answer lands next to the source, not in the index.
func TestSourceSummaryWritesFromTheClaims(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("a1", 32)
	idx := newFakeIndex()
	summarySource(t, idx, hash, entities.StepOK)
	atoms := &fakeAtomRepo{saved: []entities.Atom{
		{ID: ulid.Make(), Topic: "product-development", Claim: "Shape Up fixes time and appetite.", Body: "Six weeks."},
	}}
	store := &summaryStore{}
	gw := &summaryGateway{answer: "## Overview\n\nShape Up replaces estimates with a fixed appetite."}
	uc := usecases.SourceSummary{
		Index: idx, Atoms: atoms, Store: store, Gateway: gw, Model: "test", Clock: fixedClock{},
	}

	res, err := uc.Execute(ctx, ulid.Make(), hash)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if res.Claims != 1 || res.Chars == 0 {
		t.Fatalf("result = %+v, want one claim and a written summary", res)
	}
	got := store.written[hash+entities.SidecarSummary]
	if !strings.Contains(got, "fixed appetite") {
		t.Fatalf("sidecar = %q, want the model's answer", got)
	}
	if strings.HasPrefix(got, "```") {
		t.Fatalf("sidecar = %q, want no code fence", got)
	}
	if !strings.Contains(gw.prompt, "Shape Up fixes time and appetite.") {
		t.Fatalf("prompt = %q, want the claim in it", gw.prompt)
	}
}

// TestSourceSummaryRefusesAnUnfinishedDerivation: an overview of a half-derived source would describe
// a moving set of claims. The run fails honestly instead of writing one.
func TestSourceSummaryRefusesAnUnfinishedDerivation(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("b2", 32)
	idx := newFakeIndex()
	summarySource(t, idx, hash, entities.StepPending)
	store := &summaryStore{}
	uc := usecases.SourceSummary{
		Index: idx, Atoms: &fakeAtomRepo{}, Store: store, Gateway: &summaryGateway{}, Model: "test", Clock: fixedClock{},
	}
	if _, err := uc.Execute(ctx, ulid.Make(), hash); err == nil {
		t.Fatal("summary of a pending derivation: want an error")
	}
	if len(store.written) != 0 {
		t.Fatalf("wrote %v, want nothing", store.written)
	}
}

// TestSourceSummaryWithoutClaimsIsHonest: a metadata-only source correctly has no claims. It still
// gets a summary — one line saying so — without spending a model call, so the consistency job stops
// asking for a file that will never have prose.
func TestSourceSummaryWithoutClaimsIsHonest(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("c3", 32)
	idx := newFakeIndex()
	summarySource(t, idx, hash, entities.StepOK)
	store := &summaryStore{}
	uc := usecases.SourceSummary{
		Index: idx, Atoms: &fakeAtomRepo{}, Store: store, Clock: fixedClock{},
	}
	res, err := uc.Execute(ctx, ulid.Make(), hash)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if res.Claims != 0 {
		t.Fatalf("claims = %d, want 0", res.Claims)
	}
	if got := store.written[hash+entities.SidecarSummary]; !strings.Contains(got, "no claims") {
		t.Fatalf("sidecar = %q, want the honest empty note", got)
	}
}

// TestCleanSourceDropsTheSummary: a reset (or a correction that drops claims) must not leave an
// overview describing a derivation that no longer exists. The file goes with the claims; the next
// complete distill writes a new one.
func TestCleanSourceDropsTheSummary(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("d4", 32)
	idx := newFakeIndex()
	summarySource(t, idx, hash, entities.StepOK)
	atom := entities.Atom{
		ID: ulid.Make(), Topic: "caching", Claim: "Caching is subtle", Body: "b",
		Type: entities.AtomClaim, Status: entities.AtomActive,
		Sources:   []entities.AtomSource{{Hash: hash, NodeID: "0.1"}},
		CreatedAt: fixedClock{}.Now(), UpdatedAt: fixedClock{}.Now(),
	}
	repo := &fakeAtomRepo{saved: []entities.Atom{atom}}
	atomStore := &fakeAtomStore{m: map[string]entities.Atom{atom.ID.String(): atom}}
	store := &summaryStore{}
	uc := usecases.CleanSource{
		Atoms: repo, AtomStore: atomStore, Store: store, Index: idx, Clock: fixedClock{},
	}
	if _, err := uc.Execute(ctx, hash); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if len(store.removed) != 1 || store.removed[0] != hash+entities.SidecarSummary {
		t.Fatalf("removed = %v, want the source's summary", store.removed)
	}
}
