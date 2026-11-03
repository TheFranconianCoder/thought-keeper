package gateways

import (
	"bytes"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// A web article is not only prose. A page can end with a list of the members who liked it, a grid
// of the author's other books, a tag cloud. Readability keeps those sections, and they are not the
// article: distilling them writes claims about usernames and counts. So the page's RAILS are
// dropped before the article becomes text.
//
// The rule reads like the listing rules, one level up: a rail is a group of repeated, link-shaped
// ROWS — at least minRailRows rows of the same shape, each one a link line rather than a sentence.
// A row must carry an anchor, at most maxRailRowWords words, and at least half of them inside its
// anchors (a member row is five links and six words; a tag line is one link and one count). The
// majority is the evidence, so a list that mixes a few prose rows keeps them.
//
// Card grids survive, and that is not a detail: a card is a link or two and a paragraph of its own,
// so the text is the bulk of the row's words — well below half in links — and a listing detector
// still reads it.
//
// A page whose text would collapse to less than minArticleText is left alone: a link directory is
// still a page, and emptying it is worse than carrying its links.
const (
	minRailRows     = 8
	maxRailRowWords = 60
	minArticleText  = 200
)

// stripLinkRails returns the article's HTML without its link rails. The input is not modified; the
// caller decides whether the result is worth keeping (see minArticleText).
func stripLinkRails(articleHTML string) string {
	doc, err := html.Parse(strings.NewReader(articleHTML))
	if err != nil {
		return articleHTML
	}
	if !dropRailRows(doc) {
		return articleHTML
	}
	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return articleHTML
	}
	return buf.String()
}

// dropRailRows removes the rail rows it finds and reports whether it removed any. Sibling rows of
// one shape are weighed together: eight identical rows are a layout, two are a coincidence.
func dropRailRows(n *html.Node) bool {
	removed := false
	groups := make(map[string][]*html.Node)
	order := make([]string, 0, 8)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != html.ElementNode {
			continue
		}
		shape := rowShape(child)
		if _, known := groups[shape]; !known {
			order = append(order, shape)
		}
		groups[shape] = append(groups[shape], child)
	}
	for _, shape := range order {
		rows := groups[shape]
		if len(rows) < minRailRows {
			continue
		}
		rails := 0
		for _, row := range rows {
			if isRailRow(row) {
				rails++
			}
		}
		if rails*10 < len(rows)*6 {
			continue
		}
		for _, row := range rows {
			if !isRailRow(row) {
				continue
			}
			n.RemoveChild(row)
			removed = true
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && dropRailRows(child) {
			removed = true
		}
	}
	return removed
}

// isRailRow reports whether one row of a repeated block is a link line rather than a sentence.
func isRailRow(n *html.Node) bool {
	if n.Data == "a" {
		return false
	}
	marks := countMarks(n)
	if marks.anchors == 0 || marks.total == 0 || marks.total > maxRailRowWords {
		return false
	}
	return marks.link*2 >= marks.total
}

// rowShape is a row's structural signature: its tag and its class tokens, sorted. The siblings of
// one rail share it; a class per row (`user-1234`) would split them, which is why the tokens are
// compared as a set only within the same parent.
func rowShape(n *html.Node) string {
	classes := make([]string, 0, 2)
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, "class") {
			classes = strings.Fields(attr.Val)
		}
	}
	sort.Strings(classes)
	return n.Data + "." + strings.Join(classes, ".")
}

// marks is the text profile of a subtree.
type marks struct {
	// total is how many words the block carries (a token with a letter or a digit).
	total int
	// link is how many of those words sit inside an anchor.
	link int
	// anchors is how many anchors the block holds.
	anchors int
}

// countMarks counts a subtree's words, its anchor words and its anchors.
func countMarks(n *html.Node) marks {
	var walk func(node *html.Node, inLink bool, out *marks)
	walk = func(node *html.Node, inLink bool, out *marks) {
		switch node.Type {
		case html.TextNode:
			words := meaningfulWords(node.Data)
			out.total += words
			if inLink {
				out.link += words
			}
			return
		case html.ElementNode:
			switch node.Data {
			case "script", "style", "template", "noscript":
				return
			case "a":
				out.anchors++
			}
		default:
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inLink || node.Data == "a", out)
		}
	}
	out := marks{}
	walk(n, false, &out)
	return out
}

// meaningfulWords counts the tokens of a text node that carry a letter or a digit: a comma between
// two tags is not a word, and counting it would make a rail look like prose.
func meaningfulWords(text string) int {
	n := 0
	for token := range strings.FieldsSeq(text) {
		for _, r := range token {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				n++
				break
			}
		}
	}
	return n
}

// railTextIsSubstantial reports whether text is long enough to be the article. It is the guard that
// keeps a page of nothing but links from being emptied: a directory is still a page.
func railTextIsSubstantial(text string) bool {
	return utf8.RuneCountInString(strings.TrimSpace(text)) >= minArticleText
}
