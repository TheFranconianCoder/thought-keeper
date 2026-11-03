package usecases_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/gateways"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

// bodyConverter returns the HTML as the Markdown: the plain response and the rendered DOM are then
// two texts of different length, which is exactly the decision the use case has to take.
type bodyConverter struct{}

func (bodyConverter) Convert(_ context.Context, html []byte, _ string) (interfaces.ConvertedPage, error) {
	return interfaces.ConvertedPage{Title: "The Page", Markdown: string(html)}, nil
}

type fakeRenderer struct {
	page  interfaces.RenderedPage
	err   error
	calls int
}

func (r *fakeRenderer) Render(context.Context, string) (interfaces.RenderedPage, error) {
	r.calls++
	return r.page, r.err
}

func htmlRenderer(text string) *fakeRenderer {
	return &fakeRenderer{page: interfaces.RenderedPage{
		Status: 200, ContentType: "text/html; charset=utf-8",
		HTML: []byte(text), Engine: "lightpanda",
	}}
}

// TestAddReadsTheRenderedPageWhenItCarriesMoreText is the whole point of the browser: the plain
// response is a shell, the DOM after the scripts is the article, and the vault records which engine
// the text came from.
func TestAddReadsTheRenderedPageWhenItCarriesMoreText(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	renderer := htmlRenderer(strings.Repeat("rendered article text ", 60))
	uc := usecases.URLs{
		Fetch: &fakeFetcher{page: interfaces.FetchedPage{
			Status: 200, ContentType: "text/html; charset=utf-8", Body: []byte("shell"),
		}},
		Convert: bodyConverter{}, Render: renderer, Store: store, Index: idx, Clock: fixedClock{},
	}

	src, err := uc.Add(ctx, "https://example.com/app", false)
	if err != nil {
		t.Fatalf("add url: %v", err)
	}
	if renderer.calls != 1 {
		t.Fatalf("the page was rendered %d time(s), want 1", renderer.calls)
	}
	if src.Bookmark {
		t.Fatalf("a rendered article was stored as a bookmark")
	}
	snapshot := storedSnapshot(t, store, src.Hash)
	if !strings.Contains(snapshot, "rendered article text") {
		t.Errorf("snapshot does not carry the rendered text:\n%s", snapshot)
	}
	if !strings.Contains(snapshot, "rendered: lightpanda") {
		t.Errorf("snapshot does not name the engine:\n%s", snapshot)
	}
	if !strings.Contains(idx.m[src.Hash].Meta, `"rendered":"lightpanda"`) {
		t.Errorf("the record does not name the engine: %s", idx.m[src.Hash].Meta)
	}
}

// TestAddKeepsThePlainReadingWhenTheRenderLosesText: a rendered DOM can be SHORTER than the response
// (a site whose scripts rebuild the article). The richer text wins, and no render is claimed.
func TestAddKeepsThePlainReadingWhenTheRenderLosesText(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	plain := strings.Repeat("plain article text ", 60)
	renderer := htmlRenderer("stub")
	uc := usecases.URLs{
		Fetch: &fakeFetcher{page: interfaces.FetchedPage{
			Status: 200, ContentType: "text/html", Body: []byte(plain),
		}},
		Convert: bodyConverter{}, Render: renderer, Store: store, Index: idx, Clock: fixedClock{},
	}

	src, err := uc.Add(ctx, "https://example.com/article", false)
	if err != nil {
		t.Fatalf("add url: %v", err)
	}
	if renderer.calls != 1 {
		t.Fatalf("the page was rendered %d time(s), want 1", renderer.calls)
	}
	snapshot := storedSnapshot(t, store, src.Hash)
	if !strings.Contains(snapshot, "plain article text") || strings.Contains(snapshot, "rendered:") {
		t.Errorf("snapshot = %q, want the plain reading without a render claim", snapshot)
	}
	if strings.Contains(idx.m[src.Hash].Meta, `"rendered"`) {
		t.Errorf("the record claims a render that lost: %s", idx.m[src.Hash].Meta)
	}
}

