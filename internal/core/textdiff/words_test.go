package textdiff_test

import (
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/textdiff"
)

// TestWordsIgnoresWhatIsNotAWord: the materiality measurement must not be fooled by the things that
// churn on a page without saying anything new — line wrapping, punctuation, a ticked counter, a
// single digit.
func TestWordsIgnoresWhatIsNotAWord(t *testing.T) {
	cases := []struct {
		name     string
		old, new string
	}{
		{"identical", "the cache is cold", "the cache is cold"},
		{"reflow", "the cache\nis cold", "the\ncache is cold"},
		{"punctuation", "the cache is cold.", "the cache, is cold!"},
		{"order", "alpha beta gamma", "gamma alpha beta"},
		{"a ticked counter", "Updated 3 minutes ago", "Updated 4 minutes ago"},
		{"a single digit", "Version 2 of the page", "Version 3 of the page"},
		{"a moved paragraph", "one two three\nfour five six", "four five six\none two three"},
	}
	for _, tc := range cases {
		if shift := textdiff.Words(tc.old, tc.new); !shift.Same() {
			t.Errorf("%s: %+v, want no word change", tc.name, shift)
		}
	}
}

// TestWordsCountsChangedPositions: a replaced word counts ONCE, not twice — the number the threshold
// reads is "how many words say something different", not "how many tokens appear in the diff".
func TestWordsCountsChangedPositions(t *testing.T) {
	typo := textdiff.Words("the cache is teh cold one", "the cache is the cold one")
	if typo.Words != 1 {
		t.Fatalf("a typo = %+v, want 1 changed word", typo)
	}
	if typo.AvgRunes <= 0 {
		t.Errorf("a typo has no average length: %+v", typo)
	}

	added := textdiff.Words("alpha beta", "alpha beta gamma delta epsilon")
	if added.Words != 3 {
		t.Fatalf("three added words = %+v, want 3", added)
	}
	removed := textdiff.Words("alpha beta gamma delta", "alpha")
	if removed.Words != 3 {
		t.Fatalf("three removed words = %+v, want 3", removed)
	}
}

// TestWordsMeasuresProse: the average length is what keeps a number or an identifier from passing as
// a sentence — and a data URI from deciding that a page changed.
func TestWordsMeasuresProse(t *testing.T) {
	prose := textdiff.Words(
		"The database was slow and the queries were slow.",
		"The database invalidated eagerly and the queries became fast.",
	)
	if prose.Words < 4 {
		t.Fatalf("a rewritten sentence = %+v, want several changed words", prose)
	}
	// (database, invalidated, eagerly, queries, became, fast) — real words, not one-letter noise.
	if prose.AvgRunes < 4 {
		t.Errorf("prose average = %.1f, want a word-like length", prose.AvgRunes)
	}

	longOld := "![x](data:image/png;base64," + strings.Repeat("A", 200) + ")"
	longNew := "![x](data:image/png;base64," + strings.Repeat("B", 200) + ")"
	if shift := textdiff.Words(longOld, longNew); shift.Words > 1 {
		t.Fatalf("a changed data URI = %+v, want at most one identifier", shift)
	}
}

// TestWordsOnEmptyBodies: a page that gains or loses all its text is the clearest change there is.
func TestWordsOnEmptyBodies(t *testing.T) {
	if shift := textdiff.Words("", ""); !shift.Same() {
		t.Fatalf("two empty bodies = %+v", shift)
	}
	if shift := textdiff.Words("", "alpha beta gamma"); shift.Words != 3 {
		t.Fatalf("a body from nothing = %+v, want 3", shift)
	}
	if shift := textdiff.Words("alpha beta gamma", ""); shift.Words != 3 {
		t.Fatalf("a body to nothing = %+v, want 3", shift)
	}
}
