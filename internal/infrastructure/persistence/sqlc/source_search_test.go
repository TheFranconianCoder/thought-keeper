package sqlc

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// TestSourceSearchMatchesTheListFields: the palette's Sources layer uses the same text fields as
// the Sources list — title, original name, URL, human tags and auto tags — and is case-insensitive.
func TestSourceSearchMatchesTheListFields(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !persistence.EnsureFTS(db, slog.Default()) {
		t.Skip("SQLite build has no FTS5")
	}
	sources := NewSourceRepository(db)
	now := time.UnixMilli(1_700_000_000_000).UTC()
	hash := strings.Repeat("ef", 32)
	if err := sources.UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md",
		Path: hash + ".md", Title: "Goroutine Scheduling", OriginalName: "go-notes.md",
		URL: "https://go.dev/doc", Bytes: 3, TreeState: entities.TreeBuilt,
		ImportedAt: now, Meta: "{}",
	}); err != nil {
		t.Fatalf("source: %v", err)
	}
	if err := sources.SetSourceTags(ctx, hash, []string{"go", "runtime"}); err != nil {
		t.Fatalf("tags: %v", err)
	}
	if err := sources.SetSourceAutoTags(ctx, hash, []string{"concurrency"}, now); err != nil {
		t.Fatalf("auto tags: %v", err)
	}
	search := NewSearchRepository(db, true)
	for _, term := range []string{"GOROUTINE", "go-notes", "go.dev", "runtime", "concurrency"} {
		hits, err := search.Search(ctx, term, "sources", 10)
		if err != nil {
			t.Fatalf("search %q: %v", term, err)
		}
		if len(hits) == 0 || hits[0].Kind != "source" || hits[0].ID != hash {
			t.Errorf("source hits for %q = %+v, want the source", term, hits)
		}
	}
}
