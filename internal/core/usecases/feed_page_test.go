package usecases_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

const feedWithCopyItem = `<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">
<channel><title>The Feed</title>
  <item><title>One</title><link>https://example.com/one</link>
    <content:encoded><![CDATA[<p>feed-copy</p>]]></content:encoded></item>
</channel></rss>`

// routeFetcher serves one response per URL. The favicon fetch (a .png) is answered 404 so the
// converter's own favicon is not the point of these tests.
type routeFetcher map[string]interfaces.FetchedPage

func (f routeFetcher) Fetch(_ context.Context, rawURL string) (interfaces.FetchedPage, error) {
	if page, ok := f[rawURL]; ok {
		return page, nil
	}
	if strings.HasSuffix(rawURL, ".png") {
		return interfaces.FetchedPage{Status: 404}, nil
	}
	return interfaces.FetchedPage{}, errors.New("unexpected url " + rawURL)
}

// feedPageConverter returns the copy's text for the feed item and the page's text for the page.
type feedPageConverter struct {
	copyTitle, copyBody string
	pageTitle, pageBody string
}

func (c feedPageConverter) Convert(_ context.Context, content []byte, _ string) (interfaces.ConvertedPage, error) {
	if strings.Contains(string(content), "feed-copy") {
		return interfaces.ConvertedPage{Title: c.copyTitle, Markdown: c.copyBody}, nil
	}
	return interfaces.ConvertedPage{Title: c.pageTitle, Markdown: c.pageBody}, nil
}

type feedPageFixture struct {
	ctx     context.Context
	idx     *fakeIndex
	store   *objects.Store
	steps   *fakeSteps
	refresh usecases.RefreshURLs
	oneHash string
}

func newFeedPageFixture(t *testing.T, conv feedPageConverter) feedPageFixture {
	t.Helper()
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	steps := &fakeSteps{index: idx}
	feedURL := "https://example.com/feed.xml"
	oneURL := "https://example.com/one"
	page := interfaces.FetchedPage{Status: 200, Body: []byte("<html>page</html>"), ContentType: "text/html"}

	add := usecases.AddURL{
		Fetch: routeFetcher{
			feedURL: {Status: 200, Body: []byte(feedWithCopyItem), ContentType: "application/rss+xml"},
		},
		Convert: conv, Store: store, Index: idx, Steps: steps, Clock: fixedClock{}, MinTextChars: 40,
	}
	if _, err := add.ExecuteManual(ctx, feedURL, false); err != nil {
		t.Fatalf("feed: %v", err)
	}
	oneHash := entities.URLHash(oneURL)
	one, ok := idx.m[oneHash]
	if !ok || !one.FromFeed {
		t.Fatalf("the feed copy was not stored: %+v", one)
	}
	return feedPageFixture{
		ctx: ctx, idx: idx, store: store, steps: steps,
		refresh: usecases.RefreshURLs{
			Fetch: routeFetcher{oneURL: page}, Convert: conv,
			Store: store, Index: idx, Steps: steps, Clock: fixedClock{}, MinTextChars: 40,
		},
		oneHash: oneHash,
	}
}

func (f feedPageFixture) snapshot(t *testing.T) string {
	t.Helper()
	rc, err := f.store.Open(f.oneHash, ".md")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return string(b)
}

func lastCaptureDetail(steps *fakeSteps) string {
	seen := steps.seen[entities.StepCapture]
	if len(seen) == 0 {
		return ""
	}
	return seen[len(seen)-1].Detail
}

// TestFeedCopyIsProvisionalAndTheRicherPageReplacesIt: the copy keeps the source readable at intake,
// but the page is queued. When the page carries more text it wins, the marker goes, and the caller is
// told to rebuild the old derivation.
func TestFeedCopyIsProvisionalAndTheRicherPageReplacesIt(t *testing.T) {
	copyBody := strings.Repeat("copy text ", 20)
	pageBody := strings.Repeat("page text ", 40)
	f := newFeedPageFixture(t, feedPageConverter{
		copyTitle: "Copy", copyBody: copyBody,
		pageTitle: "Page", pageBody: pageBody,
	})

	total, ok := f.idx.m[f.oneHash]
	if !ok || !strings.Contains(total.Meta, `"text_chars"`) {
		t.Fatalf("the copy did not record its text length: %s", total.Meta)
	}

	res, err := f.refresh.ExecuteOne(f.ctx, f.oneHash)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Changed != 1 || len(res.Resets) != 1 || res.Resets[0].Hash != f.oneHash {
		t.Fatalf("refresh = %+v, want a changed page with a derivation reset", res)
	}
	one := f.idx.m[f.oneHash]
	if one.FromFeed || strings.Contains(one.Meta, `"from_feed"`) || strings.Contains(one.Meta, `"feed_hash"`) {
		t.Errorf("the page won but the feed marker survived: %+v", one)
	}
	body := f.snapshot(t)
	if !strings.Contains(body, "page text") || strings.Contains(body, "from_feed: true") {
		t.Errorf("the snapshot is not the page's:\n%s", body)
	}
	if got := lastCaptureDetail(f.steps); !strings.Contains(got, "page read") && !strings.Contains(got, "refetched") {
		t.Errorf("capture detail = %q, want the page read", got)
	}
}