// TestAddWithoutABrowserNeverRenders: no engine installed is the whole Windows story, and nothing
// about the plain read changes.
func TestAddWithoutABrowserNeverRenders(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	uc := usecases.URLs{
		Fetch: &fakeFetcher{page: interfaces.FetchedPage{
			Status: 200, ContentType: "text/html", Body: []byte(strings.Repeat("plain text ", 60)),
		}},
		Convert: bodyConverter{}, Store: store, Index: idx, Clock: fixedClock{},
	}

	src, err := uc.Add(ctx, "https://example.com/article", false)
	if err != nil {
		t.Fatalf("add url: %v", err)
	}
	if snapshot := storedSnapshot(t, store, src.Hash); strings.Contains(snapshot, "rendered:") {
		t.Errorf("snapshot claims a render without a browser:\n%s", snapshot)
	}
}

// TestRenderedWallIsRecordedWhenThePlainPageWasThin: a login form that only exists after hydration is
// a wall, not a thin page. The plain response hid it; the rendered DOM does not.
func TestRenderedWallIsRecordedWhenThePlainPageWasThin(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	renderer := &fakeRenderer{page: interfaces.RenderedPage{
		Status: 200, ContentType: "text/html", HTML: []byte("<html>login</html>"),
		Wall: entities.WallLogin, Engine: "lightpanda",
	}}
	uc := usecases.URLs{
		Fetch: &fakeFetcher{page: interfaces.FetchedPage{
			Status: 200, ContentType: "text/html", Body: []byte("short"),
		}},
		Convert: bodyConverter{}, Render: renderer, Store: store, Index: idx, Clock: fixedClock{},
	}

	src, err := uc.Add(ctx, "https://example.com/member", false)
	if err != nil {
		t.Fatalf("add url: %v", err)
	}
	snapshot := storedSnapshot(t, store, src.Hash)
	if !strings.Contains(snapshot, "wall: login_required") {
		t.Errorf("the rendered gate was not recorded as a wall:\n%s", snapshot)
	}
	if idx.m[src.Hash].Bookmark {
		t.Errorf("a walled page was filed as a bookmark")
	}
}

// TestListsAreNeverRendered: a sitemap's bytes are a list, not a page — a render would burn a browser
// on XML and could replace the list with an HTML wrapper. The same goes for a page the plain fetch
// already found walled: rendering a gate cannot make it readable.
func TestListsAndWallsAreNeverRendered(t *testing.T) {
	ctx := context.Background()
	sitemap := `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/one</loc></url>
</urlset>`
	renderer := htmlRenderer("should never appear")
	uc := usecases.URLs{
		Fetch: &fakeFetcher{page: interfaces.FetchedPage{
			Status: 200, ContentType: "application/xml", Body: []byte(sitemap),
		}},
		Convert: bodyConverter{}, Render: renderer, Store: objects.New(t.TempDir()),
		Index: newFakeIndex(), Clock: fixedClock{},
	}
	if _, err := uc.Add(ctx, "https://example.com/sitemap.xml", false); err != nil {
		t.Fatalf("add sitemap: %v", err)
	}

	walled := usecases.URLs{
		Fetch: &fakeFetcher{page: interfaces.FetchedPage{
			Status: 403, ContentType: "text/html", Body: []byte("sign in"), Wall: entities.WallLogin,
		}},
		Convert: bodyConverter{}, Render: renderer, Store: objects.New(t.TempDir()),
		Index: newFakeIndex(), Clock: fixedClock{},
	}
	if _, err := walled.Add(ctx, "https://example.com/member", false); err != nil {
		t.Fatalf("add walled page: %v", err)
	}
	if renderer.calls != 0 {
		t.Errorf("the browser was used %d time(s) on a list or a wall", renderer.calls)
	}
}

