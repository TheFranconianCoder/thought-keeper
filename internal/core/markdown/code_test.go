package markdown_test

import (
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
)

func TestQuoteBracketSyntaxWrapsBareSpans(t *testing.T) {
	cases := map[string]string{
		"The production is [[ attribute-list ]], so each specifier is doubled.": "The production is `[[ attribute-list ]]`, so each specifier is doubled.",
		"The example is bool f(void) [[unsequenced]]; in the standard.":         "The example is bool f(void) `[[unsequenced]]`; in the standard.",
		"Shell tests such as [[ ! -d \"$OUT\" ]] pass.":                         "Shell tests such as `[[ ! -d \"$OUT\" ]]` pass.",
		"An array [[1, 2, 3], [2, 4, 6]] is a table.":                           "An array `[[1, 2, 3], [2, 4, 6]]` is a table.",
		"Two spans [[a]] and [[b]] are quoted.":                                 "Two spans `[[a]]` and `[[b]]` are quoted.",
	}
	for body, want := range cases {
		got, n := markdown.QuoteBracketSyntax(body)
		if got != want || n != countSpans(want) {
			t.Errorf("QuoteBracketSyntax(%q) = %q (%d), want %q", body, got, n, want)
		}
	}
}

func TestQuoteBracketSyntaxLeavesCodeAlone(t *testing.T) {
	body := "The declaration `[[unsequenced]]` stays. A fence:\n\n```c\nint f(void) [[unsequenced]];\n```\n"
	got, n := markdown.QuoteBracketSyntax(body)
	if got != body || n != 0 {
		t.Fatalf("QuoteBracketSyntax changed code: %q (%d)", got, n)
	}
}

func TestQuoteBracketSyntaxIsIdempotent(t *testing.T) {
	body := "The production is [[ attribute-list ]]."
	once, n := markdown.QuoteBracketSyntax(body)
	if n != 1 {
		t.Fatalf("first pass = %d changes", n)
	}
	twice, n := markdown.QuoteBracketSyntax(once)
	if twice != once || n != 0 {
		t.Fatalf("second pass = %q (%d changes), want unchanged", twice, n)
	}
}

func TestQuoteBracketSyntaxLeavesUnfinishedAndMultilineSpans(t *testing.T) {
	for _, body := range []string{
		"An unfinished [[span stays.",
		"A span that crosses\na [[line\nbreak]] is not inline code.",
	} {
		got, n := markdown.QuoteBracketSyntax(body)
		if got != body || n != 0 {
			t.Errorf("QuoteBracketSyntax(%q) = %q (%d), want unchanged", body, got, n)
		}
	}
}

// countSpans is the number of inline code spans a quoted body should carry: one per wrapped pair.
func countSpans(body string) int {
	n := 0
	for i := 0; i < len(body); i++ {
		if body[i] == '`' {
			n++
		}
	}
	return n / 2
}