// TestFeedCopyStaysWhenThePageIsPoorer: a readable page that carries less text must not replace the
// richer copy — the user's rule is that more information wins.
func TestFeedCopyStaysWhenThePageIsPoorer(t *testing.T) {
	f := newFeedPageFixture(t, feedPageConverter{
		copyTitle: "Copy", copyBody: strings.Repeat("copy text ", 20),
		pageTitle: "Page", pageBody: strings.Repeat("page text ", 10),
	})
	res, err := f.refresh.ExecuteOne(f.ctx, f.oneHash)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Changed != 0 || len(res.Resets) != 0 {
		t.Fatalf("refresh = %+v, want the copy kept", res)
	}
	one := f.idx.m[f.oneHash]
	if !one.FromFeed {
		t.Errorf("the feed marker was cleared although the copy stayed: %+v", one)
	}
	body := f.snapshot(t)
	if !strings.Contains(body, "copy text") || strings.Contains(body, "page text") {
		t.Errorf("the richer copy was overwritten:\n%s", body)
	}
	if got := lastCaptureDetail(f.steps); !strings.Contains(got, "carried less text") {
		t.Errorf("capture detail = %q, want the reason the copy stayed", got)
	}
}

// TestAddUrlDoesNotClobberAFeedCopyWithAShell: a page attempt on a source that already has a feed
// copy must keep the copy when the page is a JavaScript shell. The guard has to run BEFORE the
// snapshot write; otherwise the bookmark snapshot would land on the copy's address while the row
// still claimed to be a feed copy.
func TestAddUrlDoesNotClobberAFeedCopyWithAShell(t *testing.T) {
	copyBody := strings.Repeat("copy text ", 20)
	f := newFeedPageFixture(t, feedPageConverter{
		copyTitle: "Copy", copyBody: copyBody,
		pageTitle: "Shell", pageBody: "",
	})
	add := usecases.AddURL{
		Fetch: routeFetcher{
			"https://example.com/one": {Status: 200, Body: []byte("<html>page</html>"), ContentType: "text/html"},
		},
		Convert: feedPageConverter{
			copyTitle: "Copy", copyBody: copyBody,
			pageTitle: "Shell", pageBody: "",
		},
		Store: f.store, Index: f.idx, Steps: f.steps, Clock: fixedClock{}, MinTextChars: 40,
	}
	src, err := add.ExecuteManual(f.ctx, "https://example.com/one", false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !src.FromFeed || src.NeedsReset {
		t.Errorf("the copy was not kept as a copy: %+v", src)
	}
	body := f.snapshot(t)
	if !strings.Contains(body, "copy text") || strings.Contains(body, "from_feed: false") {
		t.Errorf("the shell overwrote the feed copy:\n%s", body)
	}
}

// TestAddUrlReportsAResetWhenItReplacesAFeedCopy: a manual add that actually reads a richer page does
// replace the copy; the caller must rebuild the old derivation.
func TestAddUrlReportsAResetWhenItReplacesAFeedCopy(t *testing.T) {
	f := newFeedPageFixture(t, feedPageConverter{
		copyTitle: "Copy", copyBody: strings.Repeat("copy text ", 20),
		pageTitle: "Page", pageBody: strings.Repeat("page text ", 40),
	})
	add := usecases.AddURL{
		Fetch: routeFetcher{
			"https://example.com/one": {Status: 200, Body: []byte("<html>page</html>"), ContentType: "text/html"},
		},
		Convert: feedPageConverter{
			copyTitle: "Copy", copyBody: strings.Repeat("copy text ", 20),
			pageTitle: "Page", pageBody: strings.Repeat("page text ", 40),
		},
		Store: f.store, Index: f.idx, Steps: f.steps, Clock: fixedClock{}, MinTextChars: 40,
	}
	src, err := add.ExecuteManual(f.ctx, "https://example.com/one", false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !src.NeedsReset {
		t.Errorf("a page that replaced the copy did not ask for a derivation reset: %+v", src)
	}
	if body := f.snapshot(t); !strings.Contains(body, "page text") {
		t.Errorf("the page did not replace the copy:\n%s", body)
	}
}

// TestFeedCopyMarkerIsClearedWhenThePageHasTheSameText: the page was read, even if it says exactly
// what the copy said. The provenance must change; the claims need no reset.
func TestFeedCopyMarkerIsClearedWhenThePageHasTheSameText(t *testing.T) {
	body := strings.Repeat("same text ", 20)
	f := newFeedPageFixture(t, feedPageConverter{
		copyTitle: "Copy", copyBody: body,
		pageTitle: "Copy", pageBody: body,
	})
	res, err := f.refresh.ExecuteOne(f.ctx, f.oneHash)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Changed != 0 || len(res.Resets) != 0 {
		t.Fatalf("refresh = %+v, want the same text and no reset", res)
	}
	one := f.idx.m[f.oneHash]
	if one.FromFeed || strings.Contains(one.Meta, `"from_feed"`) {
		t.Errorf("the page was read but the feed marker survived: %+v", one)
	}
	snapshot := f.snapshot(t)
	if strings.Contains(snapshot, "from_feed: true") || !strings.Contains(snapshot, "fetched_at:") {
		t.Errorf("the snapshot still claims to be the feed's:\n%s", snapshot)
	}
}
