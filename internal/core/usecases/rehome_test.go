package usecases_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

// fakeWikiPages is the page store as the deletion reads it: only List is used, and only for the
// URLs a page's prose still declares.
type fakeWikiPages struct {
	pages []entities.WikiPage
}

func (f fakeWikiPages) Read(_ context.Context, id ulid.ULID) (entities.WikiPage, error) {
	for _, page := range f.pages {
		if page.ID == id {
			return page, nil
		}
	}
	return entities.WikiPage{}, fmt.Errorf("no page %s", id)
}

func (f fakeWikiPages) Write(context.Context, entities.WikiPage) error { return nil }
func (f fakeWikiPages) List(context.Context) ([]entities.WikiPage, error) {
	return f.pages, nil
}
func (f fakeWikiPages) Remove(context.Context, ulid.ULID) error { return nil }

// listingSnapshot renders a list source's snapshot in the shape the app writes: the declaration map
// reads exactly these lines.
func listingSnapshot(articles ...string) string {
	var b strings.Builder
	b.WriteString("---\nlisting: true\n---\n\n# A list\n\n")
	fmt.Fprintf(&b, "%d articles declared by this listing.\n\n", len(articles))
	for i, article := range articles {
		fmt.Fprintf(&b, "- [Article %d](%s)\n", i, article)
	}
	return b.String()
}

type rehomeFixture struct {
	ctx     context.Context
	idx     *fakeIndex
	store   *objects.Store
	del     usecases.Sources
	listing entities.Source
}

func newRehomeFixture(t *testing.T, articles []string, pages ...entities.WikiPage) rehomeFixture {
	t.Helper()
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	listingURL := "https://example.com/blog/"
	listing := entities.Source{
		ID: ulid.Make(), Hash: entities.URLHash(listingURL), Kind: entities.SourceKindURL,
		Ext: ".md", URL: listingURL, Title: "Blog", Listing: true,
		Meta: `{"url":"` + listingURL + `","listing":"true"}`,
	}
	if _, err := store.PutAt(listing.Hash, ".md", []byte(listingSnapshot(articles...))); err != nil {
		t.Fatalf("listing snapshot: %v", err)
	}
	if err := idx.UpsertSource(ctx, listing); err != nil {
		t.Fatalf("listing row: %v", err)
	}
	del := usecases.Sources{
		Store: store, Index: idx, Atoms: &fakeAtomRepo{},
		Pages: fakeWikiPages{pages: pages}, Clock: fixedClock{},
	}
	return rehomeFixture{ctx: ctx, idx: idx, store: store, del: del, listing: listing}
}

func (f rehomeFixture) child(t *testing.T, rawURL, origin, wikiPage, parentHash string) entities.Source {
	t.Helper()
	child := entities.Source{
		ID: ulid.Make(), Hash: entities.URLHash(rawURL), Kind: entities.SourceKindURL,
		Ext: ".md", URL: rawURL, Title: rawURL, Origin: origin, WikiPage: wikiPage, ParentHash: parentHash,
		Meta: `{"url":"` + rawURL + `"}`,
	}
	if _, err := f.store.PutAt(child.Hash, ".md", []byte("# "+rawURL+"\n")); err != nil {
		t.Fatalf("child snapshot: %v", err)
	}
	if err := f.idx.UpsertSource(f.ctx, child); err != nil {
		t.Fatalf("child row: %v", err)
	}
	return child
}

// A wiki page that goes away must not take a source its listing still declares: the source is kept
// and re-homed to the listing, with its snapshot, its claims and its log untouched.
func TestDeletingAPageRehomesASourceItsListingStillDeclares(t *testing.T) {
	pageID := ulid.Make()
	page := entities.WikiPage{ID: pageID, Title: "Notes", Body: "nothing here\n"}
	f := newRehomeFixture(t, []string{"https://example.com/one", "https://example.com/two"}, page)
	child := f.child(t, "https://example.com/one", entities.WikiLinkOrigin, pageID.String(), "")

	res, err := f.del.DeleteOwned(f.ctx, child.Hash)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.Rehomed != 1 || len(res.Removed) != 0 {
		t.Fatalf("result = %+v, want one re-home and no removal", res)
	}
	stored, err := f.idx.GetSourceByHash(f.ctx, child.Hash)
	if err != nil {
		t.Fatalf("the source was deleted: %v", err)
	}
	if stored.Origin != entities.ListingOrigin || stored.ParentHash != f.listing.Hash || stored.WikiPage != "" {
		t.Fatalf("ownership = origin %q parent %q wiki %q, want the listing", stored.Origin, stored.ParentHash, stored.WikiPage)
	}
	if body := snapshotOf(t, f.store, "https://example.com/one"); !strings.Contains(body, "https://example.com/one") {
		t.Errorf("the snapshot was touched: %q", body)
	}
}

