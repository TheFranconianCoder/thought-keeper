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

// TestSourcesOrderByActivity: the list answers "what happened last", not "what arrived last". A page
// that was rewritten last night belongs above a source that arrived a day ago, and a check that
// answered the same page must not move anything. The changed filter is the same fact, narrowed to
// the rows that actually have a change time.
func TestSourcesOrderByActivity(t *testing.T) {
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
	add := func(hash, kind string, imported time.Time) entities.Source {
		src := entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKind(kind), Ext: ".md",
			Path: hash + ".md", Title: hash, TreeState: entities.TreeNone,
			ImportedAt: imported, Meta: "{}",
		}
		if err := repo.UpsertSource(ctx, src); err != nil {
			t.Fatalf("upsert %s: %v", hash, err)
		}
		return src
	}
	arrived := add(strings.Repeat("a1", 32), "md", now.Add(-24*time.Hour))
	changedNow := add(strings.Repeat("b2", 32), "url", now.Add(-48*time.Hour))
	changedLongAgo := add(strings.Repeat("c3", 32), "url", now.Add(-72*time.Hour))

	if err := repo.SetSourceURLChanged(ctx, changedNow.Hash, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSourceURLChanged(ctx, changedLongAgo.Hash, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}

	list, err := repo.ListSources(ctx, entities.SourceFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{changedNow.Hash, arrived.Hash, changedLongAgo.Hash}
	if len(list) != len(want) {
		t.Fatalf("rows = %d, want %d", len(list), len(want))
	}
	for i := range want {
		if list[i].Hash != want[i] {
			t.Fatalf("activity order = %v, want %v", hashesOf(list), want)
		}
	}

	changed, err := repo.ListSources(ctx, entities.SourceFilter{ChangedOnly: true}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := hashesOf(changed); len(got) != 2 || got[0] != changedNow.Hash || got[1] != changedLongAgo.Hash {
		t.Fatalf("changed list = %v, want the two changed pages", got)
	}
	if n, err := repo.CountSources(ctx, entities.SourceFilter{ChangedOnly: true}); err != nil || n != 2 {
		t.Fatalf("changed count = %d (%v), want 2", n, err)
	}

	stored, err := repo.GetSourceByHash(ctx, changedNow.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.URLChanged.Equal(now) {
		t.Errorf("change time = %v, want %v", stored.URLChanged, now)
	}
}
