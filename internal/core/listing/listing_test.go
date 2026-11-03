package listing

import (
	"fmt"
	"strings"
	"testing"
)

// blogLinks is the shape of the WordPress index that calibrated the rules: 20 article cards whose
// titles are headings, beside a navigation of 16 links that are not.
func blogLinks() []Link {
	links := make([]Link, 0, 40)
	for i := range 20 {
		links = append(links, Link{
			URL:       fmt.Sprintf("https://agilealliance.org/post-%d/", i),
			Text:      fmt.Sprintf("A long article title number %d about agility", i),
			InHeading: true,
			Group:     "div/div/h3",
		})
	}
	for i := range 16 {
		links = append(links, Link{
			URL:   fmt.Sprintf("https://agilealliance.org/nav-%d/", i),
			Text:  fmt.Sprintf("A navigation entry number %d in the menu", i),
			Group: "div/ul/li",
		})
	}
	return links
}

// archiveLinks is the shape of the beehiiv archive: nine cards with a date in the anchor text, no
// heading, and a clean aria-label.
func archiveLinks() []Link {
	links := make([]Link, 0, 10)
	for i := range 9 {
		links = append(links, Link{
			URL:   fmt.Sprintf("https://sandeepswadia.beehiiv.com/p/post-%d", i),
			Text:  fmt.Sprintf("Oct %d, 2026The title of post %d and its excerpt follows here", i+1, i),
			Label: fmt.Sprintf("The title of post %d", i),
			Group: "div/div/div",
		})
	}
	return links
}

func TestDetectBlogIndex(t *testing.T) {
	res := Detect("https://agilealliance.org/blog/", blogLinks())
	if res.GroupSize != 20 {
		t.Fatalf("group size = %d, want 20 (the cards, not the 16 nav links)", res.GroupSize)
	}
	if len(res.Items) != 20 {
		t.Fatalf("items = %d, want 20", len(res.Items))
	}
	if res.Items[0].Title != "A long article title number 0 about agility" {
		t.Fatalf("title = %q", res.Items[0].Title)
	}
	if !Suggests(res, 221, 400, 0) {
		t.Fatal("a page whose readable text is 221 chars and that repeats 20 cards must be suggested")
	}
	if Suggests(res, 12473, 400, 0) {
		t.Fatal("the same cards beside a 12k-char article are a related rail, not a listing")
	}
}

func TestDetectArchive(t *testing.T) {
	res := Detect("https://sandeepswadia.beehiiv.com/archive", archiveLinks())
	if res.GroupSize != 9 || len(res.Items) != 9 {
		t.Fatalf("group=%d items=%d, want 9/9", res.GroupSize, len(res.Items))
	}
	if res.Items[0].Title != "The title of post 0" {
		t.Fatalf("an aria-label is the cleanest title a card offers, got %q", res.Items[0].Title)
	}
	if res.Items[0].Date != "Oct 1, 2026" {
		t.Fatalf("date = %q", res.Items[0].Date)
	}
	if !Suggests(res, 966, 400, 9) {
		t.Fatal("cards inside the readable text are the content of the page")
	}
}

func TestNavIsNotAListing(t *testing.T) {
	links := make([]Link, 0, 16)
	for i := range 16 {
		links = append(links, Link{
			URL:   fmt.Sprintf("https://example.org/section-%d/", i),
			Text:  fmt.Sprintf("A navigation entry number %d in the menu", i),
			Group: "div/ul/li",
		})
	}
	res := Detect("https://example.org/", links)
	if len(res.Items) != 0 {
		t.Fatalf("navigation must not become articles, got %d", len(res.Items))
	}
	if Suggests(res, 2400, 400, 0) {
		t.Fatal("navigation on a readable page is not a listing")
	}
}

func TestPagerIsNotAnArticle(t *testing.T) {
	links := archiveLinks()
	links = append(links, Link{
		URL:   "https://sandeepswadia.beehiiv.com/archive?page=2",
		Text:  "Load more",
		Group: "div/div/div",
	})
	res := Detect("https://sandeepswadia.beehiiv.com/archive", links)
	for _, item := range res.Items {
		if strings.Contains(item.URL, "page=2") {
			t.Fatal("the pager must never be collected as an article")
		}
	}
	if res.Next != "https://sandeepswadia.beehiiv.com/archive?page=2" {
		t.Fatalf("next = %q", res.Next)
	}
}

