package gateways

import (
	"bytes"
	"context"
	"net/url"
	"strings"

	"golang.org/x/net/html"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/listing"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// maxAnchors bounds one document's link profile. A page with more anchors than this is a directory
// or a generated feed, not a blog index; walking it further would cost memory for nothing.
const maxAnchors = 4000

// groupDepth is how many ancestor tag names make up a card's structural signature. Three is enough
// to tell "div/div/h3" (a title in a card) from "div/ul/li" (a menu row) and does not change when a
// page adds a wrapper.
const groupDepth = 3

// HTMLLinkExtractor reads a page's anchors with x/net/html. Pure Go, no network, no daemon: the
// parser is the same one the EPUB and wall readers use.
type HTMLLinkExtractor struct{}

var _ interfaces.LinkExtractor = HTMLLinkExtractor{}

// Links collects every usable anchor of the document. A link is resolved against the page, its
// fragment is dropped (a fragment is not a page) and its text is whitespace-normalised.
func (HTMLLinkExtractor) Links(_ context.Context, raw []byte, pageURL string) ([]listing.Link, error) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(pageURL) //nolint:errcheck // a broken address only means relative links stay relative

	out := make([]listing.Link, 0, 64)
	var walk func(n *html.Node, stack []string, heading bool)
	walk = func(n *html.Node, stack []string, heading bool) {
		if len(out) >= maxAnchors {
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "template":
				// Code and templates are not content: their text is not a link label.
				return
			case "h1", "h2", "h3", "h4", "h5", "h6":
				heading = true
			}
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			if link, ok := anchorLink(n, stack, heading, base); ok {
				out = append(out, link)
			}
			// The anchor's own children still yield nothing: an <a> inside an <a> is invalid HTML
			// and the parser already reparents it.
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			next := stack
			if n.Type == html.ElementNode && n.Data != "a" {
				next = appendTag(stack, n.Data)
			}
			walk(c, next, heading)
		}
	}
	walk(doc, nil, false)
	return out, nil
}

// anchorLink turns one <a> into a Link, or reports that it is not usable.
func anchorLink(n *html.Node, stack []string, heading bool, base *url.URL) (listing.Link, bool) {
	var href string
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, "href") {
			href = strings.TrimSpace(attr.Val)
		}
	}
	if href == "" || strings.HasPrefix(href, "#") {
		return listing.Link{}, false
	}
	lower := strings.ToLower(href)
	for _, scheme := range []string{"javascript:", "mailto:", "tel:", "data:"} {
		if strings.HasPrefix(lower, scheme) {
			return listing.Link{}, false
		}
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return listing.Link{}, false
	}
	if base != nil {
		parsed = base.ResolveReference(parsed)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return listing.Link{}, false
	}
	parsed.Fragment = ""
	// The page's own query and path are kept; only the fragment is noise.
	link := listing.Link{
		URL:       parsed.String(),
		Label:     strings.TrimSpace(firstAttr(n, "aria-label", "title")),
		InHeading: heading,
		Time:      timeOf(n),
		Group:     strings.Join(lastTags(stack, groupDepth), "/"),
	}
	if rel := strings.TrimSpace(firstAttr(n, "rel")); rel != "" {
		link.Rel = strings.ToLower(rel)
	}
	link.Text = nodeText(n)
	return link, true
}

// nodeText joins the anchor's text with single spaces. The separator matters: many cards put a
// date and a title in sibling elements, and "Oct 27, 2026Title" would hide the date.
func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		switch node.Type {
		case html.TextNode:
			b.WriteString(node.Data)
			b.WriteString(" ")
		case html.ElementNode:
			if node.Data == "script" || node.Data == "style" || node.Data == "template" {
				return
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// timeOf reads a card's date when it declares one with <time> instead of writing it into the text.
func timeOf(n *html.Node) string {
	var found string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found != "" {
			return
		}
		if node.Type == html.ElementNode && node.Data == "time" {
			if datetime := strings.TrimSpace(firstAttr(node, "datetime")); datetime != "" {
				found = datetime
			} else if text := nodeText(node); text != "" {
				found = text
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

func firstAttr(n *html.Node, keys ...string) string {
	for _, attr := range n.Attr {
		for _, key := range keys {
			if strings.EqualFold(attr.Key, key) && strings.TrimSpace(attr.Val) != "" {
				return attr.Val
			}
		}
	}
	return ""
}

func appendTag(stack []string, tag string) []string {
	next := make([]string, len(stack)+1)
	copy(next, stack)
	next[len(stack)] = tag
	return next
}

func lastTags(stack []string, n int) []string {
	if len(stack) <= n {
		return stack
	}
	return stack[len(stack)-n:]
}
