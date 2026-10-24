package gateways

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
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
				content := collapseSpace(attr(n, "content"))
				if content == "" {
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
