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

// TestMaxSourceImportedAt: the reader's watermark moves to the newest arrival the index knows, in
// the same millisecond clock it stores. An empty vault answers the zero time, never 1970.
func TestMaxSourceImportedAt(t *testing.T) {
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

	if newest, maxErr := repo.MaxSourceImportedAt(ctx); maxErr != nil || !newest.IsZero() {
		t.Fatalf("empty vault = %v, %v, want zero", newest, maxErr)
	}

	old := time.UnixMilli(1_700_000_000_000).UTC()
	newestAt := old.Add(90 * time.Minute)
	for i, at := range []time.Time{old, newestAt, old.Add(time.Hour)} {
		hash := strings.Repeat(string(rune('a'+i)), 64)
		if upsertErr := repo.UpsertSource(ctx, entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
			Path: hash + ".md", Title: hash[:4], TreeState: entities.TreeNone,
			ImportedAt: at, Meta: "{}",
		}); upsertErr != nil {
			t.Fatalf("source %d: %v", i, upsertErr)
		}
	}

	got, err := repo.MaxSourceImportedAt(ctx)
	if err != nil {
		t.Fatalf("max: %v", err)
	}
	if !got.Equal(newestAt) {
		t.Fatalf("max = %v, want %v", got, newestAt)
	}
}
