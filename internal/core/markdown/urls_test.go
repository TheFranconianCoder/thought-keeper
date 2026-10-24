package markdown

import "testing"

func TestURLsExtractsAuthoredSourcesOnly(t *testing.T) {
	body := `A [link](https://example.com/a) and <https://example.com/b>, plus https://example.com/c.

` + "```" + `
https://example.com/in-code
` + "```" + `

Inline ` + "`https://example.com/also-code`" + ` is not a source.

Repeated https://example.com/a and trailing (https://example.com/d).`
	got := URLs(body)
	want := []string{"https://example.com/a", "https://example.com/b", "https://example.com/c", "https://example.com/d"}
	if len(got) != len(want) {
		t.Fatalf("URLs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("URLs = %v, want %v", got, want)
		}
	}
}
