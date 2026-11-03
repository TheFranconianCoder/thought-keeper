package usecases_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/listing"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

// fakeLinks is the extractor as the rules see it: a page address and the links its cards declared.
// The listing rules themselves are tested in core/listing; here the question is what the USE CASE
// does with them.
type fakeLinks map[string][]listing.Link

func (f fakeLinks) Links(_ context.Context, _ []byte, pageURL string) ([]listing.Link, error) {
	return f[pageURL], nil
}

// cards builds n article cards in one repeated block: a title in a heading, which is the shape a
// CMS gives a card and what separates the grid from the navigation beside it.
func cards(pageURL string, n int, next string) []listing.Link {
	links := make([]listing.Link, 0, n+1)
	for i := range n {
		links = append(links, listing.Link{
			URL:       fmt.Sprintf("%s/article-%d/", strings.TrimSuffix(pageURL, "/"), i),
			Text:      fmt.Sprintf("A long enough article title number %d", i),
			InHeading: true,
			Group:     "div/div/h3",
		})
	}
	if next != "" {
		links = append(links, listing.Link{URL: next, Text: "Next", Rel: "next"})
	}
	return links
}

func newListingUseCase(t *testing.T, links fakeLinks, markdown string) (usecases.URLs, *fakeFetcher, *fakeIndex, *objects.Store) {
	t.Helper()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	fetch := &fakeFetcher{page: interfaces.FetchedPage{Status: 200, Body: []byte("<html>page</html>"), ContentType: "text/html"}}
	uc := usecases.URLs{
		Fetch: fetch,
		Convert: &fakeConverter{
			title: "The Blog", markdown: markdown,
		},
		Links: links,
		Store: store, Index: idx, Clock: fixedClock{}, MinTextChars: 400,
	}
	return uc, fetch, idx, store
}

func itemURLsOf(links []listing.Link) []string {
	out := make([]string, 0, len(links))
	for _, link := range links {
		out = append(out, link.URL)
	}
	return out
}

func snapshotOf(t *testing.T, store *objects.Store, url string) string {
	t.Helper()
	rc, err := store.Open(entities.URLHash(url), ".md")
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return string(body)
}

const blogURL = "https://example.com/blog/"

// A page whose readable text is tiny is a card grid: the index page of a blog. The detector finds
// the cards, but the page is still stored as the reader asked — the offer is a question, not a
// verdict.
func TestAddURLOffersAListingWithoutReclassifying(t *testing.T) {
	ctx := context.Background()
	uc, _, _, store := newListingUseCase(t, fakeLinks{blogURL: cards(blogURL, 6, "")}, "Blog")
	src, err := uc.AddManual(ctx, blogURL, false, usecases.ListingAuto)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if src.Listing {
		t.Fatal("an unconfirmed page must not become a listing")
	}
	if src.ListingCandidates != 6 {
		t.Fatalf("candidates = %d, want 6 (the offer)", src.ListingCandidates)
	}
	if !src.Bookmark {
		t.Error("the page has no readable text: it is a bookmark until the listing is confirmed")
	}
	if body := snapshotOf(t, store, blogURL); strings.Contains(body, "listing: true") {
		t.Errorf("the snapshot claims to be a listing:\n%s", body)
	}
}

// Confirming turns the page into an OWNER: the articles are the children, the list is the snapshot,
// and the document steps do not apply.
func TestAddURLAsListingStoresTheOwner(t *testing.T) {
	ctx := context.Background()
	next := "https://example.com/blog/page/2/"
	uc, _, idx, store := newListingUseCase(t, fakeLinks{blogURL: cards(blogURL, 6, next)}, "Blog")
	src, err := uc.AddManual(ctx, blogURL, false, usecases.ListingYes)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !src.Listing {
		t.Fatal("the confirmed page is not a listing")
	}
	if src.Bookmark {
		t.Error("a listing is a list of sources, not a bookmark")
	}
	if len(src.ListingURLs) != 6 {
		t.Fatalf("articles = %d, want 6", len(src.ListingURLs))
	}
	if src.ListingNext != next {
		t.Fatalf("next = %q, want %q", src.ListingNext, next)
	}
	if src.NeedsReset {
		t.Error("a brand-new listing has no derivation to throw away")
	}
	body := snapshotOf(t, store, blogURL)
	if !strings.Contains(body, "listing: true") || !strings.Contains(body, "6 articles declared") {
		t.Errorf("the snapshot is not the list:\n%s", body)
	}
	stored, err := idx.GetSourceByHash(ctx, entities.URLHash(blogURL))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !stored.Listing {
		t.Error("the listing marker did not reach the index")
	}
}

