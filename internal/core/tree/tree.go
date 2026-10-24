// Package tree shapes extracted parts into a hierarchical node tree (a PageIndex
// style table of contents). It is pure: no IO, no dependencies beyond the stdlib.
package tree

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// boilerplatePhrases mark site chrome rather than content. A URL snapshot regularly ends with
// "Thanks for your feedback!", "Leave a Reply" or a cookie notice — atomizing those produces
// claims nobody asked for. Only SHORT parts are dropped: a long one is real content.
var boilerplatePhrases = []string{
	"thanks for your feedback", "leave a reply", "post navigation", "related posts",
	"you might also like", "recommended for you", "subscribe to", "advertisement",
	"cookie", "privacy policy", "terms of service", "share this", "follow us",
	"about the author", "your email address will not be published",
	// A publisher's boilerplate page often carries no heading at all — this is the copyright page of
	// a book, which is short by nature (the phrase rule only ever applies to short parts).
	"all rights reserved", "no part of this publication", "isbn", "first published",
	"printed and bound", "typeset in", "a catalogue record for this book",
}

// markdownLink matches a Markdown link target, including a bracketed label.
var (
	markdownLink     = regexp.MustCompile(`\[[^\]]*\]\([^)]*\)`)
	navigationMarker = regexp.MustCompile(`[0-9]+[.)]|[-*+]|>`)
)

// IsNavigationText reports a link-only block: a breadcrumb or menu rendered as a list of Markdown
// links (for example "1. [Documentation](...) 2. [A Guide](...)"). It is the page's chrome, not a
// text part. The test is deliberately narrow: at least two links, no prose left after removing the
// links and list markers, and a short block.
func IsNavigationText(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" || len(t) > 500 {
		return false
	}
	if len(markdownLink.FindAllString(t, -1)) < 2 {
		return false
	}
	rest := markdownLink.ReplaceAllString(t, "")
	rest = navigationMarker.ReplaceAllString(rest, "")
	rest = strings.Trim(rest, " \t\r\n.,;:|-–—•/")
	return rest == ""
}

// IsBoilerplate reports whether a part is site chrome instead of content.
func IsBoilerplate(s entities.Part) bool {
	text := strings.TrimSpace(s.Text)
	if len(text) > 400 {
		return false
	}
	title := strings.ToLower(strings.TrimSpace(s.Title))
	body := strings.ToLower(text)
	for _, p := range boilerplatePhrases {
		if strings.Contains(title, p) || strings.Contains(body, p) {
			return true
		}
	}
	return false
}

// Build turns parts in document order into a flat, pre-ordered node list. Node
// ids are hierarchical dotted paths ("0", "0.1", "0.1.2"). The root is always "0".
func Build(parts []entities.Part) []entities.SourceNode {
	nodes := []entities.SourceNode{{NodeID: "0", Depth: 0, Ord: 0}}

	type frame struct {
		level int
		index int
	}
	stack := []frame{{level: 0, index: 0}}
	children := map[string]int{}

	for _, s := range parts {
		if strings.TrimSpace(s.Text) == "" && strings.TrimSpace(s.Title) == "" {
			continue
		}
		if IsBoilerplate(s) || IsMatter(s) || IsPageMatter(s) || (strings.TrimSpace(s.Title) == "" && IsNavigationText(s.Text)) {
			continue
		}
		level := max(s.Level, 1)
		for len(stack) > 1 && stack[len(stack)-1].level >= level {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]
		parentID := nodes[parent.index].NodeID
		children[parentID]++
		id := parentID + "." + strconv.Itoa(children[parentID])

		// The part's text is already exactly what a node carries (the parse trims it, and the
		// reference describes THAT slice) — trimming again here would move the text away from its range.
		text := s.Text
		nodes = append(nodes, entities.SourceNode{
			NodeID:    id,
			ParentID:  parentID,
			Depth:     nodes[parent.index].Depth + 1,
			Ord:       len(nodes),
			Title:     strings.TrimSpace(s.Title),
			Summary:   preview(text),
			StartPage: s.Page,
			EndPage:   s.Page,
			Text:      text,
			TextStart: s.TextStart,
			TextEnd:   s.TextEnd,
			TextHash:  textHash(text),
			HasText:   text != "",
			OCR:       s.OCR,
		})
		stack = append(stack, frame{level: level, index: len(nodes) - 1})
	}

	propagatePages(nodes)
	return nodes
}

// preview is the node's opening text, capped at a readable length. It is NOT a model-written
// summary: the outline step does not spend a model call per node. It gives the tree, the source
// search and a reader a glance at what the part says, deterministically and at no cost.
func preview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	const max = 240
	if len(text) <= max {
		return text
	}
	cut := text[:max]
	if at := strings.LastIndexByte(cut, ' '); at > max/2 {
		cut = cut[:at]
	}
	return cut + "…"
}

// textHash is the hash of a node's text: what the index stores and what tells a rebuilt tree apart
// from the one it replaces. The text itself is not part of the outline.
func textHash(text string) string {
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// propagatePages rolls each node's page range up into its ancestors so a parent
// spans its children, and a page-less node inherits its first descendant's page.
func propagatePages(nodes []entities.SourceNode) {
	for i := len(nodes) - 1; i > 0; i-- {
		n := nodes[i]
		p := indexOf(nodes, n.ParentID)
		if p < 0 {
			continue
		}
		if nodes[p].StartPage == 0 && n.StartPage > 0 {
			nodes[p].StartPage = n.StartPage
		}
		if n.StartPage > 0 && (nodes[p].StartPage == 0 || n.StartPage < nodes[p].StartPage) {
			nodes[p].StartPage = n.StartPage
		}
		if n.EndPage > nodes[p].EndPage {
			nodes[p].EndPage = n.EndPage
		}
	}
}

func indexOf(nodes []entities.SourceNode, id string) int {
	for i := range nodes {
		if nodes[i].NodeID == id {
			return i
		}
	}
	return -1
}
