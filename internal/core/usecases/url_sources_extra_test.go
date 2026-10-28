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
	atomstore "github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/atoms"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

const testFeed = `<?xml version="1.0"?>
<rss version="2.0"><channel>
  <title>The Feed</title>
  <item><title>One</title><link>https://example.com/one</link></item>
  <item><title>Two</title><link>https://example.com/two</link></item>
</channel></rss>`

const testSitemap = `<?xml version="1.0"?>
<urlset><url><loc>https://example.com/one</loc></url><url><loc>https://example.com/two</loc></url></urlset>`

// TestQueueURLsIgnoresATrailingSlash: "/article" and "/article/" address one page. A wiki page that
// renames its link by a slash must not claim a second source (and must not delete the first).
func TestQueueURLsIgnoresATrailingSlash(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	steps := &fakeSteps{}
	queue := usecases.QueueURLs{Index: idx, Steps: steps, Clock: fixedClock{}}

	first, err := queue.Execute(ctx, "01M5JAHSRBDSX83F8V6CR3DRH3", []string{"https://example.com/article"})
	if err != nil || len(first) != 1 {
		t.Fatalf("first queue = %v, %v", first, err)
	}
	second, err := queue.Execute(ctx, "01M5JAHSRBDSX83F8V6CR3DRH3", []string{"https://example.com/article/"})
	if err != nil {
		t.Fatalf("second queue: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("the trailing-slash variant was queued again: %v", second)
	}
	if len(idx.m) != 1 {
		t.Errorf("the vault has %d sources for one page", len(idx.m))
	}

	// A fetch under the other spelling fills the source that exists, it does not file a second one.
	add := usecases.AddURL{
		Fetch:   &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("<html>x</html>")}},
		Convert: &fakeConverter{title: "The Article", markdown: strings.Repeat("text ", 100)},
		Store:   objects.New(t.TempDir()), Index: idx, Steps: steps, Clock: fixedClock{},
	}
	src, err := add.Execute(ctx, "https://example.com/article/", false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if src.Hash != entities.URLHash("https://example.com/article") {
		t.Errorf("the fetch filed a new address: %s", src.Hash)
	}
	if len(idx.m) != 1 {
		t.Errorf("the vault has %d sources for one page after the fetch", len(idx.m))
	}
}

// TestManualAdditionWinsOverAnOwner: a URL a wiki page brought in, then added by hand, becomes an
// INDEPENDENT source. Deleting the page must not delete what the human collected themselves; the
// source's snapshot and claims stay untouched.
func TestManualAdditionWinsOverAnOwner(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	steps := &fakeSteps{index: idx}
	queue := usecases.QueueURLs{Index: idx, Steps: steps, Clock: fixedClock{}}
	const pageID = "01M5JAHSRBDSX83F8V6CR3DRH3"
	const raw = "https://example.com/linked"
	if _, err := queue.Execute(ctx, pageID, []string{raw}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	add := usecases.AddURL{
		Fetch:   &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("<html>x</html>")}},
		Convert: &fakeConverter{title: "Linked", markdown: strings.Repeat("text ", 100)},
		Store:   objects.New(t.TempDir()), Index: idx, Steps: steps, Clock: fixedClock{},
	}

	// The queue path (a page's link) keeps the owner.
	src, err := add.Execute(ctx, raw, false)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if idx.m[src.Hash].Origin != entities.WikiLinkOrigin {
		t.Fatalf("the first fetch lost the owner: %q", idx.m[src.Hash].Origin)
	}

	// The human's own addition releases it.
	if _, err := add.ExecuteManual(ctx, raw, false); err != nil {
		t.Fatalf("manual add: %v", err)
	}
	row := idx.m[src.Hash]
	if row.Origin != "" || row.WikiPage != "" || row.ParentHash != "" {
		t.Errorf("the manual source is still owned: %q/%q/%q", row.Origin, row.WikiPage, row.ParentHash)
	}
	if strings.Contains(row.Meta, `"origin"`) {
		t.Errorf("the row's metadata still names an owner: %s", row.Meta)
	}
	if id := steps.identities[src.Hash]; id.Origin != "" || id.WikiPage != "" {
		t.Errorf("the record still names an owner: %+v", id)
	}
}

