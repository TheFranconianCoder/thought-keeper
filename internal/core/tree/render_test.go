package tree_test

import (
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/tree"
)

func sample() []entities.Part {
	return []entities.Part{
		{Level: 1, Title: "1 Introduction", Text: "A program is a sequence of steps.", Page: 1},
		{Level: 2, Title: "1.1 Caching", Text: "Caching is only safe when the key covers every input.\n\n# not a heading\nStill the same part.", Page: 12},
		{Level: 0, Title: "", Text: "text before any heading"},
		{Level: 3, Title: "OCR page", Text: "scanned words", Page: 7, OCR: true},
	}
}

// TestRenderingRoundTrips is the load-bearing property of the capture/outline split: what step 1
// wrote is what step 2 reads, exactly. If it were lossy, a rebuild could produce a different tree
// than the derivation it is supposed to keep — and the tree comparison would throw good work away.
func TestRenderingRoundTrips(t *testing.T) {
	got := tree.ParseRendering(tree.Render(sample()))
	want := sample()
	if len(got) != len(want) {
		t.Fatalf("parts = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Level != want[i].Level {
			t.Errorf("part %d level = %d, want %d", i, got[i].Level, want[i].Level)
		}
		if got[i].Title != want[i].Title {
			t.Errorf("part %d title = %q, want %q", i, got[i].Title, want[i].Title)
		}
		if got[i].Text != want[i].Text {
			t.Errorf("part %d text =\n%q\nwant\n%q", i, got[i].Text, want[i].Text)
		}
		if got[i].Page != want[i].Page {
			t.Errorf("part %d page = %d, want %d", i, got[i].Page, want[i].Page)
		}
		if got[i].OCR != want[i].OCR {
			t.Errorf("part %d ocr = %v, want %v", i, got[i].OCR, want[i].OCR)
		}
	}
}

// TestRenderingIsReadable: the file is what a reader opens when the app is not running, so the
// structure must be visible as Markdown and the page/OCR facts must be stated, not hidden.
func TestRenderingIsReadable(t *testing.T) {
	md := tree.Render(sample())
	if !strings.Contains(md, "# 1 Introduction") || !strings.Contains(md, "## 1.1 Caching") {
		t.Errorf("headings are not rendered:\n%s", md)
	}
	if !strings.Contains(md, "<!--part level=2 page=12-->") {
		t.Errorf("the marker does not carry the page:\n%s", md)
	}
	if !strings.Contains(md, "<!--part level=3 page=7 ocr=true-->") {
		t.Errorf("OCR must stay visible in the rendering:\n%s", md)
	}
	if !strings.Contains(md, "Caching is only safe when the key covers every input.") {
		t.Error("the text is missing")
	}
}

// TestRenderingSkipsEmptyPartsPerRun: an empty part is nothing to read and nothing to cite.
func TestRenderingSkipsEmptySections(t *testing.T) {
	md := tree.Render([]entities.Part{
		{Level: 1, Title: "  ", Text: "   \n\t "},
		{Level: 1, Title: "Chapter", Text: "real"},
	})
	if strings.Count(md, "<!--part") != 1 {
		t.Errorf("empty parts must not be rendered:\n%s", md)
	}
	if got := tree.ParseRendering(md); len(got) != 1 || got[0].Title != "Chapter" {
		t.Errorf("parsed = %+v", got)
	}
}

// TestParseRenderingWithoutMarkers is the tolerant path: a file somebody edited by hand (or a
// truncated one) is still read as text instead of being dropped.
func TestParseRenderingWithoutMarkers(t *testing.T) {
	got := tree.ParseRendering("just some prose\n\nwith a blank line\n")
	if len(got) != 1 || !strings.Contains(got[0].Text, "just some prose") {
		t.Errorf("parsed = %+v", got)
	}
}