// overlayLinks is the shape langchain.com/blog renders: six cards whose anchors are EMPTY (the card
// draws the title and the date beside the link), next to six mega-menu tiles that are in a heading.
func overlayLinks() []Link {
	links := make([]Link, 0, 12)
	for i := range 6 {
		links = append(links, Link{
			URL:   fmt.Sprintf("https://www.langchain.com/blog/post-%d", i),
			Text:  fmt.Sprintf("October %d, 2026A card title the anchor does not carry %d", i+1, i),
			Label: fmt.Sprintf("A card title the anchor does not carry %d", i),
			Card:  true,
			Group: "div/div/div@",
		})
	}
	for i := range 6 {
		links = append(links, Link{
			URL:       fmt.Sprintf("https://www.langchain.com/section-%d", i),
			Text:      fmt.Sprintf("A navigation tile with a heading %d", i),
			InHeading: true,
			InNav:     true,
			Group:     "div/div/div",
		})
	}
	return links
}

func TestDetectOverlayCards(t *testing.T) {
	res := Detect("https://www.langchain.com/blog", overlayLinks())
	if res.GroupSize != 6 || len(res.Items) != 6 {
		t.Fatalf("group=%d items=%d, want 6/6 (the cards, not the six menu tiles)", res.GroupSize, len(res.Items))
	}
	if res.Items[0].Title != "A card title the anchor does not carry 0" {
		t.Fatalf("title = %q, want the card's own label", res.Items[0].Title)
	}
	if res.Items[0].Date != "October 1, 2026" {
		t.Fatalf("date = %q, want the date the card renders", res.Items[0].Date)
	}
	// A grid readability kept: 17 titles and 5k of image addresses. The cards are the page.
	if !Suggests(res, 7406, 400, 6) {
		t.Fatal("a grid whose titles are the readable text is the page's content")
	}
	if Suggests(res, 7406, 400, 0) {
		t.Fatal("cards that are neither addresses nor titles in the text are a rail")
	}
}

// The mega-menu of a JavaScript landing page: ten tiles with headings, a page whose readable text is
// 142 characters, and nothing else. This is how thoughtworks.com/de-de/insights/blog became a
// "listing" of its own navigation.
func TestMegaMenuIsNotAListing(t *testing.T) {
	links := make([]Link, 0, 10)
	for i := range 10 {
		links = append(links, Link{
			URL:       fmt.Sprintf("https://www.thoughtworks.com/de-de/insights/section-%d", i),
			Text:      fmt.Sprintf("Ein Menüeintrag mit Überschrift %d", i),
			InHeading: true,
			InNav:     true,
			Group:     "div/div/div",
		})
	}
	res := Detect("https://www.thoughtworks.com/de-de/insights/blog", links)
	if len(res.Items) != 0 {
		t.Fatalf("navigation became articles: %+v", res.Items)
	}
	if Suggests(res, 142, 400, 0) {
		t.Fatal("a thin page whose only repeated block is its menu is not a listing")
	}
}

func TestNamespacedPager(t *testing.T) {
	base := "https://www.langchain.com/blog"
	next := base + "?8457a1db_page=2"
	// A Webflow pager is an arrow with no text: the words are in the aria-label.
	if got := NextPage(base, []Link{{URL: next, Label: "Next Page"}}); got != next {
		t.Fatalf("an aria-label pager and a namespaced page query, got %q", got)
	}
	if got := NextPage(base, []Link{{URL: next, Text: "2"}}); got != "" {
		t.Fatalf("a bare page number is not a pager: %q", got)
	}
	if got := NextPage(base, []Link{{URL: base + "?sort=asc", Label: "Next Page"}}); got != "" {
		t.Fatalf("a query that is not a page number is not a pager: %q", got)
	}
}

func TestNextPage(t *testing.T) {
	base := "https://example.org/blog/"
	if got := NextPage(base, []Link{{URL: base + "page/2/", Rel: "next", Text: "2"}}); got != base+"page/2/" {
		t.Fatalf("rel=next wins, got %q", got)
	}
	// A document that declares its pager in the head has no anchor text at all.
	if got := NextPage(base, []Link{{URL: base + "page/2/", Rel: "next"}}); got != base+"page/2/" {
		t.Fatalf("a head rel=next is a pager, got %q", got)
	}
	// fs.blog words its pager with a verb in front of words the vocabulary knows.
	if got := NextPage(base, []Link{{URL: base + "page/2/", Text: "See older articles"}}); got != base+"page/2/" {
		t.Fatalf("a verb-led pager, got %q", got)
	}
	if got := NextPage(base, []Link{{URL: base + "page/2/", Text: "See older articles »"}}); got != base+"page/2/" {
		t.Fatalf("a pager with an arrow, got %q", got)
	}
	if got := NextPage(base, []Link{{URL: base + "page/2/", Text: "Load more"}}); got != base+"page/2/" {
		t.Fatalf("load more with a /page/N address, got %q", got)
	}
	// The verb does not make an address a pager: the URL still has to be one.
	if got := NextPage(base, []Link{{URL: base + "about/", Text: "See older articles"}}); got != "" {
		t.Fatalf("an address that is not a pager, got %q", got)
	}
	if got := NextPage(base, []Link{{URL: "https://other.org/blog/page/2/", Text: "Load more"}}); got != "" {
		t.Fatalf("another host is not the next page, got %q", got)
	}
	if got := NextPage(base, []Link{{URL: base + "article/", Text: "Load more"}}); got != "" {
		t.Fatalf("an address that is not a pager, got %q", got)
	}
	if got := NextPage(base, []Link{{URL: base, Text: "Next"}}); got != "" {
		t.Fatalf("the page itself is not the next page, got %q", got)
	}
}