// TestFeedIsASourceOfSources: an RSS feed is stored as a parent source whose snapshot is the item
// list, and its items are returned for the caller to claim and fetch.
func TestFeedIsASourceOfSources(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	steps := &fakeSteps{}
	uc := usecases.AddURL{
		Fetch:   &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte(testFeed), ContentType: "application/rss+xml"}},
		Store:   objects.New(t.TempDir()),
		Index:   idx,
		Steps:   steps,
		Clock:   fixedClock{},
		Convert: &fakeConverter{},
	}
	src, err := uc.ExecuteManual(ctx, "https://example.com/feed.xml", false)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if !src.RSS {
		t.Errorf("the source is not marked as a feed: %+v", src)
	}
	if src.Title != "The Feed" {
		t.Errorf("title = %q, want the feed's own", src.Title)
	}
	if len(src.RSSURLs) != 2 || src.RSSURLs[0] != "https://example.com/one" {
		t.Errorf("items = %v", src.RSSURLs)
	}
	if got := lastState(steps, entities.StepOutline); got != entities.StepSkipped {
		t.Errorf("outline = %q, want skipped for a feed", got)
	}
	if !idx.m[src.Hash].RSS {
		t.Errorf("the feed marker is not on the row: %+v", idx.m[src.Hash])
	}
	// The snapshot is the item list, and it is what the reader sees.
	rc, err := uc.Store.Open(src.Hash, ".md")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer func() { _ = rc.Close() }()
	buf := make([]byte, 4096)
	n, _ := rc.Read(buf)
	if !strings.Contains(string(buf[:n]), "https://example.com/one") {
		t.Errorf("the feed snapshot does not list its items:\n%s", buf[:n])
	}
}

// TestRefreshKeepsPagesThatLeftAFeed: a feed is a window on the newest items, not an inventory. A page
// that disappears from it stays in the vault; only deleting the feed removes it.
func TestRefreshKeepsPagesThatLeftAFeed(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	steps := &fakeSteps{}
	feedHash := entities.URLHash("https://example.com/feed.xml")
	idx.m[feedHash] = entities.Source{
		ID: ulid.Make(), Hash: feedHash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/aa/bb/" + feedHash + ".md", URL: "https://example.com/feed.xml",
		Title: "The Feed", RSS: true, ImportedAt: fixedClock{}.Now(),
		Meta: `{"url":"https://example.com/feed.xml"}`,
	}
	for _, raw := range []string{"https://example.com/one", "https://example.com/two"} {
		hash := entities.URLHash(raw)
		idx.m[hash] = entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
			URL: raw, Title: raw, Origin: entities.RSSOrigin, ParentHash: feedHash,
			ImportedAt: fixedClock{}.Now(), Meta: `{"url":"` + raw + `"}`,
		}
	}
	// The new feed lists ONLY "one" (and a new "three").
	feed := `<rss><channel><title>The Feed</title>
	 <item><title>One</title><link>https://example.com/one</link></item>
	 <item><title>Three</title><link>https://example.com/three</link></item>
	</channel></rss>`
	refresh := usecases.RefreshURLs{
		Fetch:   &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte(feed), ContentType: "application/rss+xml"}},
		Convert: &fakeConverter{}, MinAge: time.Hour,
		Store: store, Index: idx, Steps: steps, Clock: fixedClock{},
	}
	res, err := refresh.ExecuteAll(ctx)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Removed != 0 {
		t.Errorf("removed = %d, a feed never deletes", res.Removed)
	}
	for _, raw := range []string{"https://example.com/one", "https://example.com/two"} {
		if _, ok := idx.m[entities.URLHash(raw)]; !ok {
			t.Errorf("%s was deleted; a page that left a feed must stay", raw)
		}
	}
	if _, ok := idx.m[entities.URLHash("https://example.com/three")]; !ok {
		t.Error("the new item was not claimed")
	}
	if len(res.Discovered) != 1 || res.Discovered[0].Origin != entities.RSSOrigin || len(res.Discovered[0].URLs) != 1 {
		t.Errorf("discovered = %+v, want the new item as a feed child", res.Discovered)
	}
	// A refresh re-records the list's skipped document steps: the record must stay the truth even
	// when an older one lost them.
	if got := lastState(steps, entities.StepOutline); got != entities.StepSkipped {
		t.Errorf("outline after refresh = %q, want skipped", got)
	}
	if got := lastState(steps, entities.StepDistill); got != entities.StepSkipped {
		t.Errorf("distill after refresh = %q, want skipped", got)
	}

	// The per-source Refresh ignores the cadence and refreshes exactly one source.
	one, err := refresh.ExecuteOne(ctx, feedHash)
	if err != nil {
		t.Fatalf("refresh one: %v", err)
	}
	if one.Checked != 1 || len(one.Sources) != 1 {
		t.Errorf("refresh one = %+v, want exactly the feed checked", one)
	}
}

