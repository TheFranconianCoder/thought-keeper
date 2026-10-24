package tree

import (
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

func TestBuildNestsByLevel(t *testing.T) {
	nodes := Build([]entities.Part{
		{Level: 1, Title: "A", Text: "a1"},
		{Level: 2, Title: "A.1", Text: "a11"},
		{Level: 2, Title: "A.2", Text: "a12"},
		{Level: 1, Title: "B", Text: "b1"},
	})

	if len(nodes) != 5 {
		t.Fatalf("nodes = %d, want 5 (root + 4)", len(nodes))
	}
	if nodes[0].NodeID != "0" || nodes[0].Depth != 0 {
		t.Errorf("root = %+v", nodes[0])
	}
	want := []struct {
		id, parent string
		depth      int
	}{
		{"0.1", "0", 1},
		{"0.1.1", "0.1", 2},
		{"0.1.2", "0.1", 2},
		{"0.2", "0", 1},
	}
	for i, w := range want {
		n := nodes[i+1]
		if n.NodeID != w.id || n.ParentID != w.parent || n.Depth != w.depth {
			t.Errorf("node %d = {%s parent %s depth %d}, want {%s parent %s depth %d}",
				i, n.NodeID, n.ParentID, n.Depth, w.id, w.parent, w.depth)
		}
		if n.Ord != i+1 {
			t.Errorf("node %d ord = %d, want %d", i, n.Ord, i+1)
		}
	}
}

func TestBuildPropagatesPages(t *testing.T) {
	nodes := Build([]entities.Part{
		{Level: 1, Title: "Doc", Page: 1},
		{Level: 2, Title: "S1", Page: 2},
		{Level: 2, Title: "S2", Page: 5},
	})
	root := nodes[0]
	if root.StartPage != 1 || root.EndPage != 5 {
		t.Errorf("root pages = %d..%d, want 1..5", root.StartPage, root.EndPage)
	}
	doc := nodes[1]
	if doc.StartPage != 1 || doc.EndPage != 5 {
		t.Errorf("doc pages = %d..%d, want 1..5", doc.StartPage, doc.EndPage)
	}
}

func TestBuildSkipsEmptySections(t *testing.T) {
	nodes := Build([]entities.Part{
		{Level: 1, Title: "", Text: "   "},
		{Level: 1, Title: "Kept", Text: "x"},
	})
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2 (root + 1)", len(nodes))
	}
	if nodes[1].Title != "Kept" {
		t.Errorf("kept node = %+v", nodes[1])
	}
}

// TestBoilerplateSectionsAreDropped: site chrome ("Thanks for your feedback!", cookie
// notices) must not become nodes — and therefore not become atoms.
func TestBoilerplateSectionsAreDropped(t *testing.T) {
	nodes := Build([]entities.Part{
		{Level: 1, Title: "C++ Applications", Text: "Real content about C++."},
		{Level: 1, Title: "Thanks for your feedback!", Text: "Thanks for your feedback!"},
		{Level: 1, Title: "Cookie Notice", Text: "We use cookies to improve your experience."},
	})
	var titles []string
	for _, n := range nodes {
		if n.NodeID == "0" {
			continue
		}
		titles = append(titles, n.Title)
	}
	if len(titles) != 1 || titles[0] != "C++ Applications" {
		t.Errorf("tree contains boilerplate: %v", titles)
	}
	if !IsBoilerplate(entities.Part{Title: "Leave a Reply"}) {
		t.Error("Leave a Reply should count as boilerplate")
	}
	if IsBoilerplate(entities.Part{Title: "Caching", Text: strings.Repeat("real content ", 60)}) {
		t.Error("a long part must never be dropped as boilerplate")
	}
}

// TestBackMatterSectionsAreDropped: the pages a published book carries AROUND its content — the
// acknowledgements that thank the family, the dedication, the copyright page, the table of contents,
// the index, the bibliography — are not knowledge about the subject. They are dropped at tree build,
// so they never become claims, and (unlike site chrome) an acknowledgements part is dropped at any
// length: it is acknowledgements all the way through.
func TestBackMatterSectionsAreDropped(t *testing.T) {
	thanks := "I thank my family " + strings.Repeat("for their patience and support, ", 40) // long on purpose
	nodes := Build([]entities.Part{
		{Level: 1, Title: "1 Introduction", Text: "Real content about concurrency."},
		{Level: 1, Title: "Acknowledgements", Text: thanks},
		{Level: 1, Title: "ACKNOWLEDGMENTS", Text: "Thanks to everyone."},
		{Level: 1, Title: "Dedication", Text: "For Anna."},
		{Level: 1, Title: "Copyright", Text: "© 2020, ISBN 978-1-234-56789-0. All rights reserved."},
		{Level: 1, Title: "Table of Contents", Text: "1 Introduction .......... 1\n2 Memory .......... 40"},
		{Level: 1, Title: "Index", Text: "cache, 12, 88 — invalidation, 90"},
		{Level: 1, Title: "Bibliography", Text: "Knuth 1997. The Art of Computer Programming."},
		{Level: 1, Title: "17. Danksagung (Fortsetzung)", Text: "Auch meinen Kolleginnen."},
		{Level: 1, Title: "Über den Autor", Text: "Der Autor arbeitet als Entwickler."},
		{Level: 1, Title: "2 Results", Text: "Real content about throughput."},
	})
	for _, n := range nodes {
		if n.NodeID == "0" {
			continue // the root, not a part
		}
		switch n.Title {
		case "1 Introduction", "2 Results":
		default:
			t.Errorf("front/back matter survived as a node: %q", n.Title)
		}
	}
	if len(nodes) != 3 {
		t.Errorf("nodes = %d, want the root and the two content parts", len(nodes))
	}

	if !IsMatter(entities.Part{Title: "Acknowledgments"}) {
		t.Error("Acknowledgments should count as back matter")
	}
	if !IsMatter(entities.Part{Title: "  Widmung  "}) {
		t.Error("Widmung should count as back matter, trimmed")
	}
	if got := DroppedTitles([]entities.Part{{Title: "Acknowledgements", Text: thanks}, {Title: "Praise"}}); len(got) != 2 {
		t.Errorf("DroppedTitles = %v, want the two matter parts named", got)
	}
}

// TestMatterFilterKeepsContentAboutTheSameWords: the filter compares the WHOLE title, so a chapter
// about indexing, a dedication to a craft or notes about a subject stay exactly where they are.
func TestMatterFilterKeepsContentAboutTheSameWords(t *testing.T) {
	nodes := Build([]entities.Part{
		{Level: 1, Title: "Indexing strategies", Text: "How a database picks an index."},
		{Level: 1, Title: "Dedication to quality", Text: "Why craft matters in software."},
		{Level: 1, Title: "Notes on memory models", Text: "The memory model defines what a program may assume."},
		{Level: 1, Title: "Appendix: a worked example", Text: "The full derivation, step by step."},
		{Level: 2, Title: "Glossary", Text: "Happens-before: two events are ordered when one happens before the other."},
		{Level: 2, Title: "References in depth", Text: "How references are represented in memory."},
	})
	if len(nodes) != 7 { // root + six
		var titles []string
		for _, n := range nodes {
			titles = append(titles, n.Title)
		}
		t.Errorf("content was dropped: %v", titles)
	}
	// The body is never consulted: a content part may talk about an index all it likes.
	if IsMatter(entities.Part{Title: "Caching", Text: "See also the index of the book and the bibliography."}) {
		t.Error("a part whose body mentions matter was dropped")
	}
}
