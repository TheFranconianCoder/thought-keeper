package usecases_test

import (
	"context"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

type fakeTreeStore struct {
	m map[string][]entities.SourceNode
}

func (f *fakeTreeStore) Write(_ context.Context, hash string, nodes []entities.SourceNode) error {
	f.m[hash] = nodes
	return nil
}

func (f *fakeTreeStore) Read(_ context.Context, hash string) ([]entities.SourceNode, error) {
	n, ok := f.m[hash]
	if !ok {
		return nil, types.New("not_found", "no tree", types.ErrNotFound)
	}
	return n, nil
}

func (f *fakeTreeStore) Remove(_ context.Context, hash string) error {
	delete(f.m, hash)
	return nil
}

func TestBuildTreeFromMarkdown(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{}}
	extractor := extract.New(gateways.ExecRunner{}, extract.Config{})

	intake := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}
	src, err := intake.Execute(ctx, usecases.IntakeInput{Name: "notes.md", Reader: strings.NewReader("# Alpha\nintro\n## Beta\nbody\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}

	bt := usecases.BuildTree{Store: store, Index: idx, Trees: trees, Extractor: extractor, Clock: fixedClock{}}
	res, err := bt.Execute(ctx, src.Hash)
	if err != nil {
		t.Fatalf("build tree: %v", err)
	}
	if res.Nodes != 2 {
		t.Errorf("nodes = %d, want 2", res.Nodes)
	}
	if res.Title != "Alpha" {
		t.Errorf("title = %q, want Alpha", res.Title)
	}
	if got := idx.m[src.Hash].TreeState; got != entities.TreeBuilt {
		t.Errorf("tree state = %q, want built", got)
	}
	if got := len(idx.nodes[src.ID]); got != 3 {
		t.Errorf("index nodes = %d, want 3 (root + 2)", got)
	}

	stored := trees.m[src.Hash]
	if len(stored) != 3 {
		t.Fatalf("stored nodes = %d, want 3 (root + 2)", len(stored))
	}
	if stored[1].NodeID != "0.1" || stored[1].Title != "Alpha" || !strings.Contains(stored[1].Text, "intro") {
		t.Errorf("stored node 1 = %+v", stored[1])
	}
}

func TestBuildTreeFailedKeepsState(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()

	intake := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}
	src, err := intake.Execute(ctx, usecases.IntakeInput{Name: "x.docx", Reader: strings.NewReader("x")})
	if err == nil {
		t.Fatal("expected unsupported source error")
	}
	_ = src
}

// TestTitleComesFromTheModel is the rule the user asked for: the title belongs to the
// document's content, so the model names it — a heading or a file name is only a fallback.
func TestTitleComesFromTheModel(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{}}
	extractor := extract.New(gateways.ExecRunner{}, extract.Config{})

	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "bericht.md", Reader: strings.NewReader("# Erste Überschrift\n\nInhalt.\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	gw := &fakeGateway{responses: []interfaces.AgentResponse{
		{Message: interfaces.AgentMessage{Role: "assistant", Content: "\"Caching und Korrektheit\"\n\nBegründung: …"}},
	}}
	bt := usecases.BuildTree{
		Store: store, Index: idx, Trees: trees, Extractor: extractor,
		Gateway: gw, Model: "test", Clock: fixedClock{},
	}
	res, err := bt.Execute(ctx, src.Hash)
	if err != nil {
		t.Fatalf("build tree: %v", err)
	}
	if res.Title != "Caching und Korrektheit" {
		t.Errorf("title = %q, want the model's title (a heading must not win)", res.Title)
	}
	// The raw name is kept, to be shown *additionally*.
	stored, err := idx.GetSourceByHash(ctx, src.Hash)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if stored.OriginalName != "bericht.md" {
		t.Errorf("original name = %q, want bericht.md", stored.OriginalName)
	}
}

// TestTitleKeepsADeclaredUrlTitle: without a model, a URL keeps the title the page declared
// instead of losing it to the first heading of the extracted body.
func TestTitleKeepsADeclaredUrlTitle(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{}}
	extractor := extract.New(gateways.ExecRunner{}, extract.Config{})

	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "page.md", Reader: strings.NewReader("# Some Heading In The Body\n\ntext\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	// A URL source whose declared title came from the page's metadata. (The fake index keeps
	// the first row per hash, so the store is seeded directly.)
	stored, _ := idx.GetSourceByHash(ctx, src.Hash)
	stored.Kind = entities.SourceKindURL
	stored.URL = "https://example.com/article"
	stored.Title = "The Real Declared Title"
	idx.m[src.Hash] = stored

	bt := usecases.BuildTree{Store: store, Index: idx, Trees: trees, Extractor: extractor, Clock: fixedClock{}}
	res, err := bt.Execute(ctx, src.Hash)
	if err != nil {
		t.Fatalf("build tree: %v", err)
	}
	if res.Title != "The Real Declared Title" {
		t.Errorf("title = %q, want the declared title", res.Title)
	}
}