// TestRefetchTurnsACollectedLinkIntoAPage: a bookmark the reader collected before the browser was
// installed is read again on demand, and a readable page replaces the collected link.
func TestRefetchTurnsACollectedLinkIntoAPage(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	steps := &fakeSteps{}
	reset := &fakeResetter{}
	log := &fakeSourceLog{}
	rawURL := "https://example.com/share"
	hash := entities.URLHash(rawURL)
	idx.m[hash] = entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/aa/bb/share.md", URL: rawURL, Title: rawURL, OriginalName: rawURL,
		Bookmark: true, TreeState: entities.TreeNone, ImportedAt: fixedClock{}.Now(),
		Meta: `{"url":"` + rawURL + `","bookmark":true}`,
	}
	if _, err := store.PutAt(hash, ".md", []byte("---\nbookmark: true\n---\n\n> Collected as a link.")); err != nil {
		t.Fatalf("store bookmark: %v", err)
	}

	renderer := htmlRenderer(strings.Repeat("the shared conversation ", 60))
	refresh := usecases.URLs{
		Fetch: &fakeFetcher{page: interfaces.FetchedPage{
			Status: 200, ContentType: "text/html", Body: []byte("shell"),
		}},
		Convert: bodyConverter{}, Render: renderer, Store: store, Index: idx, Steps: steps,
		SourceLog: log, Reset: reset.Execute, Clock: fixedClock{},
	}

	res, err := refresh.RefreshOneForce(ctx, hash)
	if err != nil {
		t.Fatalf("refetch bookmark: %v", err)
	}
	if renderer.calls != 1 {
		t.Fatalf("the page was rendered %d time(s), want 1", renderer.calls)
	}
	if res.Replaced != 0 || res.Changed != 1 || res.Blocked != 0 {
		t.Fatalf("result = %+v, want the collected link replaced by a page", res)
	}
	if idx.m[hash].Bookmark {
		t.Errorf("the source is still a bookmark after a readable read")
	}
	if len(res.Resets) != 1 || res.Resets[0].Hash != hash {
		t.Fatalf("the replacement did not ask for a derivation reset: %+v", res.Resets)
	}
	if !strings.Contains(res.Resets[0].Reason, "collected link became a page") {
		t.Errorf("reset reason = %q", res.Resets[0].Reason)
	}
	snapshot := storedSnapshot(t, store, hash)
	if !strings.Contains(snapshot, "the shared conversation") || strings.Contains(snapshot, "bookmark: true") {
		t.Errorf("snapshot = %q, want the page without the bookmark marker", snapshot)
	}
	if !log.contains("the collected link became a page") {
		t.Errorf("the source log does not record the transition: %s", log.joined())
	}
}

// TestScriptedListingIsOffered: the cards of a JavaScript archive exist only in the rendered DOM.
// The plain response is a shell, the DOM after the scripts is the card grid, and the offer is made
// from the document the text came from.
func TestScriptedListingIsOffered(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	cardGrid := `<html><body><main><div class="grid">`
	for i := range 6 {
		cardGrid += fmt.Sprintf(
			`<div class="card"><h3><a href="/blog/post-%d">A scripted card title number %d</a></h3>
			 <div class="date">Oct %d, 2026</div></div>`, i, i, i+1)
	}
	cardGrid += `</div></main></body></html>`

	renderer := htmlRenderer(cardGrid)
	uc := usecases.URLs{
		Fetch: &fakeFetcher{page: interfaces.FetchedPage{
			Status: 200, ContentType: "text/html", Body: []byte("<html><body><div id=app></div></body></html>"),
		}},
		Convert: bodyConverter{}, Render: renderer, Links: gateways.HTMLLinkExtractor{},
		Store: store, Index: idx, Clock: fixedClock{}, MinTextChars: 400,
	}

	src, err := uc.AddManual(ctx, "https://example.com/blog/", false, usecases.ListingAuto)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if renderer.calls != 1 {
		t.Fatalf("the page was rendered %d time(s), want 1", renderer.calls)
	}
	if src.ListingCandidates != 6 {
		t.Fatalf("candidates = %d, want the 6 cards only the DOM declares", src.ListingCandidates)
	}
	if !src.Bookmark || src.Listing {
		t.Errorf("the offer must not reclassify the page: %+v", src)
	}
}

