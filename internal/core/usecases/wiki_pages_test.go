package usecases_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/atoms"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/extract"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence/sqlc"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/sourcemeta"
	treestore "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/tree"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/wiki"
)

type wikiClock struct{}

func (wikiClock) Now() time.Time { return time.UnixMilli(1_700_000_000_000).UTC() }

// TestSaveWikiPageOwnsOneStableSource: a page edit must reprocess THE source, not file a second one.
// The stable WikiHash is what makes that possible, and the source identity keeps capture open for
// the pump.
func TestSaveWikiPageOwnsOneStableSource(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourcesDir := filepath.Join(root, "sources")
	atomsDir := filepath.Join(root, "atoms")
	wikiDir := filepath.Join(root, "wiki")

	db, err := persistence.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := objects.New(sourcesDir)
	atomRepo := sqlc.NewAtomRepository(db)
	atomStore := atoms.New(atomsDir)
	index := sqlc.NewSourceRepository(db)
	trees := treestore.New(sourcesDir)
	parts := sqlc.NewPartRepository(db)
	steps := sourcemeta.NewTracker(sourcemeta.New(sourcesDir), index, nil)
	clean := usecases.CleanSource{Parts: parts, Atoms: atomRepo, AtomStore: atomStore, Index: index, Clock: wikiClock{}}
	deleter := usecases.DeleteSource{Store: store, Index: index, Atoms: atomStore, AtomRepo: atomRepo, Steps: steps, Clock: wikiClock{}}
	pages := wiki.New(wikiDir)
	save := usecases.SaveWikiPage{
		Pages: pages, Store: store, Index: index, Steps: steps, Delete: deleter, Clock: wikiClock{},
	}

	first, err := save.Execute(ctx, usecases.WikiPageInput{
		Title: "Go Concurrency", Tags: []string{"Go", "concurrency"},
		Body: "# Channels\n\nChannels connect goroutines.",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if first.Hash != entities.WikiHash(first.Page.ID) {
		t.Fatalf("source hash = %q, want the page's stable address", first.Hash)
	}
	src, err := index.GetSourceByHash(ctx, first.Hash)
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if !src.IsWiki() || !src.NeedsCapture() {
		t.Fatalf("source identity = %+v, want a wiki source that needs capture", src)
	}
	if src.Title != "Go Concurrency" {
		t.Fatalf("source title = %q", src.Title)
	}
	captured, err := (usecases.CaptureSource{
		Store: store, Index: index, Extractor: extract.New(gateways.ExecRunner{}, extract.Config{}),
		Steps: steps, Clock: wikiClock{},
	}).Execute(ctx, first.Hash)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if captured.Rendered == "" {
		t.Fatal("capture wrote no rendering for a wiki source")
	}
	if _, ok := store.StatObject(first.Hash, ".md"); !ok {
		t.Fatal("capture removed the wiki page's own object")
	}
	built, err := (usecases.BuildTree{
		Store: store, Index: index, Trees: trees, Extractor: extract.New(gateways.ExecRunner{}, extract.Config{}),
		Steps: steps, Clean: clean, Parts: parts, Clock: wikiClock{},
	}).Execute(ctx, first.Hash)
	if err != nil {
		t.Fatalf("outline: %v", err)
	}
	if built.Nodes == 0 || built.Title != "Go Concurrency" {
		t.Fatalf("tree = %+v, want nodes and the page title", built)
	}

	// The same page ID, changed body: one source row, same ID and hash, capture open again.
	second, err := save.Execute(ctx, usecases.WikiPageInput{
		ID: first.Page.ID, Title: "Go Concurrency", Tags: []string{"go"},
		Body: "# Channels\n\nA changed paragraph.",
	})
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if second.Hash != first.Hash {
		t.Fatalf("second hash = %q, want the stable %q", second.Hash, first.Hash)
	}
	again, err := index.GetSourceByHash(ctx, second.Hash)
	if err != nil {
		t.Fatalf("second source: %v", err)
	}
	if again.ID != src.ID {
		t.Fatalf("source id changed: %v → %v", src.ID, again.ID)
	}
	if again.Title != "Go Concurrency" {
		t.Fatalf("source title after edit = %q, want the page title", again.Title)
	}
	rec, err := steps.Steps(second.Hash)
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	if rec.State(entities.StepCapture) != entities.StepPending {
		t.Fatalf("capture = %q, want pending after an edit", rec.State(entities.StepCapture))
	}
	page, err := pages.Read(ctx, first.Page.ID)
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if !strings.Contains(page.Body, "changed paragraph") {
		t.Fatalf("page body = %q", page.Body)
	}

	// A tag-only edit changes the page, not its source: no derivation is thrown away.
	if _, err := (usecases.CaptureSource{
		Store: store, Index: index, Extractor: extract.New(gateways.ExecRunner{}, extract.Config{}),
		Steps: steps, Clock: wikiClock{},
	}).Execute(ctx, second.Hash); err != nil {
		t.Fatalf("capture after edit: %v", err)
	}
	if _, err := save.Execute(ctx, usecases.WikiPageInput{
		ID: first.Page.ID, Title: "Go Concurrency", Tags: []string{"go"},
		Body: "# Channels\n\nA changed paragraph.",
	}); err != nil {
		t.Fatalf("tag-only save: %v", err)
	}
	rec, _ = steps.Steps(second.Hash)
	if rec.State(entities.StepCapture) != entities.StepOK {
		t.Fatalf("tag-only edit re-armed capture: %q", rec.State(entities.StepCapture))
	}
}

// TestSaveWikiPageQueuesLinksWithoutFetchingThem: saving a page with links is a LOCAL write. The save
// only reports which links are new (the caller queues the fetches), so a page with a hundred links
// costs the same as a page with one — and nothing in the request touches the network.
func TestSaveWikiPageQueuesLinksWithoutFetchingThem(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourcesDir := filepath.Join(root, "sources")
	wikiDir := filepath.Join(root, "wiki")
	db, err := persistence.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := objects.New(sourcesDir)
	index := sqlc.NewSourceRepository(db)
	atomRepo := sqlc.NewAtomRepository(db)
	atomStore := atoms.New(filepath.Join(root, "atoms"))
	steps := sourcemeta.NewTracker(sourcemeta.New(sourcesDir), index, nil)
	deleter := usecases.DeleteSource{Store: store, Index: index, Atoms: atomStore, AtomRepo: atomRepo, Steps: steps, Clock: wikiClock{}}
	save := usecases.SaveWikiPage{
		Pages: wiki.New(wikiDir), Store: store, Index: index, Steps: steps,
		Delete: deleter, Clock: wikiClock{},
	}
	queue := usecases.QueueURLs{Index: index, Steps: steps, Clock: wikiClock{}}

	const link = "https://example.com/a"
	first, err := save.Execute(ctx, usecases.WikiPageInput{
		Title: "Links", Body: "See [the source](" + link + ").",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if len(first.URLsAdded) != 1 || first.URLsAdded[0] != link {
		t.Fatalf("URLsAdded = %v, want the one new link", first.URLsAdded)
	}
	// The save itself writes NO source for the link: that is the caller's (background) job.
	if _, err := index.GetSourceByHash(ctx, entities.URLHash(link)); err == nil {
		t.Fatal("the save fetched the link instead of queueing it")
	}

	// Queueing claims the link: a row and a record exist before anything is fetched, so the reader can
	// see it and a second save does not queue it again.
	queued, err := queue.Execute(ctx, first.Page.ID.String(), first.URLsAdded)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(queued) != 1 || queued[0] != link {
		t.Fatalf("queued = %v, want the new link", queued)
	}
	urlHash := entities.URLHash(link)
	urlSrc, err := index.GetSourceByHash(ctx, urlHash)
	if err != nil {
		t.Fatalf("url source: %v", err)
	}
	if urlSrc.Origin != entities.WikiLinkOrigin || urlSrc.WikiPage != first.Page.ID.String() {
		t.Fatalf("url origin = %q/%q, want wiki/%s", urlSrc.Origin, urlSrc.WikiPage, first.Page.ID)
	}
	rec, err := steps.Steps(urlHash)
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	if rec.State(entities.StepCapture) != entities.StepPending {
		t.Fatalf("capture = %q, want pending for a queued link", rec.State(entities.StepCapture))
	}
	// Nothing after the fetch may run on a source that has no text yet.
	for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill} {
		if rec.State(step) != entities.StepSkipped {
			t.Fatalf("%s = %q, want skipped for a queued link", step, rec.State(step))
		}
	}

	// A prose edit keeps the URL: it is known, so it is not queued again.
	second, err := save.Execute(ctx, usecases.WikiPageInput{
		ID: first.Page.ID, Title: "Links", Body: "Changed prose. See [the source](" + link + ").",
	})
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if second.Created || len(second.URLsAdded) != 0 {
		t.Fatalf("a known link was queued again: %v", second.URLsAdded)
	}

	// Removing the URL removes the page-owned source.
	third, err := save.Execute(ctx, usecases.WikiPageInput{
		ID: first.Page.ID, Title: "Links", Body: "No links any more.",
	})
	if err != nil {
		t.Fatalf("third save: %v", err)
	}
	if len(third.URLHashesRemoved) != 1 || third.URLHashesRemoved[0] != urlHash {
		t.Fatalf("URLHashesRemoved = %v, want the link's source", third.URLHashesRemoved)
	}
	if _, err := index.GetSourceByHash(ctx, urlHash); err == nil {
		t.Fatal("URL source survived its removal from the page")
	}
}

// TestQueueURLsLeavesKnownLinksAlone: a link that already has a source (a failed one included) is only
// associated. Refetching it on every save would make saving a dead-link page slow again.
func TestQueueURLsLeavesKnownLinksAlone(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourcesDir := filepath.Join(root, "sources")
	db, err := persistence.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	index := sqlc.NewSourceRepository(db)
	steps := sourcemeta.NewTracker(sourcemeta.New(sourcesDir), index, nil)
	queue := usecases.QueueURLs{Index: index, Steps: steps, Clock: wikiClock{}}

	const link = "https://example.com/dead"
	if _, err := queue.Execute(ctx, "page-1", []string{link, link}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	again, err := queue.Execute(ctx, "page-1", []string{link})
	if err != nil {
		t.Fatalf("second queue: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("a known link was queued twice: %v", again)
	}
}

// TestMarkURLFailedKeepsTheLinkVisible: a dead link is part of what the page claims. It stays listed
// with a red capture light and the reason, instead of vanishing with a log line.
func TestMarkURLFailedKeepsTheLinkVisible(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourcesDir := filepath.Join(root, "sources")
	db, err := persistence.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	index := sqlc.NewSourceRepository(db)
	steps := sourcemeta.NewTracker(sourcemeta.New(sourcesDir), index, nil)
	queue := usecases.QueueURLs{Index: index, Steps: steps, Clock: wikiClock{}}
	mark := usecases.MarkURLFailed{Index: index, Steps: steps, Clock: wikiClock{}}

	const link = "https://example.com/gone"
	if _, err := queue.Execute(ctx, "page-1", []string{link}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	hash, err := mark.Execute(ctx, link, "page-1", errors.New("dial tcp: connection refused"))
	if err != nil {
		t.Fatalf("mark: %v", err)
	}
	src, err := index.GetSourceByHash(ctx, hash)
	if err != nil {
		t.Fatalf("the failed link left the list: %v", err)
	}
	if src.Title != link {
		t.Fatalf("title = %q, want the URL while nothing was fetched", src.Title)
	}
	if src.Path != "" {
		t.Fatalf("path = %q, want no object for a link that never answered", src.Path)
	}
	rec, err := steps.Steps(hash)
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	capture := rec.Get(entities.StepCapture)
	if capture.State != entities.StepFailed {
		t.Fatalf("capture = %q, want failed", capture.State)
	}
	if !strings.Contains(capture.Error, "connection refused") {
		t.Fatalf("capture error = %q, want the reason", capture.Error)
	}
}

// TestAddURLFillsAQueuedLink: the fetch completes the row that was claimed for the link — it does not
// file a second source — and it re-arms the derivation that the queued row had switched off.
func TestAddURLFillsAQueuedLink(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourcesDir := filepath.Join(root, "sources")
	db, err := persistence.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := objects.New(sourcesDir)
	index := sqlc.NewSourceRepository(db)
	steps := sourcemeta.NewTracker(sourcemeta.New(sourcesDir), index, nil)
	queue := usecases.QueueURLs{Index: index, Steps: steps, Clock: wikiClock{}}
	const link = "https://example.com/page"
	if _, err := queue.Execute(ctx, "page-1", []string{link}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	before, err := index.GetSourceByHash(ctx, entities.URLHash(link))
	if err != nil {
		t.Fatalf("queued row: %v", err)
	}

	add := usecases.AddURL{
		Fetch:   &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("<html>real</html>")}},
		Convert: &fakeConverter{title: "Real title", markdown: "## Section\n\n" + strings.Repeat("word ", 120)},
		Store:   store, Index: index, Steps: steps, Clock: wikiClock{},
	}
	src, err := add.Execute(ctx, link, false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if src.Hash != before.Hash {
		t.Fatalf("hash = %s, want the queued address %s", src.Hash, before.Hash)
	}
	after, err := index.GetSourceByHash(ctx, before.Hash)
	if err != nil {
		t.Fatalf("filled row: %v", err)
	}
	if after.Title != "Real title" {
		t.Fatalf("title = %q, want the document's own title", after.Title)
	}
	if after.Origin != entities.WikiLinkOrigin || after.WikiPage != "page-1" {
		t.Fatalf("origin = %q/%q, want the page kept", after.Origin, after.WikiPage)
	}
	rec, err := steps.Steps(before.Hash)
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	if rec.State(entities.StepCapture) != entities.StepOK {
		t.Fatalf("capture = %q, want ok", rec.State(entities.StepCapture))
	}
	// The queued row had outline/distill switched off; the arrived page must be derived.
	for _, step := range []entities.Step{entities.StepOutline, entities.StepDistill} {
		if rec.State(step) == entities.StepSkipped {
			t.Fatalf("%s still skipped after the page arrived", step)
		}
	}
}

// TestReindexKeepsALinkThatWasNeverFetched: a queued (or failed) link has a record and NO object. A
// reindex rebuilds the index from the vault, so if it walked objects only, the link would vanish from
// the list on the next rebuild — exactly the loss the row exists to prevent.
func TestReindexKeepsALinkThatWasNeverFetched(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourcesDir := filepath.Join(root, "sources")
	db, err := persistence.Open(filepath.Join(root, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := objects.New(sourcesDir)
	index := sqlc.NewSourceRepository(db)
	steps := sourcemeta.NewTracker(sourcemeta.New(sourcesDir), index, nil)
	queue := usecases.QueueURLs{Index: index, Steps: steps, Clock: wikiClock{}}

	const link = "https://example.com/never-answered"
	if _, err := queue.Execute(ctx, "page-1", []string{link}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	// An empty object store: the link is the only thing in the vault, and it has no bytes.
	if _, err := (usecases.ReindexVault{Store: store, Index: index, Steps: steps, Clock: wikiClock{}}).Execute(ctx); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	src, err := index.GetSourceByHash(ctx, entities.URLHash(link))
	if err != nil {
		t.Fatalf("the reindex dropped a link that was never fetched: %v", err)
	}
	if src.Origin != entities.WikiLinkOrigin || src.URL != link {
		t.Fatalf("restored row = %+v, want the page's link", src)
	}
	// Reconcile must not cry wolf about a source that has no object by design.
	res, err := (usecases.ReconcileVault{Index: index, Steps: steps, Store: store, Clock: wikiClock{}}).Execute(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Broken) != 0 {
		t.Fatalf("broken = %v, want none: the record says why there is no object", res.Broken)
	}
}