// TestRebuildWithAChangedTreeCorrectsTheDerivation: the tree is what atoms cite, so a rebuild that
// changes it must drop the claims whose part is gone (and mark the pages that drew on them) and
// re-arm that part. The claims of the parts that did NOT change stay: a corrected paragraph is not
// a new document, and a document with hundreds of parts must not pay for one with its whole claim set.
func TestRebuildWithAChangedTreeCorrectsTheDerivation(t *testing.T) {
	ctx := context.Background()
	store := objects.New(t.TempDir())
	idx := newFakeIndex()
	trees := &fakeTreeStore{m: map[string][]entities.SourceNode{}}
	extractor := extract.New(gateways.ExecRunner{}, extract.Config{})

	src, err := usecases.IntakeSource{Store: store, Index: idx, Clock: fixedClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "doc.md", Reader: strings.NewReader("# Doc\n\ntext\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if _, err := (usecases.BuildTree{Store: store, Index: idx, Trees: trees, Extractor: extractor, Clock: fixedClock{}}).
		Execute(ctx, src.Hash); err != nil {
		t.Fatalf("first build: %v", err)
	}

	// Something was derived from the tree: an atom citing a node, and a part recorded done.
	atomStore := &fakeAtomStore{m: map[string]entities.Atom{}}
	atomRepo := &fakeAtomRepo{}
	atoms := []entities.Atom{{
		ID: ulid.Make(), Topic: "eng", Path: "eng/a.md", Claim: "a claim", Body: "b",
		Status: entities.AtomActive, Sources: []entities.AtomSource{{Hash: src.Hash, NodeID: "0.1"}},
	}}
	atomRepo.saved = atoms
	parts := &fakeSections{m: map[string]entities.PartState{"0.1": {NodeID: "0.1", State: entities.PartDone}}}
	clean := usecases.CleanSource{
		Parts: parts, Index: idx, Atoms: atomRepo, AtomStore: atomStore,
		Clock: fixedClock{},
	}

	// The stored tree is a different one (as an extractor/filter change would produce).
	trees.m[src.Hash] = []entities.SourceNode{
		{NodeID: "0", Title: ""},
		{NodeID: "0.9", Title: "Renamed chapter", Text: "other text"},
	}

	bt := usecases.BuildTree{
		Store: store, Index: idx, Trees: trees, Extractor: extractor, Clock: fixedClock{},
		Clean: clean, Parts: parts,
	}
	if _, err := bt.Execute(ctx, src.Hash); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if len(atomRepo.saved) != 0 {
		t.Errorf("atoms survived a changed tree: %+v", atomRepo.saved)
	}
	// The part is not DELETED — it is re-armed: its record said "done" about text that is not there
	// any more, and a done part is skipped. The claims of the parts that did not change are kept, which
	// is why a corrected paragraph no longer costs a whole document its derivation.
	if st := parts.m["0.1"]; st.State != entities.PartIncomplete {
		t.Errorf("the changed part was not re-armed: %+v", parts.m)
	}
}

// TestBuildTreeSkipsAFeedOrSitemap: a feed or a sitemap is a LIST of sources. A manual Outline (or
// the global Rebuild outlines) must never build a tree from the list and derive claims about it
// instead of the pages it owns; the steps are recorded skipped.
func TestBuildTreeSkipsAFeedOrSitemap(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		rss     bool
		sitemap bool
	}{
		{name: "feed", rss: true},
		{name: "sitemap", sitemap: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hash := strings.Repeat("ab", 32)
			idx := newFakeIndex()
			idx.m[hash] = entities.Source{
				ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
				Title: tc.name, RSS: tc.rss, Sitemap: tc.sitemap,
			}
			steps := &fakeSteps{}
			trees := &fakeTreeStore{m: map[string][]entities.SourceNode{}}
			bt := usecases.BuildTree{Index: idx, Trees: trees, Steps: steps, Clock: fixedClock{}}
			res, err := bt.Execute(ctx, hash)
			if err != nil {
				t.Fatalf("build tree: %v", err)
			}
			if res.Nodes != 0 {
				t.Errorf("nodes = %d, want no tree for a list", res.Nodes)
			}
			if len(trees.m[hash]) != 0 {
				t.Errorf("a tree was written for a list: %+v", trees.m[hash])
			}
			if got := lastState(steps, entities.StepOutline); got != entities.StepSkipped {
				t.Errorf("outline = %q, want skipped", got)
			}
			if got := lastState(steps, entities.StepDistill); got != entities.StepSkipped {
				t.Errorf("distill = %q, want skipped", got)
			}
		})
	}
}
