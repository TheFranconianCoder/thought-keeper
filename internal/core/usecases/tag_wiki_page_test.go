package usecases_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/partialentities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/vocabulary"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/wiki"
)

// TestTagWikiPageWritesDerivedTags: a page's tags are derived from its own title and prose, and they are
// written APART from the human's — a tagging run must never overwrite what the reader wrote, and deleting
// a derived tag sticks because the next run replaces exactly that key.
func TestTagWikiPageWritesDerivedTags(t *testing.T) {
	ctx := context.Background()
	pages := wiki.New(t.TempDir())

	page := entities.WikiPage{
		ID: ulid.Make(), Title: "Go Concurrency", Tags: []string{"mytag"},
		Body: "# Channels\n\nChannels connect goroutines.", CreatedAt: wikiClock{}.Now(),
	}
	if err := pages.Write(ctx, page); err != nil {
		t.Fatalf("write: %v", err)
	}

	gateway := &fakeGateway{responses: []interfaces.AgentResponse{
		{Message: interfaces.AgentMessage{Content: "Here you go:\n```json\n{\"tags\":[\"Go\",\"concurrency\",\"go\",\"Channels\"]}\n```"}},
	}}
	tagger := usecases.TagWikiPage{
		Gateway: gateway, Model: "m", Pages: pages, Clock: wikiClock{},
	}
	res, err := tagger.Execute(ctx, page.ID)
	if err != nil {
		t.Fatalf("tag: %v", err)
	}
	if len(res.Tags) != 3 { // Go/go collapse, so go, concurrency, channels
		t.Fatalf("tags = %v, want the three distinct slugs", res.Tags)
	}

	got, err := pages.Read(ctx, page.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "mytag" {
		t.Fatalf("the human's tags were changed: %v", got.Tags)
	}
	want := []string{"go", "concurrency", "channels"}
	if len(got.AutoTags) != len(want) {
		t.Fatalf("auto tags = %v, want %v", got.AutoTags, want)
	}
	for i := range want {
		if got.AutoTags[i] != want[i] {
			t.Fatalf("auto tags = %v, want %v", got.AutoTags, want)
		}
	}
	// The prompt carries the page's title, and the vocabulary is offered for reuse.
	if len(gateway.requests) != 1 {
		t.Fatalf("requests = %d, want one", len(gateway.requests))
	}
	user := gateway.requests[0].Messages[len(gateway.requests[0].Messages)-1].Content
	if !strings.Contains(user, "Go Concurrency") {
		t.Errorf("the prompt does not carry the title: %q", user)
	}
}

// TestTagRenameRewritesPageTags: a tag is a label on the page, so the curation's rename is a frontmatter
// write — the prose, the source representation and the claims are untouched.
func TestTagRenameRewritesPageTags(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	pages := wiki.New(filepath.Join(root, "wiki"))
	db, err := persistence.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	vocab := vocabulary.New(filepath.Join(root, "vocabulary"))

	page := entities.WikiPage{
		ID: ulid.Make(), Title: "A page", Tags: []string{"llm-agent", "tools"},
		Body: "# A page\n\nText.", CreatedAt: wikiClock{}.Now(),
	}
	if err := pages.Write(ctx, page); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := (usecases.ApplyVocabularyMapping{
		Namespace: entities.VocabularyWikiTags,
		Mapping:   map[string]string{"llm-agent": "llm-agents"},
		Vocab:     vocab, Wiki: pages, Clock: wikiClock{},
	}).Execute(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got, err := pages.Read(ctx, page.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "llm-agents" || got.Tags[1] != "tools" {
		t.Fatalf("tags = %v, want the rename applied", got.Tags)
	}
	// The alias survives in the vocabulary, so the next tagging run writes the canonical name.
	v, err := vocab.Read(entities.VocabularyWikiTags)
	if err != nil {
		t.Fatalf("vocabulary: %v", err)
	}
	if v.Absorbed["llm-agent"] != "llm-agents" {
		t.Fatalf("absorbed = %v, want the alias", v.Absorbed)
	}
}

// TestVocabularyReportSeesWikiTags: the tags namespace is observed from the PAGES (the file is the
// truth), and it is left out while no page carries one — an empty catalog is not a health report.
func TestVocabularyReportSeesWikiTags(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	pages := wiki.New(filepath.Join(root, "wiki"))
	vocab := vocabulary.New(filepath.Join(root, "vocabulary"))
	report := usecases.VocabularyReport{Atoms: &fakeAtomRepo{}, Wiki: pages, Vocab: vocab}

	healths, err := report.Execute(ctx)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	for _, h := range healths {
		if h.Namespace == string(entities.VocabularyWikiTags) {
			t.Fatalf("an empty tag catalog was reported: %+v", h)
		}
	}

	page := entities.WikiPage{
		ID: ulid.Make(), Title: "p", Tags: []string{"go"}, AutoTags: []string{"caching", "go"},
		Body: "text", CreatedAt: wikiClock{}.Now(),
	}
	if err := pages.Write(ctx, page); err != nil {
		t.Fatalf("write: %v", err)
	}
	healths, err = report.Execute(ctx)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	var tags *partialentities.VocabularyHealth
	for i := range healths {
		if healths[i].Namespace == string(entities.VocabularyWikiTags) {
			tags = &healths[i]
		}
	}
	if tags == nil {
		t.Fatalf("no tag namespace in the report: %+v", healths)
	}
	// "go" is on the page twice (written and derived) and must count ONCE per page.
	if tags.Names != 2 || tags.Uses != 2 {
		t.Fatalf("names=%d uses=%d, want 2/2 (a tag counts once per page)", tags.Names, tags.Uses)
	}
}
