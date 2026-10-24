package extract

import (
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/tree"
)

// TestStripPageFurnitureDropsNumbersAndRunningHeads: a page number and a running head are not the
// document's text. `pdftotext -layout` hands both over on every page, and without the filter they
// become part of a source node — and therefore a claim.
func TestStripPageFurnitureDropsNumbersAndRunningHeads(t *testing.T) {
	pages := [][]string{
		{"Concurrency in Practice      42", "1 Introduction", "The cache is safe only when the key covers every input.", "42"},
		{"Concurrency in Practice      43", "1.1 Motivation", "Prose on page two.", "43"},
		{"Concurrency in Practice      44", "1.2 Background", "More prose.", "44"},
		{"Concurrency in Practice      45", "2 Results", "Result prose.", "45"},
	}
	kept, dropped := stripPageFurniture(pages)

	if dropped != 8 {
		t.Errorf("dropped %d lines, want 8 (a head and a number on each of four pages)", dropped)
	}
	for _, page := range kept {
		for _, line := range page {
			if strings.Contains(line, "Concurrency in Practice") {
				t.Errorf("the running head survived: %q", line)
			}
			if strings.TrimSpace(line) == "42" || strings.TrimSpace(line) == "45" {
				t.Errorf("a page number survived: %q", line)
			}
		}
	}
	if len(kept[0]) != 2 || kept[0][0] != "1 Introduction" {
		t.Errorf("page 1 = %q, want the heading and its prose", kept[0])
	}
}

// TestStripPageFurnitureLeavesTheBodyAlone: the filter must never eat content. Prose that repeats
// away from the band is text, a line in a two-page document cannot be "repeated furniture", and a
// heading below the band survives.
func TestStripPageFurnitureLeavesTheBodyAlone(t *testing.T) {
	pages := [][]string{
		{
			"Concurrency in Practice 41",                        // running head: repeats → dropped
			"An opening sentence about the cache.",              // band, unique → kept
			"A second sentence on this page.",                   // band, unique → kept
			"1.1 A heading on page 41",                          // MIDDLE → kept
			"Cache invalidation is one of the two hard things.", // MIDDLE and repeated → kept
			"Closing words about eviction.",                     // outside the band → kept
			"41",                                                // page number → dropped
			"xii",                                               // roman page number → dropped
		},
		{
			"Concurrency in Practice 42",
			"A different opening entirely.",
			"Another remark here.",
			"1.2 A heading on page 42",
			"Cache invalidation is one of the two hard things.",
			"Closing words about eviction.",
			"42",
			"xiii",
		},
		{
			"Concurrency in Practice 43",
			"Yet another opener for the third page.",
			"Something else again.",
			"1.3 A heading on page 43",
			"Cache invalidation is one of the two hard things.",
			"Closing words about eviction.",
			"43",
			"xiv",
		},
	}
	kept, dropped := stripPageFurniture(pages)

	if dropped != 9 {
		t.Errorf("dropped %d lines, want 9 (head, number and roman number on each of three pages)", dropped)
	}
	for i, lines := range kept {
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "Cache invalidation is one of the two hard things.") {
			t.Errorf("page %d: a repeated body line was dropped:\n%s", i+1, joined)
		}
		if !strings.Contains(joined, "A heading on page ") {
			t.Errorf("page %d: a heading was dropped:\n%s", i+1, joined)
		}
		if !strings.Contains(joined, "Closing words about eviction.") {
			t.Errorf("page %d: a repeated line outside the band was dropped:\n%s", i+1, joined)
		}
		if strings.Contains(joined, "Concurrency in Practice") {
			t.Errorf("page %d: the running head survived:\n%s", i+1, joined)
		}
		if strings.Contains(joined, "xii") || strings.Contains(joined, "xiv") {
			t.Errorf("page %d: a page number survived:\n%s", i+1, joined)
		}
	}
	if len(kept[0]) != 5 {
		t.Errorf("page 1 = %q, want the five text lines", kept[0])
	}

	// Two pages: nothing is "repeated on many pages" yet.
	two := [][]string{
		{"Introduction", "Body one."},
		{"Introduction", "Body two."},
	}
	kept, dropped = stripPageFurniture(two)
	if dropped != 0 || kept[1][0] != "Introduction" {
		t.Errorf("a two-page document lost a line: dropped=%d kept=%q", dropped, kept[1])
	}
}