// TestListingWalkRendersAPageThatDeclaresNothing: an archive's page 2 is a shell too. The walk has
// to render it, or the archive stops at the first page a script builds.
func TestListingWalkRendersAPageThatDeclaresNothing(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	blogURL := "https://example.com/blog/"
	page2 := "https://example.com/blog/page/2"
	idx.m[entities.URLHash(blogURL)] = entities.Source{
		ID: ulid.Make(), Hash: entities.URLHash(blogURL), Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/aa/bb/blog.md", URL: blogURL, Title: "Blog", Listing: true,
		ImportedAt: fixedClock{}.Now(), Meta: `{"url":"` + blogURL + `"}`,
	}

	pageGrid := `<html><body><main><div class="grid">`
	for i := range 5 {
		pageGrid += fmt.Sprintf(
			`<div class="card"><h3><a href="/blog/old-%d">An older scripted title number %d</a></h3></div>`, i, i)
	}
	pageGrid += `</div><a rel="next" href="/blog/page/3">Next</a></main></body></html>`

	uc := usecases.URLs{
		Fetch: &fakeFetcher{page: interfaces.FetchedPage{
			Status: 200, ContentType: "text/html", Body: []byte("<html><body></body></html>"),
		}},
		Convert: bodyConverter{}, Render: htmlRenderer(pageGrid), Links: gateways.HTMLLinkExtractor{},
		Store: objects.New(t.TempDir()), Index: idx, Clock: fixedClock{}, MinTextChars: 400,
	}

	page, err := uc.ExpandListing(ctx, entities.URLHash(blogURL), page2)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(page.Items) != 5 {
		t.Fatalf("items = %d, want the 5 cards of the rendered page", len(page.Items))
	}
	if page.Next != "https://example.com/blog/page/3" {
		t.Fatalf("next = %q, want the rendered pager", page.Next)
	}
}

// TestRefetchKeepsACollectedLinkWhenThePageStaysThin: the browser is not a promise that every page is
// readable. A thin page leaves the collected link exactly as it was.
func TestRefetchKeepsACollectedLinkWhenThePageStaysThin(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	rawURL := "https://example.com/share"
	hash := entities.URLHash(rawURL)
	old := "---\nbookmark: true\n---\n\n> Collected as a link."
	idx.m[hash] = entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: "sources/aa/bb/share.md", URL: rawURL, Title: rawURL, OriginalName: rawURL,
		Bookmark: true, TreeState: entities.TreeNone, ImportedAt: fixedClock{}.Now(),
		Meta: `{"url":"` + rawURL + `","bookmark":true}`,
	}
	if _, err := store.PutAt(hash, ".md", []byte(old)); err != nil {
		t.Fatalf("store bookmark: %v", err)
	}

	refresh := usecases.URLs{
		Fetch: &fakeFetcher{page: interfaces.FetchedPage{
			Status: 200, ContentType: "text/html", Body: []byte("still a shell"),
		}},
		Convert: bodyConverter{}, Render: &fakeRenderer{page: interfaces.RenderedPage{
			Status: 200, ContentType: "text/html", HTML: []byte("still a shell"), Engine: "lightpanda",
		}},
		Store: store, Index: idx, Clock: fixedClock{},
	}

	res, err := refresh.RefreshOneForce(ctx, hash)
	if err != nil {
		t.Fatalf("refetch bookmark: %v", err)
	}
	if res.Replaced != 0 {
		t.Errorf("a thin page replaced the collected link: %+v", res)
	}
	if !idx.m[hash].Bookmark {
		t.Errorf("the collected link stopped being one without readable text")
	}
	if got := storedSnapshot(t, store, hash); got != old {
		t.Errorf("the collected link changed on a thin page:\n%s", got)
	}
}