// The confirmation is a SECOND add: the page is a bookmark by then, and turning it into a listing
// must throw the bookmark's derivation away.
func TestConfirmingAListingOnABookmarkAsksForAReset(t *testing.T) {
	ctx := context.Background()
	uc, _, _, _ := newListingUseCase(t, fakeLinks{blogURL: cards(blogURL, 6, "")}, "Blog")
	if _, err := uc.AddManual(ctx, blogURL, false, usecases.ListingAuto); err != nil {
		t.Fatalf("first add: %v", err)
	}
	src, err := uc.AddManual(ctx, blogURL, false, usecases.ListingYes)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if !src.Listing {
		t.Fatal("the confirmed page is not a listing")
	}
	if !src.NeedsReset {
		t.Error("the bookmark's outline node belongs to other text: the caller must reset")
	}
}

// A card grid whose excerpts make the readable text long enough to look like a document is still not
// prose: a newsletter archive is exactly that. Until the reader answers, it must not be distilled
// into claims about article titles, so it is stored as a collected link.
func TestAddURLKeepsACardGridOutOfTheDistillQueue(t *testing.T) {
	ctx := context.Background()
	links := cards(blogURL, 9, "")
	// The converter's output for a real archive: the cards survive readability, so the readable text
	// is long AND carries the article addresses.
	markdown := strings.Repeat("card excerpt text ", 100) + " " + strings.Join(itemURLsOf(links), " ")
	uc, _, idx, _ := newListingUseCase(t, fakeLinks{blogURL: links}, markdown)
	src, err := uc.AddManual(ctx, blogURL, false, usecases.ListingAuto)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if src.ListingCandidates != 9 {
		t.Fatalf("candidates = %d, want 9", src.ListingCandidates)
	}
	stored, err := idx.GetSourceByHash(ctx, entities.URLHash(blogURL))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !stored.Bookmark {
		t.Error("a card grid must not enter the document pipeline before the reader answers")
	}
	if stored.Listing {
		t.Error("the page became a listing without the reader's answer")
	}
}

// A long article with a related-posts rail is not a listing: the rail is not the page's content.
func TestAddURLDoesNotOfferAListingForAnArticle(t *testing.T) {
	ctx := context.Background()
	uc, _, _, _ := newListingUseCase(t, fakeLinks{blogURL: cards(blogURL, 6, "")}, strings.Repeat("article prose ", 100))
	src, err := uc.AddManual(ctx, blogURL, false, usecases.ListingAuto)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if src.ListingCandidates != 0 || src.Listing {
		t.Fatalf("an article with a related rail was offered as a listing: %+v", src)
	}
}

// An overlay grid leaves the readable text with the card TITLES and not one address: readability
// keeps the headings and drops the empty anchors (langchain.com/blog). The grid is still the page's
// content, so it is offered — and kept out of the distill queue until the reader answers.
func TestAddURLOffersAListingForAnOverlayGrid(t *testing.T) {
	ctx := context.Background()
	links := make([]listing.Link, 0, 6)
	var markdown strings.Builder
	markdown.WriteString("## Featured stories\n\n")
	for i := range 6 {
		title := fmt.Sprintf("An overlay card title number %d", i)
		links = append(links, listing.Link{
			URL:   fmt.Sprintf("%s/overlay-%d", strings.TrimSuffix(blogURL, "/"), i),
			Text:  fmt.Sprintf("October %d, 2026%s", i+1, title),
			Label: title,
			Card:  true,
			Group: "div/div/div@",
		})
		fmt.Fprintf(&markdown,
			"![cover image for the post](https://cdn.example.com/cover-%d-1200x675.png)\n\n## %s\n\n", i, title)
	}
	uc, _, idx, _ := newListingUseCase(t, fakeLinks{blogURL: links}, markdown.String())
	src, err := uc.AddManual(ctx, blogURL, false, usecases.ListingAuto)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if src.ListingCandidates != 6 {
		t.Fatalf("candidates = %d, want 6 (the cards whose titles carry the page)", src.ListingCandidates)
	}
	stored, err := idx.GetSourceByHash(ctx, entities.URLHash(blogURL))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !stored.Bookmark || stored.Listing {
		t.Errorf("bookmark=%v listing=%v, want a collected link until the reader answers", stored.Bookmark, stored.Listing)
	}
}

