package sqlc

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// TestSourceListLinksByURLs: the URL a page links to and the address a source was captured from may
// differ in a trailing slash or a fragment. The query answers the raw forms it was given; the
// caller decides by URL key, so both resolve. A non-URL source never matches.
func TestSourceListLinksByURLs(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewSourceRepository(db)
	now := time.UnixMilli(1_700_000_000_000).UTC()
	insert := func(hash, kind, url string) {
		t.Helper()
		if err := repo.UpsertSource(ctx, entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKind(kind), Ext: "." + kind,
			Path: hash + "." + kind, Title: url, Bytes: 1, TreeState: entities.TreeNone,
			URL: url, ImportedAt: now,
		}); err != nil {
			t.Fatalf("source %s: %v", url, err)
		}
	}

	article := strings.Repeat("a1", 32)
	blog := strings.Repeat("b2", 32)
	markdown := strings.Repeat("c3", 32)
	insert(article, "url", "https://example.com/article")
	insert(blog, "url", "https://example.com/blog/")
	insert(markdown, "md", "")

	links, err := repo.ListSourceLinks(ctx, []string{"https://example.com/article", "https://example.com/blog/"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, link := range links {
		got[link.URL] = link.Hash
	}
	if len(links) != 2 {
		t.Fatalf("links = %v, want the two URL sources", got)
	}
	if got["https://example.com/article"] != article || got["https://example.com/blog/"] != blog {
		t.Errorf("links = %v, want article and blog", got)
	}

	// A URL nothing holds resolves to nothing; the query ignores sources that are not URL snapshots.
	none, err := repo.ListSourceLinks(ctx, []string{"https://example.com/nowhere"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("unknown URL = %v, want none", none)
	}
}
