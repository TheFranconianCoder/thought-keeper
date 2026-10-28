package usecases_test

import (
	"context"
	"io"
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

const feedWithCopy = `<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">
<channel>
  <title>The Feed</title>
  <item><title>One</title><link>https://example.com/one</link>
    <content:encoded><![CDATA[<p>The whole post lives in the feed.</p>]]></content:encoded></item>
  <item><title>Two</title><link>https://example.com/two</link></item>
</channel></rss>`

// TestFeedCopyMakesAJavaScriptPageReadable: a feed that carries the post solves the page that only
// resolves with JavaScript. The item's copy becomes the page's snapshot, marked with where it came
// from, and the page is NOT fetched — the fetch would only return the shell.
func TestFeedCopyMakesAJavaScriptPageReadable(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	steps := &fakeSteps{index: idx}
	uc := usecases.AddURL{
		Fetch:        &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte(feedWithCopy), ContentType: "application/rss+xml"}},
		Convert:      &fakeConverter{title: "Copied", markdown: strings.Repeat("article body ", 60)},
		Store:        store,
		Index:        idx,
		Steps:        steps,
		Clock:        fixedClock{},
		MinTextChars: 40,
	}
	src, err := uc.ExecuteManual(ctx, "https://example.com/feed.xml", false)
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	if src.RSSURLs == nil || len(src.RSSURLs) != 1 || src.RSSURLs[0] != "https://example.com/two" {
		t.Fatalf("to fetch = %v, want only the item without a copy", src.RSSURLs)
	}

	oneHash := entities.URLHash("https://example.com/one")
	one, ok := idx.m[oneHash]
	if !ok {
		t.Fatal("the content-backed item was not claimed")
	}
	if !one.FromFeed || one.Bookmark || one.Path == "" {
		t.Errorf("the copy was not stored as a readable snapshot: %+v", one)
	}
	if one.FeedHash != src.Hash {
		t.Errorf("feed hash = %q, want %q", one.FeedHash, src.Hash)
	}
	if id := steps.identities[oneHash]; !id.FromFeed || id.FeedHash != src.Hash {
		t.Errorf("the record lost the feed marker: %+v", id)
	}
	rc, err := store.Open(oneHash, ".md")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer func() { _ = rc.Close() }()
	b, _ := io.ReadAll(rc)
	snapshot := string(b)
	for _, want := range []string{"from_feed: true", "feed_hash: " + src.Hash, "article body"} {
		if !strings.Contains(snapshot, want) {
			t.Errorf("snapshot lacks %q:\n%s", want, snapshot)
		}
	}
}

// TestFeedRefreshFillsAnExistingBookmark: pages that stayed bookmarks BEFORE the feed copy existed are
// filled in place by the next expansion. The URL keeps its address; only its snapshot becomes readable.
func TestFeedRefreshFillsAnExistingBookmark(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	steps := &fakeSteps{index: idx}
	feedHash := entities.URLHash("https://example.com/feed.xml")
	idx.m[feedHash] = entities.Source{
		ID: ulid.Make(), Hash: feedHash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/aa/bb/" + feedHash + ".md", URL: "https://example.com/feed.xml",
		Title: "The Feed", RSS: true, ImportedAt: fixedClock{}.Now(),
		Meta: `{"url":"https://example.com/feed.xml","rss":"true"}`,
	}
	oneHash := entities.URLHash("https://example.com/one")
	idx.m[oneHash] = entities.Source{
		ID: ulid.Make(), Hash: oneHash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/cc/dd/" + oneHash + ".md", URL: "https://example.com/one",
		Title: "Codingwithroby", Bookmark: true, Origin: entities.RSSOrigin, ParentHash: feedHash,
		URLChecked: fixedClock{}.Now(), ImportedAt: fixedClock{}.Now(),
		Meta: `{"url":"https://example.com/one","bookmark":true,"origin":"rss","parent_hash":"` + feedHash + `"}`,
	}

	refresh := usecases.RefreshURLs{
		Fetch:        &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte(feedWithCopy), ContentType: "application/rss+xml"}},
		Convert:      &fakeConverter{title: "Copied", markdown: strings.Repeat("article body ", 60)},
		Store:        store,
		Index:        idx,
		Steps:        steps,
		Clock:        fixedClock{},
		MinAge:       time.Hour,
		MinTextChars: 40,
	}
	if _, err := refresh.ExecuteAll(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	one := idx.m[oneHash]
	if one.Bookmark || !one.FromFeed {
		t.Errorf("the bookmark was not filled from the feed: %+v", one)
	}
	if one.ParentHash != feedHash || one.Origin != entities.RSSOrigin {
		t.Errorf("filling the copy lost the ownership: %+v", one)
	}
	rc, err := store.Open(oneHash, ".md")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer func() { _ = rc.Close() }()
	b, _ := io.ReadAll(rc)
	if !strings.Contains(string(b), "from_feed: true") {
		t.Errorf("the snapshot does not say where it came from:\n%s", b)
	}
}

