package entities

import "testing"

// TestURLKey: two spellings of one page must be one key. The stored address is still the URL as it
// was given (URLHash); only the comparison ignores the slash and the fragment.
func TestURLKey(t *testing.T) {
	same := [][]string{
		{"https://example.com/article", "https://example.com/article/"},
		{"https://example.com/article/", "https://example.com/article"},
		{"https://example.com", "https://example.com/"},
		{"https://example.com/a#section", "https://example.com/a"},
		{"https://example.com/a/#section", "https://example.com/a/"},
		{"  https://example.com/a/  ", "https://example.com/a"},
		{"https://example.com/a?q=1", "https://example.com/a/?q=1"},
	}
	for _, pair := range same {
		if URLKey(pair[0]) != URLKey(pair[1]) {
			t.Errorf("%q and %q are not the same key: %q vs %q",
				pair[0], pair[1], URLKey(pair[0]), URLKey(pair[1]))
		}
	}
	different := [][]string{
		{"https://example.com/a", "https://example.com/b"},
		{"https://example.com/a", "https://example.com/A"},
		{"https://example.com/a?q=1", "https://example.com/a?q=2"},
		{"https://example.com/a", "http://example.com/a"},
	}
	for _, pair := range different {
		if URLKey(pair[0]) == URLKey(pair[1]) {
			t.Errorf("%q and %q share a key", pair[0], pair[1])
		}
	}
	// The ADDRESS still distinguishes them: only the comparison is normalised.
	if URLHash("https://example.com/a") == URLHash("https://example.com/a/") {
		t.Error("URLHash must keep the spelling: a source's address is the URL as it was given")
	}
}
