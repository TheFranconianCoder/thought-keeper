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

func TestNextPage(t *testing.T) {
	base := "https://example.org/blog/"
	if got := NextPage(base, []Link{{URL: base + "page/2/", Rel: "next", Text: "2"}}); got != base+"page/2/" {
		t.Fatalf("rel=next wins, got %q", got)
	}
	if got := NextPage(base, []Link{{URL: base + "page/2/", Text: "Load more"}}); got != base+"page/2/" {
		t.Fatalf("load more with a /page/N address, got %q", got)
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
