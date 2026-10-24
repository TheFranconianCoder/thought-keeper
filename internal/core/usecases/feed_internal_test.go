package usecases

import (
	"strings"
	"testing"
)

// TestParseFeed covers the three XML families the app accepts, the two address shapes (RSS <link>,
// Atom <link href>) and the rules the rest of the app depends on: resolve against the feed, drop the
// feed itself, deduplicate with trailing slashes ignored.
func TestParseFeed(t *testing.T) {
	const base = "https://example.com/feed.xml"

	t.Run("rss 2.0", func(t *testing.T) {
		body := `<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel>
  <title> Example Blog </title>
  <item><title> First post </title><link>https://example.com/posts/one</link><pubDate>Mon, 20 Oct 2026 10:00:00 GMT</pubDate><content:encoded><![CDATA[<p>Full one</p>]]></content:encoded></item>
  <item><title>Second</title><link>/posts/two/</link></item>
  <item><title>Duplicate of one</title><link>https://example.com/posts/one/</link></item>
  <item><title>The feed itself</title><link>https://example.com/feed.xml</link></item>
  <item><title>Relative</title><link>posts/three</link></item>
  <item><title>No link at all</title></item>
</channel></rss>`
		doc, ok := parseFeed([]byte(body), base)
		if !ok {
			t.Fatal("the RSS document was not recognised")
		}
		if doc.Title != "Example Blog" {
			t.Errorf("title = %q", doc.Title)
		}
		got := feedLinks(doc.Items)
		want := []string{"https://example.com/posts/one", "https://example.com/posts/two/", "https://example.com/posts/three"}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("links = %v, want %v", got, want)
		}
		if doc.Items[0].Published.IsZero() {
			t.Error("the pubDate was not read")
		}
		if !strings.Contains(doc.Items[0].Content, "Full one") {
			t.Errorf("the content:encoded copy was not read: %q", doc.Items[0].Content)
		}
	})

	t.Run("atom", func(t *testing.T) {
		body := `<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Blog</title>
  <entry><title>One</title><link rel="alternate" type="text/html" href="https://example.com/atom/one"/><updated>2026-10-20T10:00:00Z</updated><content type="html">&lt;p&gt;Full one&lt;/p&gt;</content></entry>
  <entry><title>Two</title><link rel="self" href="https://example.com/self"/><link rel="alternate" href="https://example.com/atom/two"/></entry>
  <entry><title>By id</title><id>https://example.com/atom/three</id></entry>
</feed>`
		doc, ok := parseFeed([]byte(body), base)
		if !ok {
			t.Fatal("the Atom document was not recognised")
		}
		if doc.Title != "Atom Blog" {
			t.Errorf("title = %q", doc.Title)
		}
		got := feedLinks(doc.Items)
		want := []string{"https://example.com/atom/one", "https://example.com/atom/two", "https://example.com/atom/three"}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("links = %v, want %v", got, want)
		}
		if doc.Items[0].Published.IsZero() {
			t.Error("the updated date was not read")
		}
		if !strings.Contains(doc.Items[0].Content, "Full one") {
			t.Errorf("the Atom content was not read: %q", doc.Items[0].Content)
		}
	})

	t.Run("rss 1.0", func(t *testing.T) {
		body := `<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/">
  <channel><title>RDF Blog</title></channel>
  <item><title>One</title><link>https://example.com/rdf/one</link><date>2026-10-20</date></item>
</rdf:RDF>`
		doc, ok := parseFeed([]byte(body), base)
		if !ok {
			t.Fatal("the RDF document was not recognised")
		}
		if doc.Title != "RDF Blog" || len(doc.Items) != 1 || doc.Items[0].Link != "https://example.com/rdf/one" {
			t.Errorf("doc = %+v", doc)
		}
	})

	t.Run("not a feed", func(t *testing.T) {
		for _, body := range []string{
			`<html><body><h1>An article</h1></body></html>`,
			`<urlset><url><loc>https://example.com/a</loc></url></urlset>`,
			`just prose`,
		} {
			if _, ok := parseFeed([]byte(body), base); ok {
				t.Errorf("recognised a feed in %q", body)
			}
		}
	})

	t.Run("empty feed", func(t *testing.T) {
		doc, ok := parseFeed([]byte(`<rss><channel><title>Empty</title></channel></rss>`), base)
		if !ok || len(doc.Items) != 0 {
			t.Errorf("an empty feed must be a valid, empty document: ok=%v items=%d", ok, len(doc.Items))
		}
	})
}

func TestLooksLikeFeed(t *testing.T) {
	for _, ct := range []string{"application/rss+xml", "application/atom+xml; charset=utf-8", "application/rdf+xml"} {
		if !looksLikeFeed(ct) {
			t.Errorf("%q was not recognised", ct)
		}
	}
	for _, ct := range []string{"text/html", "application/xml", "text/plain"} {
		if looksLikeFeed(ct) {
			t.Errorf("%q was taken for a feed", ct)
		}
	}
}
