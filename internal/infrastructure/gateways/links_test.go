package gateways

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/listing"
)

func TestLinksReadsCardsAndNavigation(t *testing.T) {
	page := `<!doctype html><html><body>
<nav><ul><li><a href="/agile101/">Agile 101 – What is Agile?</a></li></ul></nav>
<main>
  <article><div><div>
    <h3><a href="/post-one/" aria-label="Post One">Post One — a title long enough</a></h3>
    <div class="terms"><a href="/category/process/">Process</a></div>
  </div></div></article>
  <article><div><div>
    <h3><a href="https://example.org/post-two/#section">Post Two</a></h3>
  </div></div></article>
  <article><div><div>
    <a href="/post-three/"><span>Oct 27, 2026</span><span>The clean title of post three</span>
      <time datetime="2026-10-27">Oct 27</time></a>
  </div></div></article>
</main>
<a href="mailto:hello@example.org">Mail</a>
<a href="#top">Top</a>
<a href="/feed.xml" rel="alternate">Feed</a>
</body></html>`

	links, err := HTMLLinkExtractor{}.Links(context.Background(), []byte(page), "https://example.org/blog/")
	if err != nil {
		t.Fatalf("links: %v", err)
	}
	if len(links) != 6 {
		t.Fatalf("links = %d, want 6 (the mail, the fragment and the feed are still anchors)", len(links))
	}

	one := findByURL(links, "https://example.org/post-one/")
	if one == nil {
		t.Fatal("the relative card link was not resolved")
	}
	if !one.InHeading {
		t.Error("a title inside <h3> must be marked as a heading")
	}
	if one.Label != "Post One" {
		t.Errorf("label = %q, want the aria-label", one.Label)
	}
	if one.Group != "div/div/h3" {
		t.Errorf("group = %q, want the tag path of the card", one.Group)
	}

	two := findByURL(links, "https://example.org/post-two/")
	if two == nil {
		t.Fatal("an absolute link was not kept")
	}
	if two.URL != "https://example.org/post-two/" {
		t.Errorf("the fragment must be dropped, got %q", two.URL)
	}

	three := findByURL(links, "https://example.org/post-three/")
	if three == nil {
		t.Fatal("a heading-less card link was not kept")
	}
	if three.InHeading {
		t.Error("a card without a heading is not in one")
	}
	if three.Time == "" {
		t.Error("the card's <time> was not read")
	}
	if !containsSpace(three.Text) || three.Text == "" {
		t.Errorf("the anchor text lost its word boundaries: %q", three.Text)
	}

	if rel := findByURL(links, "https://example.org/feed.xml"); rel == nil || rel.Rel != "alternate" {
		t.Errorf("rel was not read: %+v", rel)
	}
}

func TestLinksSkipsExecutableSchemes(t *testing.T) {
	page := `<html><body>
<a href="javascript:void(0)">One</a>
<a href="data:text/html,x">Two</a>
<a href="">Three</a>
<a>Four</a>
<a href="https://example.org/ok/">Five</a>
</body></html>`
	links, err := HTMLLinkExtractor{}.Links(context.Background(), []byte(page), "https://example.org/")
	if err != nil {
		t.Fatalf("links: %v", err)
	}
	if len(links) != 1 || links[0].URL != "https://example.org/ok/" {
		t.Fatalf("links = %+v, want the one http address", links)
	}
}

// An overlay card is what a Webflow blog index renders: the anchor is EMPTY and the title, the author
// and the date sit beside it in the same block. Without borrowing the block, a page of twenty perfect
// cards has zero candidates — which is exactly what langchain.com/blog reported.
func TestLinksReadOverlayCards(t *testing.T) {
	page := `<html><body><main><section id="blog">
  <div role="list" class="blog-list">
    <div role="listitem" class="blog-item">
      <div class="blog-link"><h2>How We Built Our Design Agent</h2>
        <div class="author">Harrison Chase</div><div class="date">October 27, 2026</div></div>
      <a href="/blog/design-agent" class="blog-link-absolute"></a>
    </div>
    <div role="listitem" class="blog-item">
      <div class="blog-link"><h2>Machine Learning in the News</h2></div>
      <a href="/blog/ml-in-the-news" class="blog-link-absolute"></a>
    </div>
  </div>
</section></main>
<header><div class="tile"><h3>Platform</h3><a href="/platform"></a></div></header>
<nav><ul><li><a href="/pricing">Pricing</a></li></ul></nav>
</body></html>`

	links, err := HTMLLinkExtractor{}.Links(context.Background(), []byte(page), "https://www.langchain.com/blog")
	if err != nil {
		t.Fatalf("links: %v", err)
	}
	card := findByURL(links, "https://www.langchain.com/blog/design-agent")
	if card == nil {
		t.Fatal("the overlay link was dropped")
	}
	if !card.Card {
		t.Error("an empty anchor whose block renders a heading is a card")
	}
	if card.Label != "How We Built Our Design Agent" {
		t.Errorf("label = %q, want the card's heading", card.Label)
	}
	if !strings.Contains(card.Text, "October 27, 2026") || !strings.Contains(card.Text, "Harrison Chase") {
		t.Errorf("the card's text was not borrowed: %q", card.Text)
	}
	if card.InHeading {
		t.Error("the anchor is not inside a heading: the card's heading is its title")
	}
	if !strings.HasSuffix(card.Group, "@") {
		t.Errorf("group = %q, want the overlay marker so cards do not merge with inline links", card.Group)
	}
	if card.InNav {
		t.Error("the card is in <main>, not in the navigation")
	}

	// A tile in the header is navigation, however card-shaped it looks.
	if tile := findByURL(links, "https://www.langchain.com/platform"); tile == nil || !tile.InNav {
		t.Errorf("a header tile was not marked as navigation: %+v", tile)
	}
	// An ordinary nav link is marked too, and keeps its own text.
	nav := findByURL(links, "https://www.langchain.com/pricing")
	if nav == nil || !nav.InNav || nav.Card {
		t.Errorf("a nav link = %+v, want InNav and not a card", nav)
	}
}

