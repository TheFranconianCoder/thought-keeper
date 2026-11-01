package gateways

import (
	"context"
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
