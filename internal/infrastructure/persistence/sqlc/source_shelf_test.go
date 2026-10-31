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

// TestShelfFiltersAndQueueOrder: the star and the read-later queue are filters of the SAME list, and
// the queue is ordered by the moment the source entered it (oldest first) — that is what makes it a
// queue rather than a second "newest" list.
func TestShelfFiltersAndQueueOrder(t *testing.T) {
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
	insert := func(hash, title string, at time.Time) {
		t.Helper()
		if err := repo.UpsertSource(ctx, entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
			Path: hash + ".md", Title: title, Bytes: 1, TreeState: entities.TreeNone,
			ImportedAt: at, Meta: "{}",
		}); err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
	}
	rust := strings.Repeat("11", 32)
	llm := strings.Repeat("22", 32)
	done := strings.Repeat("33", 32)
	insert(rust, "Rust async", base)
	insert(llm, "LLM agents", base.Add(time.Minute))
	insert(done, "Finished", base.Add(2*time.Minute))

	if err := repo.SetSourceFavorite(ctx, rust, base); err != nil {
		t.Fatal(err)
	}
	// rust entered the queue first; llm second; done entered and left it.
	if err := repo.SetSourceReadLater(ctx, rust, base.Add(time.Hour), time.Time{}, false, []string{"rust-async"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSourceReadLater(ctx, llm, base.Add(2*time.Hour), time.Time{}, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSourceReadLater(ctx, done, base.Add(3*time.Hour), base.Add(4*time.Hour), false, nil); err != nil {
		t.Fatal(err)
	}

	check := func(name string, filter entities.SourceFilter, want int) {
		t.Helper()
		n, err := repo.CountSources(ctx, filter)
		if err != nil {
			t.Fatalf("%s: count: %v", name, err)
		}
		if n != want {
			t.Errorf("%s: count = %d, want %d", name, n, want)
		}
		rows, err := repo.ListSources(ctx, filter, 100, 0)
		if err != nil {
			t.Fatalf("%s: list: %v", name, err)
		}
		if len(rows) != want {
			t.Errorf("%s: rows = %d, want %d", name, len(rows), want)
		}
	}

	check("all", entities.SourceFilter{}, 3)
	check("favorites", entities.SourceFilter{FavoriteOnly: true}, 1)
	check("read later", entities.SourceFilter{ReadLaterOnly: true}, 2)
	check("read later / interest", entities.SourceFilter{ReadLaterOnly: true, Interest: "rust-async"}, 1)
	check("read later / unknown interest", entities.SourceFilter{ReadLaterOnly: true, Interest: "nope"}, 0)

	queue, err := repo.ListSources(ctx, entities.SourceFilter{ReadLaterOnly: true}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 2 || queue[0].Hash != rust || queue[1].Hash != llm {
		t.Fatalf("queue order = %v, want rust then llm (oldest entry first)", hashes(queue))
	}

	if n, err := repo.CountReadLater(ctx); err != nil || n != 2 {
		t.Fatalf("open queue = %d (%v), want 2", n, err)
	}
	if n, err := repo.CountReadLaterFromInterests(ctx); err != nil || n != 1 {
		t.Fatalf("queue from interests = %d (%v), want 1", n, err)
	}
	if n, err := repo.CountReadLaterVia(ctx, "rust-async"); err != nil || n != 1 {
		t.Fatalf("queue via rust-async = %d (%v), want 1", n, err)
	}
	if n, err := repo.CountFavorites(ctx); err != nil || n != 1 {
		t.Fatalf("favorites = %d (%v), want 1", n, err)
	}

	// Finishing the entry takes it out of the queue and leaves the source alone.
	open, err := repo.GetSourceByHash(ctx, llm)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSourceReadLater(ctx, llm, open.ReadLaterAt, base.Add(5*time.Hour), open.ReadLaterManual, open.ReadLaterVia); err != nil {
		t.Fatal(err)
	}
	check("read later after finishing", entities.SourceFilter{ReadLaterOnly: true}, 1)
}

func hashes(sources []entities.Source) []string {
	out := make([]string, 0, len(sources))
	for _, source := range sources {
		out = append(out, source.Hash[:8])
	}
	return out
}