// A listing is refreshed through its first page only. A new article is on page 1; walking the
// archive again every day would fetch everything to learn nothing.
func TestListingRefreshReadsOnlyTheFirstPage(t *testing.T) {
	ctx := context.Background()
	next := "https://example.com/blog/page/2/"
	uc, fetch, _, store := newListingUseCase(t, fakeLinks{blogURL: cards(blogURL, 4, next)}, "Blog")
	src, err := uc.AddManual(ctx, blogURL, false, usecases.ListingYes)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := uc.QueueListing(ctx, src.Hash, src.ListingURLs); err != nil {
		t.Fatalf("queue: %v", err)
	}
	// A new article appears on page 1.
	links := cards(blogURL, 4, next)
	links = append(links, listing.Link{
		URL: "https://example.com/blog/article-new/", Text: "A brand new article title here", InHeading: true, Group: "div/div/h3",
	})
	uc.Links = fakeLinks{blogURL: links}
	before := fetch.calls
	res, err := uc.RefreshOne(ctx, src.Hash)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if fetch.calls != before+1 {
		t.Fatalf("refresh fetched %d pages, want 1", fetch.calls-before)
	}
	if len(res.Discovered) != 1 || len(res.Discovered[0].URLs) != 1 ||
		res.Discovered[0].URLs[0] != "https://example.com/blog/article-new/" {
		t.Fatalf("discovered = %+v", res.Discovered)
	}
	if res.Removed != 0 {
		t.Errorf("a listing never removes: removed = %d", res.Removed)
	}
	// Page 1 was the only page read, but the archive it collected on capture must survive the
	// refresh: a listing is a window, and its snapshot is the set it ever declared.
	body := snapshotOf(t, store, blogURL)
	if !strings.Contains(body, "5 articles declared") {
		t.Errorf("the refresh dropped part of the archive:\n%s", body)
	}
	for _, want := range []string{"article-new", "article-0/", "article-3/"} {
		if !strings.Contains(body, want) {
			t.Errorf("the snapshot lost %q after the refresh:\n%s", want, body)
		}
	}
}

// The initial walk follows the pagination: ExpandListing reads one page, FinishListing folds every
// page it has seen into the list.
func TestListingWalkReadsTheNextPage(t *testing.T) {
	ctx := context.Background()
	next := "https://example.com/blog/page/2/"
	second := "https://example.com/blog/page/2/"
	uc, _, _, store := newListingUseCase(t, fakeLinks{
		blogURL: cards(blogURL, 4, next),
		second:  cards("https://example.com/blog/", 4, ""),
	}, "Blog")
	src, err := uc.AddManual(ctx, blogURL, false, usecases.ListingYes)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if src.ListingNext != next {
		t.Fatalf("next = %q", src.ListingNext)
	}
	page, err := uc.ExpandListing(ctx, src.Hash, next)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(page.Items) != 4 {
		t.Fatalf("items on page 2 = %d, want 4", len(page.Items))
	}
	items := append(append([]listing.Item{}, src.ListingItems...), page.Items...)
	count, err := uc.FinishListing(ctx, src.Hash, items, page.Status)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	// Both pages declare the same four addresses, so the list stays at four.
	if count != 4 {
		t.Fatalf("articles = %d, want 4 after the dedupe", count)
	}
	if body := snapshotOf(t, store, blogURL); !strings.Contains(body, "4 articles declared") {
		t.Errorf("the snapshot was not rewritten:\n%s", body)
	}
}

// Re-adding a listing re-reads page 1 and keeps the archive the walk collected. It must neither
// replace the list with a page nor start a second walk of the pagination.
func TestReAddingAListingKeepsTheArchive(t *testing.T) {
	ctx := context.Background()
	next := "https://example.com/blog/page/2/"
	uc, _, _, store := newListingUseCase(t, fakeLinks{
		blogURL: cards(blogURL, 4, next),
		next:    cards(next, 4, ""),
	}, "Blog")
	src, err := uc.AddManual(ctx, blogURL, false, usecases.ListingYes)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	page, err := uc.ExpandListing(ctx, src.Hash, next)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	items := append(append([]listing.Item{}, src.ListingItems...), page.Items...)
	if _, err := uc.FinishListing(ctx, src.Hash, items, page.Status); err != nil {
		t.Fatalf("finish: %v", err)
	}

	again, err := uc.AddManual(ctx, blogURL, false, usecases.ListingAuto)
	if err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if !again.Listing {
		t.Fatal("a known listing was turned back into a page")
	}
	if again.ListingNext != "" {
		t.Errorf("a re-add started a second pagination walk: next = %q", again.ListingNext)
	}
	if again.NeedsReset {
		t.Error("a re-read of the same list is not a reclassification")
	}
	if body := snapshotOf(t, store, blogURL); !strings.Contains(body, "8 articles declared") {
		t.Errorf("the re-add dropped the archive:\n%s", body)
	}
}

// Forcing a listing on a page with no cards is an error, not an empty owner.
func TestForcingAListingWithoutCardsFails(t *testing.T) {
	ctx := context.Background()
	uc, _, _, _ := newListingUseCase(t, fakeLinks{}, "Prose")
	if _, err := uc.AddManual(ctx, blogURL, false, usecases.ListingYes); err == nil {
		t.Fatal("a page with no cards cannot be forced into a listing")
	}
}