func TestItemsDedupeAndTitleFallback(t *testing.T) {
	links := []Link{
		{URL: "https://example.org/post/", Text: "Sep 1, 2026A title that is long enough to pass", InHeading: true, Group: "a/b/c"},
		{URL: "https://example.org/post", Text: "Sep 1, 2026A title that is long enough to pass", InHeading: true, Group: "a/b/c"},
		{URL: "https://example.org/post-b/", Text: "Sep 2, 2026Another title that is long enough", InHeading: true, Group: "a/b/c"},
		{URL: "https://example.org/post-c/", Text: "Sep 3, 2026A third title that is long enough", InHeading: true, Group: "a/b/c"},
		{URL: "https://example.org/post-d/", Text: "Sep 4, 2026A fourth title that is long enough", InHeading: true, Group: "a/b/c"},
		{URL: "https://example.org/post-e/", Text: "Sep 5, 2026A fifth title that is long enough", InHeading: true, Group: "a/b/c"},
	}
	items := PageItems("https://example.org/", links)
	if len(items) != 5 {
		t.Fatalf("items = %d, want 5 after the trailing-slash dedupe", len(items))
	}
	if items[0].Title != "A title that is long enough to pass" {
		t.Fatalf("the leading date must be stripped from the fallback title, got %q", items[0].Title)
	}
	if items[0].Date != "Sep 1, 2026" {
		t.Fatalf("date = %q", items[0].Date)
	}
}

func TestOffHostLinksAreIgnored(t *testing.T) {
	links := archiveLinks()
	for i := range links {
		links[i].URL = strings.Replace(links[i].URL, "sandeepswadia.beehiiv.com", "evil.example", 1)
	}
	if res := Detect("https://sandeepswadia.beehiiv.com/archive", links); len(res.Items) != 0 {
		t.Fatalf("links to another host are not articles, got %d", len(res.Items))
	}
}

// The Thoughtworks blog index renders its cards as Coveo search results: the title is an anchor with
// role="heading", and the block also carries a language switch into another locale. The cards are the
// majority of the block, so the block is article-shaped — one foreign link does not undo twenty cards.
func TestRoleHeadingCardsBesideOneForeignLink(t *testing.T) {
	links := make([]Link, 0, 21)
	for i := range 20 {
		links = append(links, Link{
			URL:       fmt.Sprintf("https://www.thoughtworks.com/de-de/insights/blog/topic/post-%d", i),
			Text:      fmt.Sprintf("Ein Kartentitel aus dem Suchergebnis %d", i),
			Label:     fmt.Sprintf("Ein Kartentitel aus dem Suchergebnis %d", i),
			InHeading: true,
			Group:     "div/div/div",
		})
	}
	links = append(links, Link{
		URL:       "https://www.thoughtworks.com/en-de/insights/blog",
		Text:      "Diese Seite auf Englisch lesen",
		InHeading: true,
		Group:     "div/div/div",
	})
	res := Detect("https://www.thoughtworks.com/de-de/insights/blog", links)
	if res.GroupSize != 21 || len(res.Items) != 21 {
		t.Fatalf("group=%d items=%d, want the 21 links of the block", res.GroupSize, len(res.Items))
	}
	if !Suggests(res, 4148, 400, 20) {
		t.Fatal("twenty card titles in the readable text are the page's content")
	}
}

// A block of plain title links in one section is still article-shaped: the shared path is the
// evidence, and it does not need every last link to agree (a `?sort=` or an "all posts" tile in the
// same block is not a reason to call twenty cards navigation).
func TestSharedPrefixToleratesAMinority(t *testing.T) {
	links := make([]Link, 0, 10)
	for i := range 8 {
		links = append(links, Link{
			URL:   fmt.Sprintf("https://example.org/posts/post-%d", i),
			Text:  fmt.Sprintf("A plain post title without any date %d", i),
			Group: "ul/li/a",
		})
	}
	for i := range 2 {
		links = append(links, Link{
			URL:   fmt.Sprintf("https://example.org/about/page-%d", i),
			Text:  fmt.Sprintf("A page of another section entirely %d", i),
			Group: "ul/li/a",
		})
	}
	res := Detect("https://example.org/posts", links)
	if len(res.Items) != 10 {
		t.Fatalf("items = %d, want the ten links of the block", len(res.Items))
	}
}
