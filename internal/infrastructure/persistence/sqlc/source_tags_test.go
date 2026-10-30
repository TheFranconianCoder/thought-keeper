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

// TestSourceTagsMirrorAndCount: the index carries a source's human labels as a JSON array; the list
// reads them with the row and the vocabulary counts them without opening a .meta per source.
func TestSourceTagsMirrorAndCount(t *testing.T) {
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
	insert := func(hash string) {
		t.Helper()
		if err := repo.UpsertSource(ctx, entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
			Path: hash + ".md", Title: "src", Bytes: 1, TreeState: entities.TreeNone,
			ImportedAt: now, Meta: "{}",
		}); err != nil {
			t.Fatalf("source: %v", err)
		}
	}
	hashA, hashB := strings.Repeat("a1", 32), strings.Repeat("b2", 32)
	insert(hashA)
	insert(hashB)
	if err := repo.SetSourceTags(ctx, hashA, []string{"go", "caching"}); err != nil {
		t.Fatalf("tags A: %v", err)
	}
	if err := repo.SetSourceTags(ctx, hashB, []string{"go"}); err != nil {
		t.Fatalf("tags B: %v", err)
	}
	if err := repo.SetSourceAutoTags(ctx, hashB, []string{"go", "reference"}, now); err != nil {
		t.Fatalf("auto tags B: %v", err)
	}

	got, err := repo.GetSourceByHash(ctx, hashA)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "go" || got.Tags[1] != "caching" {
		t.Fatalf("tags = %v, want the labels", got.Tags)
	}
	b, err := repo.GetSourceByHash(ctx, hashB)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.AutoTags) != 2 || b.AutoTagsAt.IsZero() {
		t.Fatalf("auto tags = %v at %v, want the derived labels and the marker", b.AutoTags, b.AutoTagsAt)
	}
	counts, err := repo.SourceTagCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts["go"] != 2 || counts["caching"] != 1 || counts["reference"] != 1 || len(counts) != 3 {
		t.Fatalf("counts = %v, want go=2 caching=1 reference=1", counts)
	}
	list, err := repo.ListSources(ctx, entities.SourceFilter{}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range list {
		if src.Hash == hashA && len(src.Tags) != 2 {
			t.Fatalf("list row tags = %v, want the labels", src.Tags)
		}
	}
}
