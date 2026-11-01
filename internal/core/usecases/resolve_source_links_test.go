package usecases_test

import (
	"context"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
)

// TestResolveLinksMatchesByURLKey: a rendered link and the address a source was captured from may
// differ in a fragment or a trailing slash — they still address the same page, so the link opens
// the source. A URL the vault does not know stays external (absent from the answer).
func TestResolveLinksMatchesByURLKey(t *testing.T) {
	index := newFakeIndex()
	source := func(hash, url string) {
		t.Helper()
		if err := index.UpsertSource(context.Background(), entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, URL: url,
		}); err != nil {
			t.Fatal(err)
		}
	}
	source("aaaaaaaaaaaaaaaa", "https://example.com/article")
	source("bbbbbbbbbbbbbbbb", "https://example.com/blog/")
	source("cccccccccccccccc", "https://example.com/with-query?x=1")

	uc := usecases.Sources{Index: index}
	links, err := uc.ResolveLinks(context.Background(), []string{
		"https://example.com/article#section", // fragment ignored
		"https://example.com/blog",            // trailing slash ignored
		"https://example.com/with-query?x=1",
		"https://example.com/nowhere", // unknown: stays external
		"https://example.com/article", // duplicate of the first input
		"not a url",                   // ignored
	})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, link := range links {
		got[link.URL] = link.Hash
	}
	if len(links) != 3 {
		t.Fatalf("links = %d (%v), want 3", len(links), got)
	}
	if got["https://example.com/article#section"] != "aaaaaaaaaaaaaaaa" {
		t.Errorf("fragment link = %q, want the article's source", got["https://example.com/article#section"])
	}
	if got["https://example.com/blog"] != "bbbbbbbbbbbbbbbb" {
		t.Errorf("slash link = %q, want the blog's source", got["https://example.com/blog"])
	}
	if _, ok := got["https://example.com/nowhere"]; ok {
		t.Errorf("unknown URL resolved: %v", got)
	}
}

// TestResolveLinksWithoutIndexEntries: nothing known means no links, never an error — the answer a
// page renders when the vault holds none of its sources.
func TestResolveLinksWithoutIndexEntries(t *testing.T) {
	uc := usecases.Sources{Index: newFakeIndex()}
	links, err := uc.ResolveLinks(context.Background(), []string{"https://example.com/a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Fatalf("links = %v, want none", links)
	}
}