// TestRefreshKeepsAFeedCopyWhenThePageStillDoesNotRead: the weekly pass asks the page again. While the
// page yields no readable text, the feed's copy stays — the pass must not replace an article with a
// JavaScript shell (or a wall).
func TestRefreshKeepsAFeedCopyWhenThePageStillDoesNotRead(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	hash := entities.URLHash("https://example.com/one")
	feedHash := entities.URLHash("https://example.com/feed.xml")
	idx.m[hash] = entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/cc/dd/" + hash + ".md", URL: "https://example.com/one",
		Title: "One", FromFeed: true, FeedHash: feedHash, Origin: entities.RSSOrigin, ParentHash: feedHash,
		ImportedAt: fixedClock{}.Now(),
		Meta:       `{"url":"https://example.com/one","from_feed":true,"feed_hash":"` + feedHash + `"}`,
	}
	if _, err := store.PutAt(hash, ".md", []byte("the readable copy from the feed\n")); err != nil {
		t.Fatal(err)
	}
	refresh := usecases.RefreshURLs{
		Fetch:        &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("<html>enable js</html>"), ContentType: "text/html"}},
		Convert:      &fakeConverter{markdown: ""},
		Store:        store,
		Index:        idx,
		Clock:        fixedClock{},
		MinTextChars: 40,
	}
	res, err := refresh.ExecuteAll(ctx)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Failed != 0 {
		t.Errorf("an unreadable page is not a failure when a feed copy exists: %+v", res)
	}
	rc, err := store.Open(hash, ".md")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer func() { _ = rc.Close() }()
	b, _ := io.ReadAll(rc)
	if !strings.Contains(string(b), "the readable copy from the feed") {
		t.Errorf("the feed copy was overwritten by the unreadable page:\n%s", b)
	}
}

// TestDeletingAFeedSurvivesACanceledRequest: the human confirmed the delete; a browser that gives up
// must not leave a half-deleted feed behind. The old code used the request context for the cascade and
// stopped at the first "context canceled", leaving the children deleted and the feed alive.
func TestDeletingAFeedSurvivesACanceledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	feedHash := entities.URLHash("https://example.com/feed.xml")
	idx.m[feedHash] = entities.Source{
		ID: ulid.Make(), Hash: feedHash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/aa/bb/" + feedHash + ".md", URL: "https://example.com/feed.xml",
		Title: "The Feed", RSS: true, ImportedAt: fixedClock{}.Now(), Meta: `{"url":"https://example.com/feed.xml"}`,
	}
	for _, raw := range []string{"https://example.com/one", "https://example.com/two"} {
		hash := entities.URLHash(raw)
		idx.m[hash] = entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
			URL: raw, Title: raw, Origin: entities.RSSOrigin, ParentHash: feedHash,
			ImportedAt: fixedClock{}.Now(), Meta: `{"url":"` + raw + `"}`,
		}
	}
	cancel() // the request is gone before the cascade starts

	del := usecases.DeleteSource{
		Store: store, Index: idx, Atoms: atomstore.New(t.TempDir()), AtomRepo: &fakeAtomRepo{},
		Clock: fixedClock{},
	}
	if _, err := del.ExecuteOwned(ctx, feedHash); err != nil {
		t.Fatalf("delete with a canceled request: %v", err)
	}
	if len(idx.m) != 0 {
		t.Errorf("the feed or its items survived: %d rows left", len(idx.m))
	}
}
