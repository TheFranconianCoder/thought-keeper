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

// TestActivityRootsNameTheMainSource: the Start statistic counts the batch but names the source that
// OWNS it. Two feed articles that arrived are one chip — the feed; an orphan child (its owner is not
// in the index) stands for itself, so nothing disappears from the statistic.
func TestActivityRootsNameTheMainSource(t *testing.T) {
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
	base := time.UnixMilli(1_700_000_000_000).UTC()

	hash := func(prefix string) string { return strings.Repeat(prefix, 32) }
	feed := hash("f1")
	firstArticle := hash("a1")
	secondArticle := hash("a2")
	changedArticle := hash("a3")
	orphan := hash("a4")
	page := hash("b1")

	insert := func(h, title, meta string, imported time.Time) {
		t.Helper()
		if err := repo.UpsertSource(ctx, entities.Source{
			ID: ulid.Make(), Hash: h, Kind: entities.SourceKindMD, Ext: ".md",
			Path: h + ".md", Title: title, TreeState: entities.TreeNone,
			ImportedAt: imported, Meta: meta,
		}); err != nil {
			t.Fatalf("source %s: %v", title, err)
		}
	}
	owned := func(owner string) string { return `{"parent_hash":"` + owner + `"}` }

	insert(feed, "The feed", `{"url":"https://example.com/feed"}`, base.Add(-72*time.Hour))
	insert(page, "A page added by hand", `{}`, base.Add(time.Hour))
	insert(firstArticle, "Article one", owned(feed), base.Add(time.Hour))
	insert(secondArticle, "Article two", owned(feed), base.Add(2*time.Hour))
	insert(changedArticle, "An article that changed", owned(feed), base.Add(-72*time.Hour))
	insert(orphan, "An orphan", owned(hash("c1")), base.Add(time.Hour))
	// The feed's own snapshot did not change here: only its article's page did.
	if err := repo.SetSourceURLChanged(ctx, changedArticle, base.Add(time.Hour)); err != nil {
		t.Fatalf("changed: %v", err)
	}

	arrived := entities.SourceFilter{ArrivedSince: base}
	batch, err := repo.CountSources(ctx, arrived)
	if err != nil {
		t.Fatalf("count batch: %v", err)
	}
	// The four arrivals: the page, two articles and the orphan.
	if batch != 4 {
		t.Fatalf("batch = %d, want 4", batch)
	}
	roots, err := repo.CountSourceActivityRoots(ctx, arrived)
	if err != nil {
		t.Fatalf("count roots: %v", err)
	}
	// The feed stands for both articles; the page and the orphan stand for themselves.
	if roots != 3 {
		t.Fatalf("roots = %d, want 3", roots)
	}
	main, err := repo.ListSourceActivityRoots(ctx, arrived, 24)
	if err != nil {
		t.Fatalf("list roots: %v", err)
	}
	names := make(map[string]bool, len(main))
	for _, src := range main {
		names[src.Title] = true
	}
	for _, want := range []string{"The feed", "A page added by hand", "An orphan"} {
		if !names[want] {
			t.Fatalf("main = %v, want %q", names, want)
		}
	}
	if len(main) != 3 {
		t.Fatalf("main = %v, want three rows", names)
	}

	changed := entities.SourceFilter{ChangedSince: base}
	if n, cErr := repo.CountSourceActivityRoots(ctx, changed); cErr != nil || n != 1 {
		t.Fatalf("changed roots = %d, %v, want 1 (its feed)", n, cErr)
	}
	main, err = repo.ListSourceActivityRoots(ctx, changed, 24)
	if err != nil {
		t.Fatalf("list changed roots: %v", err)
	}
	if len(main) != 1 || main[0].Hash != feed {
		t.Fatalf("changed main = %v, want the feed", main)
	}
}
