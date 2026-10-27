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

func TestSlug(t *testing.T) {
	if got := markdown.Slug("Caching is only safe!"); got != "caching-is-only-safe" {
		t.Errorf("slug = %q", got)
	}
	if got := markdown.Slug("###"); got != "untitled" {
		t.Errorf("slug = %q, want untitled", got)
	}
}
