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

// TestMaxSourceActivityAt: the reader's watermark moves to the newest activity the index knows — the
// last real content change of a page, otherwise the arrival — in the same millisecond clock it
// stores. An empty vault answers the zero time, never 1970.
func TestMaxSourceActivityAt(t *testing.T) {
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

	if newest, maxErr := repo.MaxSourceActivityAt(ctx); maxErr != nil || !newest.IsZero() {
		t.Fatalf("empty vault = %v, %v, want zero", newest, maxErr)
	}

	old := time.UnixMilli(1_700_000_000_000).UTC()
	newestArrival := old.Add(90 * time.Minute)
	// One page arrived earlier but changed later than every arrival: the change is the activity, so
	// the watermark must land on it — that is what settles an "updated" mark.
	changedAt := newestArrival.Add(30 * time.Minute)
	sources := []entities.Source{
		{ImportedAt: old},
		{ImportedAt: newestArrival},
		{ImportedAt: old.Add(time.Hour)},
	}
	hashes := make([]string, 0, len(sources))
	for i, src := range sources {
		hash := strings.Repeat(string(rune('a'+i)), 64)
		src.ID, src.Hash, src.Kind, src.Ext = ulid.Make(), hash, entities.SourceKindMD, ".md"
		src.Path, src.Title, src.TreeState, src.Meta = hash+".md", hash[:4], entities.TreeNone, "{}"
		if upsertErr := repo.UpsertSource(ctx, src); upsertErr != nil {
			t.Fatalf("source %d: %v", i, upsertErr)
		}
		hashes = append(hashes, hash)
	}
	if changedErr := repo.SetSourceURLChanged(ctx, hashes[len(hashes)-1], changedAt); changedErr != nil {
		t.Fatalf("change time: %v", changedErr)
	}

	got, err := repo.MaxSourceActivityAt(ctx)
	if err != nil {
		t.Fatalf("max: %v", err)
	}
	if !got.Equal(changedAt) {
		t.Fatalf("max = %v, want %v", got, changedAt)
	}
}
