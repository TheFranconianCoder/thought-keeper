package usecases_test

import (
	"context"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
)

// TestAddURLExpandsASitemap: the sitemap is a source and the parent of the pages it declares. The
// pages are returned (not fetched here) so the caller can claim them with the parent as owner.
func TestAddURLExpandsASitemap(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	steps := &fakeSteps{}
	add := usecases.AddURL{
		Fetch: &fakeFetcher{page: interfaces.FetchedPage{
			Status: 200,
			Body: []byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://example.com/one</loc></url>
  <url><loc>https://example.com/two</loc></url>
  <url><loc>https://example.com/one</loc></url>
  <url><loc>/relative</loc></url>
  <url><loc>mailto:not-a-page@example.com</loc></url>
</urlset>`),
		}},
		Store: objects.New(t.TempDir()), Index: idx, Steps: steps, Clock: fixedClock{},
	}

	src, err := add.Execute(ctx, "https://example.com/sitemap.xml", false)
	if err != nil {
		t.Fatalf("add sitemap: %v", err)
	}
	if !src.Sitemap {
		t.Fatal("source is not marked as a sitemap")
	}
	if src.Hash != entities.URLHash("https://example.com/sitemap.xml") {
		t.Fatalf("hash = %s, want the URL address", src.Hash)
	}
	want := []string{"https://example.com/one", "https://example.com/two", "https://example.com/relative"}
	if len(src.SitemapURLs) != len(want) {
		t.Fatalf("pages = %v, want %v", src.SitemapURLs, want)
	}
	for i, url := range want {
		if src.SitemapURLs[i] != url {
			t.Fatalf("page %d = %q, want %q", i, src.SitemapURLs[i], url)
		}
	}
	stored, err := idx.GetSourceByHash(ctx, src.Hash)
	if err != nil {
		t.Fatalf("stored sitemap: %v", err)
	}
	if !stored.Sitemap {
		t.Fatal("the row lost its sitemap marker")
	}
	if steps.identities[src.Hash].Sitemap != true {
		t.Fatal("the source record lost its sitemap marker")
	}
	if steps.identities[src.Hash].Kind != entities.SourceKindURL {
		t.Fatalf("identity kind = %q, want url", steps.identities[src.Hash].Kind)
	}
}

// TestQueueSitemapChildrenAreOwnedByTheSitemap: pages from a sitemap cannot exist without it. The
// parent hash is written on every claimed row; an independently collected URL is left alone.
func TestQueueSitemapChildrenAreOwnedByTheSitemap(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	steps := &fakeSteps{}
	const parent = "parent-sitemap"
	const one = "https://example.com/one"
	const two = "https://example.com/two"
	const independent = "https://example.com/independent"
	_ = idx.UpsertSource(ctx, entities.Source{Hash: entities.URLHash(independent), Kind: entities.SourceKindURL, URL: independent, Title: independent})

	queue := usecases.QueueURLs{Index: idx, Steps: steps, Clock: fixedClock{}}
	queued, err := queue.ExecuteSitemap(ctx, parent, []string{one, two, independent})
	if err != nil {
		t.Fatalf("queue sitemap: %v", err)
	}
	if len(queued) != 2 {
		t.Fatalf("queued = %v, want the two new pages", queued)
	}
	for _, raw := range []string{one, two} {
		src, err := idx.GetSourceByHash(ctx, entities.URLHash(raw))
		if err != nil {
			t.Fatalf("%s not claimed: %v", raw, err)
		}
		if src.Origin != entities.SitemapOrigin || src.ParentHash != parent {
			t.Fatalf("%s owner = %q/%q, want sitemap/%s", raw, src.Origin, src.ParentHash, parent)
		}
	}
	known, err := idx.GetSourceByHash(ctx, entities.URLHash(independent))
	if err != nil {
		t.Fatalf("independent source disappeared: %v", err)
	}
	if known.Origin != "" {
		t.Fatalf("independent source was adopted by the sitemap: %+v", known)
	}
}

// TestSitemapDeleteCascadesAndDependentDeleteIsRefused: the sitemap owns its pages. A direct delete
// of a page is a conflict; deleting the sitemap removes the subtree.
func TestSitemapDeleteCascadesAndDependentDeleteIsRefused(t *testing.T) {
	ctx := context.Background()
	idx := newFakeIndex()
	store := objects.New(t.TempDir())
	const parent = "sitemap-parent"
	const child = "sitemap-child"
	const grandchild = "sitemap-grandchild"
	_ = idx.UpsertSource(ctx, entities.Source{Hash: parent, Kind: entities.SourceKindURL, URL: "https://example.com/sitemap.xml", Title: "Sitemap", Sitemap: true})
	_ = idx.UpsertSource(ctx, entities.Source{Hash: child, Kind: entities.SourceKindURL, URL: "https://example.com/a", Title: "A", Origin: entities.SitemapOrigin, ParentHash: parent, Sitemap: true})
	_ = idx.UpsertSource(ctx, entities.Source{Hash: grandchild, Kind: entities.SourceKindURL, URL: "https://example.com/b", Title: "B", Origin: entities.SitemapOrigin, ParentHash: child})

	del := usecases.DeleteSource{
		Store: store, Index: idx, Atoms: nil, AtomRepo: &fakeAtomRepo{}, Steps: nil, Clock: fixedClock{},
	}
	if _, err := del.Execute(ctx, child); err == nil || !strings.Contains(err.Error(), "belongs to a sitemap") {
		t.Fatalf("deleting a sitemap page = %v, want a dependent_source refusal", err)
	}
	if _, err := idx.GetSourceByHash(ctx, grandchild); err != nil {
		t.Fatal("the refused delete removed a page")
	}

	res, err := del.Execute(ctx, parent)
	if err != nil {
		t.Fatalf("delete sitemap: %v", err)
	}
	if res.DependentsRemoved != 2 {
		t.Fatalf("dependents removed = %d, want 2", res.DependentsRemoved)
	}
	for _, hash := range []string{parent, child, grandchild} {
		if _, err := idx.GetSourceByHash(ctx, hash); err == nil {
			t.Fatalf("%s survived the sitemap deletion", hash)
		}
	}
}
