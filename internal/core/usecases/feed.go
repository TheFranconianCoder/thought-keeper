package usecases

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// RSS feeds are a SOURCE OF SOURCES, like a sitemap: the feed itself is one URL source whose snapshot
// is the item list, and every item points at a page that becomes its own URL source. The difference is
// what the list means. A sitemap claims to be complete, so a page that disappears from it is gone; a
// feed is a window on the newest items, so a page that disappears from it is KEPT — the archive of
// what the feed announced is the point.
//
// Supported: RSS 2.0, RSS 1.0 (RDF) and Atom. JSON Feed is deliberately not accepted: the app reads
// XML feeds and the three formats above cover the web.

// feedItem is one page a feed declared. The page is the source; the title and date are only there to
// make the parent's snapshot readable. Content is the item's own copy of the post (RSS
// `content:encoded`, Atom `content`, or the description) — many feeds carry the whole article while
// the page itself is a JavaScript shell.
type feedItem struct {
	Title     string
	Link      string
	Published time.Time
	Content   string
}

// feedDocument is what one feed declared: its own title and its items, in document order.
type feedDocument struct {
	Title string
	Items []feedItem
}

// looksLikeFeed reports whether a response's content type says "this is a feed". It is only a hint: a
// feed served as text/xml or with no type at all is still recognised from its document.
func looksLikeFeed(contentType string) bool {
	contentType = strings.ToLower(contentType)
	for _, marker := range []string{"application/rss+xml", "application/atom+xml", "application/rdf+xml", "application/x-rss+xml"} {
		if strings.Contains(contentType, marker) {
			return true
		}
	}
	return false
}

// parseFeed recognises an RSS, RDF or Atom document and returns the pages it declares, resolved
// against the feed's own address and deduplicated (trailing slashes ignored).
func parseFeed(body []byte, baseURL string) (feedDocument, bool) {
	root := xmlRoot(body)
	if root != "rss" && root != "feed" && root != "RDF" {
		return feedDocument{}, false
	}

	var doc feedDocument
	switch root {
	case "rss":
		var parsed struct {
			Channel struct {
				Title string `xml:"title"`
				Items []struct {
					Title   string `xml:"title"`
					Link    string `xml:"link"`
					GUID    string `xml:"guid"`
					PubDate string `xml:"pubDate"`
					Content string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
					Summary string `xml:"description"`
				} `xml:"item"`
			} `xml:"channel"`
		}
		if err := xml.Unmarshal(body, &parsed); err != nil {
			return feedDocument{}, false
		}
		doc.Title = cleanFeedText(parsed.Channel.Title)
		for _, item := range parsed.Channel.Items {
			doc.Items = append(doc.Items, feedItem{
				Title: cleanFeedText(item.Title), Link: firstNonEmpty(item.Link, feedGUIDLink(item.GUID)),
				Published: parseFeedDate(item.PubDate),
				Content:   firstNonEmpty(item.Content, item.Summary),
			})
		}
	case "feed": // Atom
		var parsed struct {
			Title   string `xml:"title"`
			Entries []struct {
				Title   string `xml:"title"`
				ID      string `xml:"id"`
				Updated string `xml:"updated"`
				Pub     string `xml:"published"`
				Content string `xml:"content"`
				Summary string `xml:"summary"`
				Links   []struct {
					Href string `xml:"href,attr"`
					Rel  string `xml:"rel,attr"`
					Type string `xml:"type,attr"`
				} `xml:"link"`
			} `xml:"entry"`
		}
		if err := xml.Unmarshal(body, &parsed); err != nil {
			return feedDocument{}, false
		}
		doc.Title = cleanFeedText(parsed.Title)
		for _, entry := range parsed.Entries {
			doc.Items = append(doc.Items, feedItem{
				Title: cleanFeedText(entry.Title), Link: atomEntryLink(entry.Links, entry.ID),
				Published: parseFeedDate(firstNonEmpty(entry.Pub, entry.Updated)),
				Content:   firstNonEmpty(entry.Content, entry.Summary),
			})
		}
	case "RDF": // RSS 1.0
		var parsed struct {
			Channel struct {
				Title string `xml:"title"`
			} `xml:"channel"`
			Items []struct {
				Title   string `xml:"title"`
				Link    string `xml:"link"`
				Date    string `xml:"date"`
				PubDate string `xml:"pubDate"`
				Content string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
				Summary string `xml:"description"`
			} `xml:"item"`
		}
		if err := xml.Unmarshal(body, &parsed); err != nil {
			return feedDocument{}, false
		}
		doc.Title = cleanFeedText(parsed.Channel.Title)
		for _, item := range parsed.Items {
			doc.Items = append(doc.Items, feedItem{
				Title: cleanFeedText(item.Title), Link: item.Link,
				Published: parseFeedDate(firstNonEmpty(item.Date, item.PubDate)),
				Content:   firstNonEmpty(item.Content, item.Summary),
			})
		}
	default:
		return feedDocument{}, false
	}

	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return feedDocument{}, false
	}
	self := entities.URLKey(baseURL)
	seen := make(map[string]bool, len(doc.Items))
	out := make([]feedItem, 0, len(doc.Items))
	for _, item := range doc.Items {
		raw := strings.TrimSpace(item.Link)
		if raw == "" {
			continue
		}
		ref, err := url.Parse(raw)
		if err != nil {
			continue
		}
		resolved := base.ResolveReference(ref)
		if resolved.Scheme != "http" && resolved.Scheme != "https" {
			continue
		}
		resolved.Fragment = ""
		value := resolved.String()
		key := entities.URLKey(value)
		if key == self || seen[key] {
			continue
		}
		seen[key] = true
		item.Link = value
		out = append(out, item)
	}
	doc.Items = out
	return doc, true
}