// TestLinksReadARoleHeadingCard: a card component built by a script marks its title
// `role="heading"` instead of using an h tag — the Coveo result card of the Thoughtworks blog does
// exactly that, and a title is a title whichever way it is declared.
func TestLinksReadARoleHeadingCard(t *testing.T) {
	page := `<html><body><main><div class="results">
  <div class="result-item"><a data-testid="result-item__title" role="heading" aria-level="4"
      href="/de-de/insights/blog/careers/behind-the-scenes" aria-label="Behind the scenes, Mehr erfahren">
      <div class="result-item__header"><p>Careers</p></div>
      <span class="result-item__title-text">Behind the scenes of Thoughtworks University</span>
      <p class="result-item__author"><span>June 02, 2025</span><span>Von Jade Daubney-Bennett</span></p>
      <p class="result-item__excerpt">Thoughtworks University is back in person.</p>
    </a></div>
</div></main></body></html>`

	links, err := HTMLLinkExtractor{}.Links(context.Background(), []byte(page), "https://www.thoughtworks.com/de-de/insights/blog")
	if err != nil {
		t.Fatalf("links: %v", err)
	}
	card := findByURL(links, "https://www.thoughtworks.com/de-de/insights/blog/careers/behind-the-scenes")
	if card == nil {
		t.Fatal("the card link was dropped")
	}
	if !card.InHeading {
		t.Error(`an anchor with role="heading" carries its title as a heading`)
	}
	if card.InNav {
		t.Error("a result card in <main> is not navigation")
	}
	if !strings.Contains(card.Text, "June 02, 2025") {
		t.Errorf("the card's text was not read: %q", card.Text)
	}
}

// The shape fs.blog/blog renders: sixteen heading cards, a pager worded "See older articles" and the
// next page declared only as `<link rel="next">` in the head. Without the head declaration the walk
// stops after page 1 and the archive is never collected.
func TestLinksReadsTheHeadPager(t *testing.T) {
	var cards strings.Builder
	for i := range 6 {
		fmt.Fprintf(&cards, `<article><div><h2><a href="/post-%d/">A title long enough to be a card %d</a></h2></div></article>`, i, i)
	}
	page := `<!doctype html><html><head>
  <link rel="next" href="/blog/page/2/">
  <link rel="alternate" type="application/rss+xml" href="/feed.xml">
</head><body><main>` + cards.String() + `</main>
<div class="pagination"><a href="/blog/page/2/">See older articles</a></div>
</body></html>`

	const address = "https://example.org/blog/"
	links, err := HTMLLinkExtractor{}.Links(context.Background(), []byte(page), address)
	if err != nil {
		t.Fatalf("links: %v", err)
	}
	next := findByURL(links, "https://example.org/blog/page/2/")
	if next == nil {
		t.Fatal("the head's rel=next was not read")
	}
	res := listing.Detect(address, links)
	if res.Next != "https://example.org/blog/page/2/" {
		t.Fatalf("next = %q, want the page the head declares", res.Next)
	}
	if len(res.Items) != 6 {
		t.Fatalf("items = %d, want the six cards (the pager is never an article)", len(res.Items))
	}
}

func findByURL(links []listing.Link, url string) *listing.Link {
	for i := range links {
		if links[i].URL == url {
			return &links[i]
		}
	}
	return nil
}

func containsSpace(s string) bool {
	for _, r := range s {
		if r == ' ' {
			return true
		}
	}
	return false
}
