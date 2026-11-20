package usecases

import (
	"strings"
	"testing"
)

// TestNodeTextPageReachesTheTail: a node longer than one page is readable — the old code cut both the
// prompt and the tool at the same 8,000 characters, so "read the rest" answered with the same text.
func TestNodeTextPageReachesTheTail(t *testing.T) {
	text := strings.Repeat("a", 20_000)
	first := nodeTextPage(text, 0, 5_000)
	if len(first) <= 5_000 || !strings.Contains(first, "offset=5000") {
		t.Fatalf("the first page must name the next offset: %q", first)
	}
	second := nodeTextPage(text, 5_000, 5_000)
	if !strings.HasPrefix(second, strings.Repeat("a", 5_000)) || !strings.Contains(second, "offset=10000") {
		t.Fatalf("the second page is not the text from 5,000: %q", second[:60])
	}
	last := nodeTextPage(text, 15_000, 5_000)
	if strings.Contains(last, "offset=") {
		t.Fatalf("the last page must not ask for another one: %q", last)
	}
	if past := nodeTextPage(text, 25_000, 5_000); !strings.Contains(past, "end of the node") {
		t.Fatalf("an offset past the text = %q", past)
	}
}

// TestNodeTextPageKeepsRunesWhole: a page never ends inside a multi-byte character.
func TestNodeTextPageKeepsRunesWhole(t *testing.T) {
	text := strings.Repeat("ä", 10)  // two bytes each
	page := nodeTextPage(text, 0, 5) // 5 bytes: a boundary inside the third character
	if !strings.Contains(page, "ä") {
		t.Fatalf("page = %q", page)
	}
	for _, r := range strings.SplitN(page, "\n", 2)[0] {
		if r == '\uFFFD' {
			t.Fatalf("the page split a rune: %q", page)
		}
	}
	if short := nodeTextPage("tiny", 0, 8_000); short != "tiny" {
		t.Fatalf("a short node = %q", short)
	}
	if empty := nodeTextPage("   ", 0, 8_000); empty != "(empty part)" {
		t.Fatalf("an empty node = %q", empty)
	}
}
