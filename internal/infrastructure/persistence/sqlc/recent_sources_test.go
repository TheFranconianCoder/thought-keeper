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

// TestListRecentSourcesSeparatesContentFromNavigation: a digest lists what has content (a document,
// a page, a snapshot) and only COUNTS what is navigation (a bookmark, a sitemap, a feed). A window
// that returned the containers would spend its eight slots on lists of other sources.
func TestListRecentSourcesSeparatesContentFromNavigation(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err = persistence.Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewSourceRepository(db)
	now := time.UnixMilli(1_700_000_000_000).UTC()

	mk := func(hash string, at time.Time, s entities.Source) {
		t.Helper()
		s.ID = ulid.Make()
		s.Hash = hash
		s.Kind = entities.SourceKindURL
		s.Ext = ".md"
		s.Path = hash + ".md"
		s.Title = hash[:4]
		s.TreeState = entities.TreeNone
		s.ImportedAt = at
		s.Meta = "{}"
		if uErr := repo.UpsertSource(ctx, s); uErr != nil {
			t.Fatalf("source %s: %v", hash[:4], uErr)
		}
	}

	newest := strings.Repeat("a1", 32)
	second := strings.Repeat("b2", 32)
	old := strings.Repeat("c3", 32)
	bookmark := strings.Repeat("d4", 32)
	sitemap := strings.Repeat("e5", 32)
	feed := strings.Repeat("f6", 32)

	mk(newest, now.Add(-time.Hour), entities.Source{})
	mk(second, now.Add(-5*time.Hour), entities.Source{})
	mk(old, now.Add(-48*time.Hour), entities.Source{})
	mk(bookmark, now.Add(-2*time.Hour), entities.Source{Bookmark: true})
	mk(sitemap, now.Add(-3*time.Hour), entities.Source{Sitemap: true})
	mk(feed, now.Add(-4*time.Hour), entities.Source{RSS: true})

	recent, err := repo.ListRecentSources(ctx, now.Add(-24*time.Hour), 8, 0)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if got := hashesOf(recent.Sources); len(got) != 2 || got[0] != newest || got[1] != second {
		t.Fatalf("sources = %v, want the two content sources newest first", got)
	}
	if recent.Total != 2 || recent.Bookmarks != 1 || recent.Containers != 2 {
		t.Fatalf("counts = total %d bookmarks %d containers %d, want 2/1/2",
			recent.Total, recent.Bookmarks, recent.Containers)
	}

	// No window means the newest sources of the whole vault, still content only.
	all, err := repo.ListRecentSources(ctx, time.Time{}, 8, 0)
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if all.Total != 3 || len(all.Sources) != 3 {
		t.Fatalf("all = %d sources (total %d), want the three content sources", len(all.Sources), all.Total)
	}
	if all.Sources[2].Hash != old {
		t.Fatalf("last source = %s, want the old one", all.Sources[2].Hash)
	}

	// The limit bounds the list, never the total: a digest says "8 of 30".
	limited, err := repo.ListRecentSources(ctx, time.Time{}, 1, 0)
	if err != nil {
		t.Fatalf("limited: %v", err)
	}
	if len(limited.Sources) != 1 || limited.Total != 3 {
		t.Fatalf("limited = %d sources (total %d), want 1 of 3", len(limited.Sources), limited.Total)
	}

	// The second page continues after the first and still reports the window's total.
	pageTwo, err := repo.ListRecentSources(ctx, time.Time{}, 2, 2)
	if err != nil {
		t.Fatalf("page two: %v", err)
	}
	if len(pageTwo.Sources) != 1 || pageTwo.Sources[0].Hash != old || pageTwo.Total != 3 {
		t.Fatalf("page two = %v (total %d), want the old source and total 3", hashesOf(pageTwo.Sources), pageTwo.Total)
	}
}
