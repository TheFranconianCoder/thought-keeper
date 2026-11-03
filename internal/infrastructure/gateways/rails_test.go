package gateways

import (
	"fmt"
	"strings"
	"testing"
)

// A page of one paragraph, then ninety member rows that are nothing but links and counts. The rows
// are furniture; the paragraph is the page.
func TestStripLinkRailsDropsRepeatedLinkRows(t *testing.T) {
	var rows strings.Builder
	for i := range 9 {
		fmt.Fprintf(&rows, `<div class="member"><p><a href="/user/%d">Member %d</a>
			<br/><span><a href="/user/%d/shelf">%d books</a></span>
			<br/><a href="/user/%d/quotes">view quotes</a></p></div>`, i, i, i, i+10, i)
	}
	article := `<div class="article"><h1>“Two things are infinite.”</h1>
		<p>― Albert Einstein</p>` + rows.String() + `</div>`

	stripped := stripLinkRails(article)
	if strings.Contains(stripped, "view quotes") {
		t.Fatalf("the member rows are still there:\n%s", stripped)
	}
	if !strings.Contains(stripped, "Two things are infinite") {
		t.Fatalf("the article was dropped with the rails:\n%s", stripped)
	}
}

// A card grid is not a rail: each card is a title link and a paragraph of its own, so the anchor
// words are a small part of it. The listing detector reads these rows.
func TestStripLinkRailsKeepsACardGrid(t *testing.T) {
	var cards strings.Builder
	for i := range 9 {
		fmt.Fprintf(&cards, `<article class="card"><h2><a href="/post-%d">The title of post %d</a></h2>
			<p>An excerpt of three sentences that says what the post is about, in the voice of the
			writer rather than in a list of links, which is what makes it prose.</p></article>`, i, i)
	}
	article := `<div class="grid">` + cards.String() + `</div>`

	stripped := stripLinkRails(article)
	if !strings.Contains(stripped, "The title of post 8") {
		t.Fatalf("a card grid was taken for a rail:\n%s", stripped)
	}
}

// A prose row with one link is a list, not a rail: a table of contents of a documentation page.
func TestStripLinkRailsKeepsProseRows(t *testing.T) {
	var rows strings.Builder
	for i := range 9 {
		fmt.Fprintf(&rows, `<li><a href="/chapter-%d">Chapter %d</a> explains how the pipeline reads
			a document, what a part is and why the steps may never overlap.</li>`, i, i)
	}
	article := `<ul>` + rows.String() + `</ul>`

	stripped := stripLinkRails(article)
	if !strings.Contains(stripped, "Chapter 8") {
		t.Fatalf("a prose list was taken for a rail:\n%s", stripped)
	}
}

// Five rows are a "related posts" rail, and eight is a layout. Below the threshold nothing is
// removed: the rule must not eat a page that lists a handful of links.
func TestStripLinkRailsNeedsEnoughRows(t *testing.T) {
	var rows strings.Builder
	for i := range 5 {
		fmt.Fprintf(&rows, `<div class="member"><a href="/user/%d">Member %d</a> <a href="/x/%d">12 books</a></div>`, i, i, i)
	}
	article := `<div>` + rows.String() + `</div>`

	if stripped := stripLinkRails(article); stripped != article {
		t.Fatalf("five rows were removed:\n%s", stripped)
	}
}

// The guard: a page that is nothing but link rows must not be emptied into a bookmark.
func TestRailTextIsSubstantial(t *testing.T) {
	if railTextIsSubstantial("") {
		t.Error("an empty text is not an article")
	}
	if railTextIsSubstantial(strings.Repeat("a", minArticleText-1)) {
		t.Error("a text below the floor is not an article")
	}
	if !railTextIsSubstantial(strings.Repeat("a", minArticleText)) {
		t.Error("a text at the floor is an article")
	}
}