// The other direction: deleting the listing keeps a source a wiki page still links, and the page is
// the new owner.
func TestDeletingAListingKeepsASourceAWikiPageStillLinks(t *testing.T) {
	pageID := ulid.Make()
	page := entities.WikiPage{ID: pageID, Title: "Notes", Body: "read https://example.com/one\n"}
	f := newRehomeFixture(t, []string{"https://example.com/one"}, page)
	child := f.child(t, "https://example.com/one", entities.ListingOrigin, "", f.listing.Hash)

	res, err := f.del.DeleteOwned(f.ctx, f.listing.Hash)
	if err != nil {
		t.Fatalf("delete listing: %v", err)
	}
	if res.Rehomed != 1 {
		t.Fatalf("result = %+v, want the child re-homed", res)
	}
	stored, err := f.idx.GetSourceByHash(f.ctx, child.Hash)
	if err != nil {
		t.Fatalf("the child was deleted with the listing: %v", err)
	}
	if stored.Origin != entities.WikiLinkOrigin || stored.WikiPage != pageID.String() || stored.ParentHash != "" {
		t.Fatalf("ownership = origin %q wiki %q parent %q, want the page", stored.Origin, stored.WikiPage, stored.ParentHash)
	}
	if _, err := f.idx.GetSourceByHash(f.ctx, f.listing.Hash); err == nil {
		t.Fatal("the listing itself must be gone")
	}
}

// A complete list is the stronger claim: when a sitemap and a listing both declare the URL, the
// sitemap owns it after the page goes.
func TestRehomePrefersTheCompleteList(t *testing.T) {
	pageID := ulid.Make()
	page := entities.WikiPage{ID: pageID, Title: "Notes", Body: "nothing here\n"}
	f := newRehomeFixture(t, []string{"https://example.com/one"}, page)
	sitemapURL := "https://example.com/sitemap.xml"
	sitemap := entities.Source{
		ID: ulid.Make(), Hash: entities.URLHash(sitemapURL), Kind: entities.SourceKindURL,
		Ext: ".md", URL: sitemapURL, Title: "Sitemap", Sitemap: true,
		Meta: `{"sitemap":"true"}`,
	}
	if _, err := f.store.PutAt(sitemap.Hash, ".md", []byte(listingSnapshot("https://example.com/one"))); err != nil {
		t.Fatalf("sitemap snapshot: %v", err)
	}
	if err := f.idx.UpsertSource(f.ctx, sitemap); err != nil {
		t.Fatalf("sitemap row: %v", err)
	}
	child := f.child(t, "https://example.com/one", entities.WikiLinkOrigin, pageID.String(), "")

	if _, err := f.del.DeleteOwned(f.ctx, child.Hash); err != nil {
		t.Fatalf("delete: %v", err)
	}
	stored, err := f.idx.GetSourceByHash(f.ctx, child.Hash)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.Origin != entities.SitemapOrigin || stored.ParentHash != sitemap.Hash {
		t.Fatalf("ownership = origin %q parent %q, want the sitemap", stored.Origin, stored.ParentHash)
	}
}

// Nobody else declares it: the source is deleted, as it always was.
func TestDeletingAPageRemovesASourceNoOneElseDeclares(t *testing.T) {
	pageID := ulid.Make()
	page := entities.WikiPage{ID: pageID, Title: "Notes", Body: "nothing here\n"}
	f := newRehomeFixture(t, nil, page)
	child := f.child(t, "https://example.com/orphan", entities.WikiLinkOrigin, pageID.String(), "")

	res, err := f.del.DeleteOwned(f.ctx, child.Hash)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.Rehomed != 0 || len(res.Removed) != 1 {
		t.Fatalf("result = %+v, want a removal", res)
	}
	if _, err := f.idx.GetSourceByHash(f.ctx, child.Hash); err == nil {
		t.Fatal("a source nobody declares must be deleted")
	}
}

// The single-source read tells the two numbers apart: a listing that declares four articles and
// owns one of them reports 4 declared and 1 child.
func TestSourceReadReportsDeclaredAndOwned(t *testing.T) {
	f := newRehomeFixture(t, []string{
		"https://example.com/one", "https://example.com/two",
		"https://example.com/three", "https://example.com/four",
	})
	owned := f.child(t, "https://example.com/one", entities.ListingOrigin, "", f.listing.Hash)

	got, err := f.del.Get(f.ctx, f.listing.Hash)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Declared != 4 {
		t.Errorf("declared = %d, want 4", got.Declared)
	}
	if got.Children != 1 {
		t.Errorf("children = %d, want 1 (the other three belong elsewhere)", got.Children)
	}
	if _, err := f.idx.GetSourceByHash(f.ctx, owned.Hash); err != nil {
		t.Fatalf("owned child: %v", err)
	}
}
