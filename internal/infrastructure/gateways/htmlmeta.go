package gateways

import (
	"bytes"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/dates"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// pageMeta is the page's declared metadata. It is read straight from the document
// so the title comes from the page's <title>/og:title — not from a heading that
// readability happened to pick.
type pageMeta struct {
	OGTitle     string
	TwitterName string
	TitleTag    string
	OGDesc      string
	Desc        string
	SiteName    string
	Language    string
	IconHref    string
	// Published is the date the page DECLARES for itself — a publication meta tag, an itemprop, or
	// a <time> element — and PublishedFrom the layer that carried it (meta or time). Zero when the
	// page names none; the date under the title is looked for after the conversion, not here.
	Published     time.Time
	PublishedFrom string
}

// Title resolves the best declared title: og:title, then twitter:title, then <title>.
func (m pageMeta) bestTitle() string {
	return firstNonEmpty(m.OGTitle, m.TwitterName, m.TitleTag)
}

// Description resolves the declared description.
func (m pageMeta) bestDescription() string {
	return firstNonEmpty(m.OGDesc, m.Desc)
}

// extractPageMeta walks the document head and collects the metadata tags.
func extractPageMeta(raw []byte) pageMeta {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return pageMeta{}
	}
	var m pageMeta
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "html":
				if m.Language == "" {
					m.Language = strings.TrimSpace(attr(n, "lang"))
				}
			case "title":
				if m.TitleTag == "" && n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
					m.TitleTag = collapseSpace(n.FirstChild.Data)
				}
			case "time":
				// A byline often carries the date as a <time datetime="…">; its machine-readable
				// attribute is the page's own statement, the visible text only its rendering.
				if m.Published.IsZero() {
					value := strings.TrimSpace(attr(n, "datetime"))
					if value == "" && n.FirstChild != nil && n.FirstChild.Type == html.TextNode {
						value = collapseSpace(n.FirstChild.Data)
					}
					if at, ok := dates.Parse(value); ok {
						m.Published, m.PublishedFrom = at, entities.DateFromTime
					}
				}
			case "link":
				rel := strings.ToLower(attr(n, "rel"))
				if m.IconHref == "" && (rel == "icon" || rel == "shortcut icon" || rel == "apple-touch-icon") {
					m.IconHref = strings.TrimSpace(attr(n, "href"))
				}
			case "meta":
				key := strings.ToLower(attr(n, "property"))
				if key == "" {
					key = strings.ToLower(attr(n, "name"))
				}
				itemprop := strings.ToLower(attr(n, "itemprop"))
				content := collapseSpace(attr(n, "content"))
				if content == "" {
					break
				}
				if m.Published.IsZero() && isDateMeta(key, itemprop) {
					if at, ok := dates.Parse(content); ok {
						m.Published, m.PublishedFrom = at, entities.DateFromMeta
					}
					break
				}
				switch key {
				case "og:title":
					if m.OGTitle == "" {
						m.OGTitle = content
					}
				case "twitter:title":
					if m.TwitterName == "" {
						m.TwitterName = content
					}
				case "og:description":
					if m.OGDesc == "" {
						m.OGDesc = content
					}
				case "description":
					if m.Desc == "" {
						m.Desc = content
					}
				case "og:site_name":
					if m.SiteName == "" {
						m.SiteName = content
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return m
}

// isDateMeta reports whether a meta tag declares the page's PUBLICATION date. A modification date
// is deliberately absent: "changed last week" is not "published last week".
func isDateMeta(key, itemprop string) bool {
	switch key {
	case "article:published_time", "og:published_time", "date", "dc.date", "dc.date.issued",
		"dcterms.date", "dcterms.created", "dcterms.issued", "dcterms.available",
		"pubdate", "publish-date", "sailthru.date", "parsely-pub-date",
		"citation_publication_date", "datepublished":
		return true
	}
	switch itemprop {
	case "datepublished", "datecreated":
		return true
	}
	return false
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
