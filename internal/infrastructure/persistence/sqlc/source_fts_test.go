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

// TestSourceSearchUsesItsFTSIndex: source_nodes_fts is fed by triggers and rebuilt at start-up, so
// a source's outline title is findable through the sources layer.
func TestSourceSearchUsesItsFTSIndex(t *testing.T) {
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
	id := ulid.Make()
	if err := sources.UpsertSource(ctx, entities.Source{
		ID: id, Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: hash + ".md", Title: "src", Bytes: 3, TreeState: entities.TreeBuilt,
		ImportedAt: now, Meta: "{}",
	}); err != nil {
		t.Fatalf("source: %v", err)
	}
	if err := sources.ReplaceSourceNodes(ctx, id, []entities.SourceNode{
		{NodeID: "0.1", Depth: 1, Ord: 0, Title: "Goroutine Scheduling", Text: "text"},
	}); err != nil {
		t.Fatalf("nodes: %v", err)
	}
	search := NewSearchRepository(db, true)
	hits, err := search.Search(ctx, "Goroutine", "sources", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 || hits[0].Kind != "source" || hits[0].ID != hash {
		t.Fatalf("source hits = %+v, want the source with the outline title", hits)
	}
}