// TestRefreshDeletesPagesThatLeftASitemap: a sitemap claims to be complete, so a page that left it is
// removed from the vault by the refresh (1:1), with its claims.
func TestRefreshDeletesPagesThatLeftASitemap(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	sitemapHash := entities.URLHash("https://example.com/sitemap.xml")
	idx.m[sitemapHash] = entities.Source{
		ID: ulid.Make(), Hash: sitemapHash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/aa/bb/" + sitemapHash + ".md", URL: "https://example.com/sitemap.xml",
		Title: "Sitemap", Sitemap: true, ImportedAt: fixedClock{}.Now(),
		Meta: `{"url":"https://example.com/sitemap.xml"}`,
	}
	for _, raw := range []string{"https://example.com/one", "https://example.com/two"} {
		hash := entities.URLHash(raw)
		idx.m[hash] = entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
			Path: "sources/cc/dd/" + hash + ".md",
			URL:  raw, Title: raw, Origin: entities.SitemapOrigin, ParentHash: sitemapHash,
			ImportedAt: fixedClock{}.Now(), URLChecked: fixedClock{}.Now(), Meta: `{"url":"` + raw + `"}`,
		}
	}
	// A page the human added by hand: released from the sitemap, so the 1:1 reconciliation must not
	// touch it even though the sitemap does not list it any more.
	manualURL := "https://example.com/collected"
	manualHash := entities.URLHash(manualURL)
	idx.m[manualHash] = entities.Source{
		ID: ulid.Make(), Hash: manualHash, Kind: entities.SourceKindURL, Ext: ".md",
		URL: manualURL, Title: "Collected", ImportedAt: fixedClock{}.Now(),
		URLChecked: fixedClock{}.Now(), Meta: `{"url":"` + manualURL + `"}`,
	}
	sitemap := `<urlset><url><loc>https://example.com/one</loc></url></urlset>`
	refresh := usecases.RefreshURLs{
		Fetch:   &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte(sitemap), ContentType: "application/xml"}},
		Convert: &fakeConverter{}, MinAge: time.Hour,
		Store: store, Index: idx, Clock: fixedClock{},
		Delete: usecases.DeleteSource{
			Store: store, Index: idx, Atoms: atomstore.New(t.TempDir()), AtomRepo: &fakeAtomRepo{},
			Clock: fixedClock{},
		},
	}
	res, err := refresh.Execute(ctx)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Removed != 1 {
		t.Fatalf("removed = %d, want the page that left the sitemap", res.Removed)
	}
	if _, ok := idx.m[entities.URLHash("https://example.com/two")]; ok {
		t.Error("the page that left the sitemap is still in the vault")
	}
	if _, ok := idx.m[entities.URLHash("https://example.com/one")]; !ok {
		t.Error("the page the sitemap still lists was deleted")
	}
	if _, ok := idx.m[manualHash]; !ok {
		t.Error("a manually added page was deleted by the sitemap's reconciliation")
	}
}

// TestDeletingAFeedDeletesItsItems: the only way a feed's pages ever leave is deleting the feed. That
// is what makes "keep what disappeared from the feed" safe.
func TestDeletingAFeedDeletesItsItems(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	feedHash := entities.URLHash("https://example.com/feed.xml")
	idx.m[feedHash] = entities.Source{
		ID: ulid.Make(), Hash: feedHash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/aa/bb/" + feedHash + ".md", URL: "https://example.com/feed.xml",
		Title: "The Feed", RSS: true, ImportedAt: fixedClock{}.Now(), Meta: `{"url":"https://example.com/feed.xml"}`,
	}
	children := []string{}
	for _, raw := range []string{"https://example.com/one", "https://example.com/two"} {
		hash := entities.URLHash(raw)
		children = append(children, hash)
		idx.m[hash] = entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
			Path: "sources/cc/dd/" + hash + ".md",
			URL:  raw, Title: raw, Origin: entities.RSSOrigin, ParentHash: feedHash,
			ImportedAt: fixedClock{}.Now(), Meta: `{"url":"` + raw + `"}`,
		}
	}
	del := usecases.DeleteSource{
		Store: store, Index: idx, Atoms: atomstore.New(t.TempDir()), AtomRepo: &fakeAtomRepo{},
		Clock: fixedClock{},
	}
	res, err := del.Execute(ctx, feedHash)
	if err != nil {
		t.Fatalf("delete feed: %v", err)
	}
	if res.DependentsRemoved != 2 {
		t.Errorf("dependents removed = %d, want the two items", res.DependentsRemoved)
	}
	for _, hash := range children {
		if _, ok := idx.m[hash]; ok {
			t.Errorf("item %s survived the feed's deletion", hash[:8])
		}
	}
	if _, ok := idx.m[feedHash]; ok {
		t.Error("the feed itself survived")
	}
}