// TestStripPageFurnitureForms: the shapes a page number comes in, in English and German.
func TestStripPageFurnitureForms(t *testing.T) {
	furniture := []string{"42", "42.", "- 42 -", "[42]", "| 42 |", "42 / 500", "Page 42", "page 42 of 500", "Seite 42", "———————", "•••"}
	for _, line := range furniture {
		if !isFurnitureLine(line, false) {
			t.Errorf("%q was not recognised as page furniture", line)
		}
	}
	content := []string{"1 Introduction", "2.3 The memory model", "Caching is hard.", "42 claims were checked", "The page number 42 is wrong.", "mm"}
	for _, line := range content {
		if isFurnitureLine(line, false) {
			t.Errorf("%q was dropped as page furniture", line)
		}
	}
	// The roman-numeral rule only applies inside the top/bottom band, and only for a real numeral.
	if !isFurnitureLine("xii", true) || !isFurnitureLine("IV", true) {
		t.Error("a roman page number in the band was not recognised")
	}
	if isFurnitureLine("mm", true) || isFurnitureLine("cc", true) {
		t.Error("a unit in the band was dropped as a roman numeral")
	}
	if isFurnitureLine("xii", false) {
		t.Error("a roman line in the middle of a page is content")
	}
}

// TestHeadingCandidate: the shapes that count as a heading — and the ones that must not.
func TestHeadingCandidate(t *testing.T) {
	headings := []struct {
		line  string
		level int
	}{
		{"1 Introduction", 1},
		{"2.3 The memory model", 2},
		{"3.1.4 Detail", 3},
		{"1.2.3.4.5 Too deep", 4},
		{"Chapter 3: Cache lines", 1},
		{"Kapitel 2.1 Grundlagen", 1},
		{"Appendix B", 1},
		{"RESULTS AND DISCUSSION", 2},
		{"CONCLUSION", 2},
	}
	for _, h := range headings {
		level, title, ok := headingCandidate(h.line)
		if !ok || level != h.level || title != h.line {
			t.Errorf("headingCandidate(%q) = (%d, %q, %v), want level %d", h.line, level, title, ok, h.level)
		}
	}

	notHeadings := []string{
		"5.5 is the score of the first match",
		"The cache is safe only when the key covers every input.",
		"",
		"   ",
		strings.Repeat("a very long line of prose that keeps going and going ", 3),
		"Body text that happens to be here.",
		"A short line",
		"TCP",
		"and then it continued",
	}
	for _, line := range notHeadings {
		if _, _, ok := headingCandidate(line); ok {
			t.Errorf("headingCandidate(%q) called it a heading", line)
		}
	}
}

// TestSectionsFromPagesKeepsEveryLine: the heuristic may split a page, but it may never lose a line
// of it, and a page without headings keeps the page-per-node shape it always had.
func TestSectionsFromPagesKeepsEveryLine(t *testing.T) {
	pages := []pageText{
		// Prose BEFORE the first heading: it becomes the page's own part, or it would be lost.
		{no: 7, lines: []string{"Continuation prose from page six.", "2.4 Headings", "After the heading."}},
		{no: 8, lines: []string{"", ""}},
	}
	secs := partsFromPages(pages)

	if len(secs) != 3 {
		t.Fatalf("parts = %d, want 3: %+v", len(secs), secs)
	}
	if secs[0].Title != "Page 7" || secs[0].Text != "Continuation prose from page six." {
		t.Errorf("the leading prose got a wrong part: %+v", secs[0])
	}
	if secs[1].Title != "2.4 Headings" || secs[1].Level != 2 || secs[1].Text != "After the heading." {
		t.Errorf("the heading part is wrong: %+v", secs[1])
	}
	// The empty page stays visible instead of disappearing from the outline.
	if secs[2].Title != "Page 8" || secs[2].Text != "" || secs[2].Page != 8 {
		t.Errorf("the empty page is not represented: %+v", secs[2])
	}
}

// TestSectionsFromPagesBuildsAHierarchy: with headings the tree is a real outline, not "Page 1…N".
func TestSectionsFromPagesBuildsAHierarchy(t *testing.T) {
	pages := []pageText{
		{no: 1, lines: []string{"1 Introduction", "Body one.", "1.1 Motivation", "Why it matters."}},
		{no: 2, lines: []string{"2 Results", "Numbers."}},
	}
	nodes := tree.Build(partsFromPages(pages))

	// root, chapter 1, its sub-part, chapter 2
	if len(nodes) != 4 {
		for _, n := range nodes {
			t.Logf("%s depth=%d title=%q", n.NodeID, n.Depth, n.Title)
		}
		t.Fatalf("nodes = %d, want 4", len(nodes))
	}
	if nodes[1].Title != "1 Introduction" || nodes[1].Depth != 1 {
		t.Errorf("chapter = %+v, want depth 1", nodes[1])
	}
	if nodes[2].Title != "1.1 Motivation" || nodes[2].Depth != 2 || nodes[2].ParentID != nodes[1].NodeID {
		t.Errorf("sub-part = %+v, want a child of %s", nodes[2], nodes[1].NodeID)
	}
	if nodes[3].Title != "2 Results" || nodes[3].ParentID != "0" {
		t.Errorf("second chapter = %+v, want a child of the root", nodes[3])
	}
	// The chapter's page range spans its children (it was built from page 1).
	if nodes[1].StartPage != 1 {
		t.Errorf("chapter start page = %d, want 1", nodes[1].StartPage)
	}
}
