package gateways

import (
	"bytes"
	"context"
	"net/url"
	"slices"
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

// cardDepth is how far up an anchor with no text of its own looks for the block that renders its
// card. One level covers the common shape (the link sits beside the card body, or inside the card's
// own wrapper); more would let a whole section lend its heading to every link in it.
const cardDepth = 2

// cardMarker is appended to the group of an overlay link (see cardOf). Two anchors that sit in the
// same tag chain are still different things: an overlay link is a card, an inline link is prose.
const cardMarker = "@"

// navTags and navRoles name the regions whose links are navigation, never article cards: a mega-menu
// renders a dozen tiles with headings and dates, and a page whose readable text is thin (the shell
// of a JavaScript site) would otherwise look like a listing of its own menu.
var navTags = map[string]bool{"nav": true, "header": true, "footer": true}

var navRoles = map[string]bool{"navigation": true, "menu": true, "menubar": true, "search": true}

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
	var walk func(n *html.Node, ancestors []*html.Node, heading, nav bool)
	walk = func(n *html.Node, ancestors []*html.Node, heading, nav bool) {
		if len(out) >= maxAnchors {
			return
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "template":
				// Code and templates are not content: their text is not a link label.
				return
			}
			if isHeading(n) {
				heading = true
			}
			if navRegion(n) {
				nav = true
			}
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			if link, ok := anchorLink(n, ancestors, heading, nav, base); ok {
				out = append(out, link)
			}
			// The anchor's own children still yield nothing: an <a> inside an <a> is invalid HTML
			// and the parser already reparents it.
		}
		// The document's own pager declaration lives in the head and is not an anchor at all
		// (`<link rel="next" href="…">`). A site that writes it there and words its visible pager
		// with anything the vocabulary does not know (fs.blog: "See older articles") would otherwise
		// be read as a single page.
		if n.Type == html.ElementNode && n.Data == "link" {
			if link, ok := headNext(n, base); ok {
				out = append(out, link)
			}
		}
		next := ancestors
		if n.Type == html.ElementNode && n.Data != "a" {
			next = appendNode(ancestors, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, next, heading, nav)
		}
	}
	walk(doc, nil, false, false)
	return out, nil
}

// anchorLink turns one <a> into a Link, or reports that it is not usable.
func anchorLink(n *html.Node, ancestors []*html.Node, heading, nav bool, base *url.URL) (listing.Link, bool) {
	var href string
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, "href") {
			href = strings.TrimSpace(attr.Val)
		}
	}
	if href == "" || strings.HasPrefix(href, "#") {
		return listing.Link{}, false
	}
	resolved, ok := absoluteURL(href, base)
	if !ok {
		return listing.Link{}, false
	}
	// The page's own query and path are kept; only the fragment is noise.
	link := listing.Link{
		URL:       resolved,
		Label:     strings.TrimSpace(firstAttr(n, "aria-label", "title")),
		InHeading: heading,
		InNav:     nav,
		Time:      timeOf(n),
		Group:     strings.Join(lastTags(ancestors, groupDepth), "/"),
	}
	if rel := strings.TrimSpace(firstAttr(n, "rel")); rel != "" {
		link.Rel = strings.ToLower(rel)
	}
	link.Text = nodeText(n)
	// An anchor with no text of its OWN is an overlay: the block around it draws the card and the
	// link is the invisible tile over it (`<a class="…-absolute"></a>` next to the card body). Its
	// title, date and signature come from that block — otherwise the whole grid is invisible to the
	// rules, which is what a page of 20 perfect cards with 0 candidates looked like.
	if link.Text == "" {
		if card, title := cardOf(ancestors); card != nil {
			link.Text = cardText(card)
			if title != "" {
				link.Card = true
				if link.Label == "" {
					link.Label = title
				}
			}
			if link.Time == "" {
				link.Time = timeOf(card)
			}
			link.Group += cardMarker
		}
	}
	return link, true
}

// absoluteURL resolves one href against the page and drops the fragment. It reports false for an
// address that is not a page: a fragment, a script, a mail address, a file.
func absoluteURL(href string, base *url.URL) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return "", false
	}
	lower := strings.ToLower(href)
	for _, scheme := range []string{"javascript:", "mailto:", "tel:", "data:"} {
		if strings.HasPrefix(lower, scheme) {
			return "", false
		}
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	if base != nil {
		parsed = base.ResolveReference(parsed)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	parsed.Fragment = ""
	return parsed.String(), true
}

// headNext reads the document's declaration of its next page: `<link rel="next" href="…">` in the
// head. It carries no text and no card, so it is only ever a pager — the listing rules filter it out
// of a group and NextPage takes it first.
func headNext(n *html.Node, base *url.URL) (listing.Link, bool) {
	if !slices.Contains(strings.Fields(strings.ToLower(firstAttr(n, "rel"))), "next") {
		return listing.Link{}, false
	}
	href, ok := absoluteURL(firstAttr(n, "href"), base)
	if !ok {
		return listing.Link{}, false
	}
	return listing.Link{URL: href, Rel: "next"}, true
}

// cardOf finds the block that renders an overlay link's card: the nearest ancestor that declares a
// heading (the card's title) or a date. It returns the block and the heading's text.
func cardOf(ancestors []*html.Node) (*html.Node, string) {
	for i := len(ancestors) - 1; i >= 0 && len(ancestors)-i <= cardDepth; i-- {
		card := ancestors[i]
		if heading := firstHeading(card); heading != nil {
			return card, nodeText(heading)
		}
		if timeOf(card) != "" {
			return card, ""
		}
	}
	return nil, ""
}

// firstHeading is the first heading (h1–h6 or `role="heading"`) of a block, in document order.
func firstHeading(n *html.Node) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found != nil {
			return
		}
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style", "template":
				return
			}
			if isHeading(node) {
				found = node
				return
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return found
}

// cardText is a card's text without the anchors inside it: an overlay bar's own label is not the
// card's title, and a second card nested in the first must not lend its words.
func cardText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		switch node.Type {
		case html.TextNode:
			b.WriteString(node.Data)
			b.WriteString(" ")
			return
		case html.ElementNode:
			switch node.Data {
			case "script", "style", "template", "a":
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

// navRegion reports whether an element is navigation or boilerplate: the regions whose links are
// never article cards.
func navRegion(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if navTags[n.Data] {
		return true
	}
	for _, attr := range n.Attr {
		switch strings.ToLower(attr.Key) {
		case "role":
			for role := range strings.FieldsSeq(strings.ToLower(attr.Val)) {
				if navRoles[role] {
					return true
				}
			}
		case "aria-hidden":
			if strings.EqualFold(strings.TrimSpace(attr.Val), "true") {
				return true
			}
		}
	}
	return false
}

// isHeading reports whether an element is a heading: an h1–h6 OR the ARIA role. A card component
// built by a script marks its title `role="heading"` instead of using an h tag — the Coveo result
// card of a blog index does exactly that — and a title is a title whichever way it is declared.
func isHeading(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, "role") &&
			slices.Contains(strings.Fields(strings.ToLower(attr.Val)), "heading") {
			return true
		}
	}
	return false
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

func appendNode(stack []*html.Node, n *html.Node) []*html.Node {
	next := make([]*html.Node, len(stack)+1)
	copy(next, stack)
	next[len(stack)] = n
	return next
}

func lastTags(stack []*html.Node, n int) []string {
	if len(stack) > n {
		stack = stack[len(stack)-n:]
	}
	out := make([]string, 0, len(stack))
	for _, node := range stack {
		out = append(out, node.Data)
	}
	return out
}
