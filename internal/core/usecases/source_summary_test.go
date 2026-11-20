package usecases_test

import (
	"context"
	"strings"
	"testing"
	"time"

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
	system string
}

func (g *summaryGateway) Complete(_ context.Context, req interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			g.prompt = m.Content
		case "system":
			g.system = m.Content
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
	tags := &fakeSourceTags{}
	gw := &summaryGateway{answer: `{"summary":["## Overview","Shape Up replaces estimates with a fixed appetite."],"tags":["product-development","shape-up"]}`}
	uc := usecases.Sources{
		Index: idx, Atoms: atoms, Store: store, Gateway: gw, Model: "test", Clock: fixedClock{},
		Tags: tags, MaxTags: 6, Vocab: &fakeVocabStore{},
	}

	res, err := uc.Summary(ctx, ulid.Make(), hash)
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
	// The answer is JSON because it carries the tags too; the FILE is prose. A reader must never see
	// the envelope, and the paragraphs joined by blank lines.
	if strings.Contains(got, `"tags"`) || strings.Contains(got, `{"summary"`) {
		t.Fatalf("sidecar = %q, want plain prose", got)
	}
	if !strings.Contains(got, "\n\n") {
		t.Fatalf("sidecar = %q, want the paragraphs separated", got)
	}
	// The labels were decided from the same claims, in the same request.
	if res.Tags == nil || len(res.Tags) != 2 || tags.auto[hash][0] != "product-development" {
		t.Fatalf("tags = %v (written %v), want the two labels filed", res.Tags, tags.auto[hash])
	}
	if !strings.Contains(gw.prompt, "Shape Up fixes time and appetite.") {
		t.Fatalf("prompt = %q, want the claim in it", gw.prompt)
	}
	// A summary is always English, even when the source and its claims are not.
	if !strings.Contains(gw.system, "Write in ENGLISH") {
		t.Fatalf("system prompt = %q, want the English rule", gw.system)
	}
	if !strings.Contains(gw.prompt, "write the summary in English") {
		t.Fatalf("user prompt = %q, want the English reminder", gw.prompt)
	}
}

// TestOverviewAnswerToleratesAPlainString: a model that answers the overview as one string instead of
// a paragraph list is not a failed run — the prose is what the reader wants.
func TestOverviewAnswerToleratesAPlainString(t *testing.T) {
	body, tags, err := usecases.ParseOverviewAnswer(`{"summary":"One paragraph.\n\nAnother.","tags":["a"]}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(body, "Another.") || len(tags) != 1 {
		t.Fatalf("body = %q tags = %v", body, tags)
	}
	if _, _, err := usecases.ParseOverviewAnswer("no json here"); err == nil {
		t.Fatal("an answer without a JSON object must be an error")
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
	uc := usecases.Sources{
		Index: idx, Atoms: &fakeAtomRepo{}, Store: store, Gateway: &summaryGateway{}, Model: "test", Clock: fixedClock{},
	}
	if _, err := uc.Summary(ctx, ulid.Make(), hash); err == nil {
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
	uc := usecases.Sources{
		Index: idx, Atoms: &fakeAtomRepo{}, Store: store, Clock: fixedClock{},
	}
	res, err := uc.Summary(ctx, ulid.Make(), hash)
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
	uc := usecases.Derivation{
		Atoms: repo, AtomStore: atomStore, Store: store, Index: idx, Clock: fixedClock{},
	}
	if _, err := uc.Clean(ctx, hash); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if len(store.removed) != 1 || store.removed[0] != hash+entities.SidecarSummary {
		t.Fatalf("removed = %v, want the source's summary", store.removed)
	}
}

// windowGateway answers the way the guarded gateway does inside a blocked window.
type windowGateway struct{ reopen time.Time }

func (g windowGateway) Complete(context.Context, interfaces.AgentRequest) (interfaces.AgentResponse, error) {
	return interfaces.AgentResponse{}, interfaces.WindowClosed(g.reopen)
}

// TestSourceSummaryDefersInsideAModelWindow: a window is not a failed summary. The run says
// `aborted` with the reopening instant, the sentinel survives to the caller (so the app keeps the
// source in the backlog instead of retrying it), and no sidecar is written.
func TestSourceSummaryDefersInsideAModelWindow(t *testing.T) {
	ctx := context.Background()
	hash := strings.Repeat("c3", 32)
	idx := newFakeIndex()
	summarySource(t, idx, hash, entities.StepOK)
	store := &summaryStore{}
	runs := &fakeRunRepo{}
	reopen := fixedClock{}.Now().Add(2 * time.Hour)
	uc := usecases.Sources{
		Index: idx,
		Atoms: &fakeAtomRepo{saved: []entities.Atom{
			{ID: ulid.Make(), Topic: "caching", Claim: "A cache trades freshness for speed.", Body: ""},
		}},
		Store: store, Gateway: windowGateway{reopen: reopen}, Model: "test",
		Clock: fixedClock{}, Runs: runs,
	}

	if _, err := uc.Summary(ctx, ulid.Make(), hash); !interfaces.IsWindowClosed(err) {
		t.Fatalf("err = %v, want the window deferral", err)
	}
	if runs.run.Status != entities.RunAborted {
		t.Fatalf("run status = %q, want aborted", runs.run.Status)
	}
	if !strings.Contains(runs.run.Error, "deferred") || !strings.Contains(runs.run.Error, reopen.Format(time.RFC3339)) {
		t.Fatalf("run error = %q, want the deferral and the reopening instant", runs.run.Error)
	}
	if len(store.written) != 0 {
		t.Fatalf("a deferred summary wrote a sidecar: %v", store.written)
	}
}
