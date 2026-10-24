package markdown_test

import (
	"reflect"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/markdown"
)

func TestWikilinks(t *testing.T) {
	body := "See [[Alpha]] and [[Beta|the second]] and [[Gamma#Heading]] " +
		"and [[Delta#H|alias]] and again [[Alpha]].\n`[[code]]`\n```\n[[fenced]]\n```\n"
	got := markdown.Wikilinks(body)

	var raw []string
	for _, l := range got {
		raw = append(raw, l.RawTarget)
	}
	want := []string{"Alpha", "Beta", "Gamma", "Delta"}
	if !reflect.DeepEqual(raw, want) {
		t.Fatalf("targets = %v, want %v", raw, want)
	}
	if got[1].Alias != "the second" {
		t.Errorf("alias = %q", got[1].Alias)
	}
	if got[2].Heading != "Heading" {
		t.Errorf("heading = %q", got[2].Heading)
	}
	if got[3].Alias != "alias" || got[3].Heading != "H" {
		t.Errorf("delta = %+v", got[3])
	}
}

func TestTags(t *testing.T) {
	body := "# Heading\nA #caching and #area/health tag.\n`#code`\n```\n#fenced\n```\n"
	got := markdown.Tags(body)
	want := []string{"caching", "area/health"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %v, want %v", got, want)
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

func TestLinkKey(t *testing.T) {
	cases := map[string]string{
		"Cache Invalidation":    "cache invalidation",
		"cache-invalidation":    "cache invalidation",
		"Cache  Invalidation.":  "cache invalidation",
		"Caching & Correctness": "caching correctness",
		"Über Caching":          "ueber caching",
	}
	for in, want := range cases {
		if got := markdown.LinkKey(in); got != want {
			t.Errorf("LinkKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRetarget(t *testing.T) {
	body := "See [[Cache Invalidation]], [[cache-invalidation|the cache]] and " +
		"[[Cache Invalidation#Proof]] and [[Other]].\n`[[Cache Invalidation]]`\n"
	got, n := markdown.Retarget(body, "Cache Invalidation", "Caching and Correctness")
	if n != 3 {
		t.Fatalf("retargeted %d links, want 3: %q", n, got)
	}
	want := "See [[Caching and Correctness]], [[Caching and Correctness|the cache]] and " +
		"[[Caching and Correctness#Proof]] and [[Other]].\n`[[Cache Invalidation]]`\n"
	if got != want {
		t.Errorf("body =\n%q\nwant\n%q", got, want)
	}
	if _, n := markdown.Retarget(body, "Nothing", "Else"); n != 0 {
		t.Errorf("retargeting an unknown target changed %d links", n)
	}
}

func TestWikilinksIgnoresCodeAndCodeLikeSyntax(t *testing.T) {
	body := "Real [[Cache Invalidation]] stays. " +
		"Code `[[inline]]` and ``[[double tick]]`` and ```\n[[fenced]]\n``` are not links. " +
		"C++ [[maybe_unused]] and [[nodiscard]], SQL [[join_type] JOIN t [ON c]] " +
		"and shell [[ ! -d \"$OUT\" ]] are syntax, not claims."
	got := markdown.Wikilinks(body)
	var raw []string
	for _, l := range got {
		raw = append(raw, l.RawTarget)
	}
	want := []string{"Cache Invalidation"}
	if !reflect.DeepEqual(raw, want) {
		t.Fatalf("targets = %v, want %v", raw, want)
	}
}