// xmlRoot is the document's root element, read without decoding the whole body: a page that starts
// with <html> is rejected after one token, not after a full parse of five megabytes.
func xmlRoot(body []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if start, ok := tok.(xml.StartElement); ok {
			return start.Name.Local
		}
	}
}

// feedGUIDLink uses a permalink guid as the item's address when the feed has no <link>. A guid that
// is not a URL is an opaque identifier and is ignored.
func feedGUIDLink(guid string) string {
	guid = strings.TrimSpace(guid)
	if strings.HasPrefix(guid, "http://") || strings.HasPrefix(guid, "https://") {
		return guid
	}
	return ""
}

// atomEntryLink prefers rel="alternate" (the page), then any link with an href, then an <id> that is
// itself a URL.
func atomEntryLink(links []struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}, id string,
) string {
	fallback := ""
	for _, link := range links {
		if link.Href == "" {
			continue
		}
		rel := strings.TrimSpace(link.Rel)
		kind := strings.ToLower(strings.TrimSpace(link.Type))
		if rel == "" || rel == "alternate" {
			if kind == "" || strings.Contains(kind, "html") {
				return link.Href
			}
			if fallback == "" {
				fallback = link.Href
			}
		}
	}
	if fallback != "" {
		return fallback
	}
	if strings.HasPrefix(id, "http://") || strings.HasPrefix(id, "https://") {
		return id
	}
	return ""
}

// cleanFeedText collapses a title to one line: a feed is rendered as a Markdown list.
func cleanFeedText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// parseFeedDate reads the date formats the three feed families use. An unknown date is not a reason
// to drop an item: the page is what matters.
func parseFeedDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339, time.RFC3339Nano,
		time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822,
		"Mon, 2 Jan 2006 15:04:05 MST", "Mon, 2 Jan 2006 15:04 MST",
		"2006-01-02T15:04:05Z07:00", "2006-01-02",
	} {
		if at, err := time.Parse(layout, raw); err == nil {
			return at
		}
	}
	return time.Time{}
}

// renderFeedSnapshot is what the feed source shows: its address, how many items it declared, and the
// item list with titles and dates. The pages themselves are separate sources.
func renderFeedSnapshot(rawURL, title string, status int, fetchedAt time.Time, items []feedItem) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "url: %s\n", rawURL)
	fmt.Fprintf(&b, "fetched_at: %s\n", fetchedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "http_status: %d\n", status)
	fmt.Fprintf(&b, "title: %s\n", yamlScalar(title))
	b.WriteString("rss: true\n")
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "%d item%s declared by this feed.\n\n", len(items), pluralSuffix(len(items)))
	for _, item := range items {
		name := item.Title
		if name == "" {
			name = item.Link
		}
		name = strings.NewReplacer("[", "(", "]", ")", "\n", " ").Replace(name)
		if item.Published.IsZero() {
			fmt.Fprintf(&b, "- [%s](%s)\n", name, item.Link)
			continue
		}
		fmt.Fprintf(&b, "- [%s](%s) — %s\n", name, item.Link, item.Published.UTC().Format("2006-01-02"))
	}
	return b.String()
}

// feedTitle names the feed source. The document's own <title> is the feed's name; without one the
// address names it, so two feeds of one site never share a title.
func feedTitle(rawURL, declared string) string {
	if title := cleanFeedText(declared); title != "" {
		return title
	}
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err == nil && u.Host != "" {
		path := strings.TrimSuffix(u.Path, "/")
		if path != "" {
			return "Feed · " + u.Host + path
		}
		return "Feed · " + u.Host
	}
	return "Feed"
}
