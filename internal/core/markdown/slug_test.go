package markdown_test

import (
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
)

func TestSlugTransliteratesGerman(t *testing.T) {
	cases := map[string]string{
		"Caching für Anfänger": "caching-fuer-anfaenger",
		"Übertragung & Größe":  "uebertragung-groesse",
		"ÄÖÜ äöü ß":            "aeoeue-aeoeue-ss",
		"Crème brûlée":         "creme-brulee",
		"Software Engineering": "software-engineering",
	}
	for in, want := range cases {
		if got := markdown.Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugPathKeepsHierarchy(t *testing.T) {
	if got := markdown.SlugPath("Software Engineering/Caching für Anfänger"); got != "software-engineering/caching-fuer-anfaenger" {
		t.Fatalf("SlugPath = %q", got)
	}
}

func TestNormalizeTagASCII(t *testing.T) {
	cases := map[string]string{
		"Übertragung":   "uebertragung",
		"Größe":         "groesse",
		"maß":           "mass",
		"Cache/Konzept": "cache/konzept",
		"Ärger!!":       "aerger",
	}
	for in, want := range cases {
		if got := markdown.NormalizeTag(in); got != want {
			t.Errorf("NormalizeTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTagsWithUmlauts(t *testing.T) {
	body := "Text über #Übertragung und #größe, plus #cache/invalidierung."
	got := markdown.Tags(body)
	want := []string{"uebertragung", "groesse", "cache/invalidierung"}
	if len(got) != len(want) {
		t.Fatalf("Tags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tag[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStripTrailingTags(t *testing.T) {
	body := "The real prose stays.\n\n#caching #correctness\n"
	if got := markdown.StripTrailingTags(body); got != "The real prose stays." {
		t.Fatalf("StripTrailingTags = %q", got)
	}
	// A lone '#' heading line is not a tag line.
	body2 := "Text.\n\n# Heading"
	if got := markdown.StripTrailingTags(body2); got != "Text.\n\n# Heading" {
		t.Fatalf("StripTrailingTags kept heading wrong: %q", got)
	}
}

func TestUnlinkDropsUnknownTargets(t *testing.T) {
	known := map[string]bool{"Caching": true}
	body := "See [[Caching]] and [[Ghost Page|the ghost]] and `[[Code]]`."
	got, n := markdown.Unlink(body, func(target string) bool { return known[target] })
	want := "See [[Caching]] and the ghost and `[[Code]]`."
	if got != want || n != 1 {
		t.Fatalf("Unlink = %q (%d changes), want %q (1)", got, n, want)
	}
}
