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

// TestSourceChildrenAreOwnerScoped: the Sub-sources tab asks "what does THIS source own?", and the
// two owners are different keys — a sitemap owns pages by parent hash, a wiki page owns the URLs it
// brought in by page id. A missing filter would show unrelated sources; a lost owner field would
// empty the tab.
func TestSourceChildrenAreOwnerScoped(t *testing.T) {
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

	parent := strings.Repeat("a1", 32)
	sitemapChild := strings.Repeat("b2", 32)
	wikiID := ulid.Make().String()
	wikiHash := entities.WikiHash(ulid.MustParse(wikiID))
	wikiChild := strings.Repeat("c3", 32)
	unrelated := strings.Repeat("d4", 32)
	mk := func(hash string, s entities.Source) {
		t.Helper()
		s.ID = ulid.Make()
		s.Hash = hash
		s.Kind = entities.SourceKindURL
		s.Ext = ".md"
		s.Path = hash + ".md"
		s.Title = hash[:4]
		s.TreeState = entities.TreeNone
		s.ImportedAt = now
		s.Meta = "{}"
		if err := repo.UpsertSource(ctx, s); err != nil {
			t.Fatalf("source %s: %v", hash[:4], err)
		}
	}
	mk(parent, entities.Source{Sitemap: true})
	mk(sitemapChild, entities.Source{Origin: entities.SitemapOrigin, ParentHash: parent})
	mk(wikiHash, entities.Source{Kind: entities.SourceKindMD, OriginalName: entities.WikiPrefix + wikiID})
	mk(wikiChild, entities.Source{Origin: entities.WikiLinkOrigin, WikiPage: wikiID})
	mk(unrelated, entities.Source{})

	children, err := repo.ListSourceChildren(ctx, parent, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].Hash != sitemapChild {
		t.Fatalf("sitemap children = %v, want only its page", hashesOf(children))
	}
	if n, err := repo.CountSourceChildren(ctx, parent, ""); err != nil || n != 1 {
		t.Fatalf("sitemap count = %d (%v), want 1", n, err)
	}

	wikiChildren, err := repo.ListSourceChildren(ctx, "", wikiID)
	if err != nil {
		t.Fatal(err)
	}
	if len(wikiChildren) != 1 || wikiChildren[0].Hash != wikiChild {
		t.Fatalf("wiki children = %v, want only the page's URL source", hashesOf(wikiChildren))
	}
	if n, err := repo.CountSourceChildren(ctx, "", wikiID); err != nil || n != 1 {
		t.Fatalf("wiki count = %d (%v), want 1", n, err)
	}
}

func hashesOf(srcs []entities.Source) []string {
	out := make([]string, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, s.Hash)
	}
	return out
}
