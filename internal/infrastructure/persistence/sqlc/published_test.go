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

// TestSourcePublishedSurvivesAPlainUpsert: a source's own date is set by the reading that found it,
// not by the row's existence. A later refresh that lands with a zero date (the page stopped declaring
// one) must keep the date the page declared before — losing it would silently change the list.
func TestSourcePublishedSurvivesAPlainUpsert(t *testing.T) {
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
	hash := strings.Repeat("a1", 32)
	src := entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindURL, Ext: ".md", Path: hash + ".md",
		Title: "Ein Artikel", TreeState: entities.TreeNone,
		ImportedAt: time.UnixMilli(1_700_000_000_000).UTC(), Meta: "{}",
	}
	if err := repo.UpsertSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.GetSourceByHash(ctx, hash); err != nil || !got.PublishedAt.IsZero() {
		t.Fatalf("fresh source = %v (%v), want no date", got.PublishedAt, err)
	}

	at := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	if err := repo.SetSourcePublished(ctx, hash, at, entities.DateFromMeta); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetSourceByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !got.PublishedAt.UTC().Equal(at) || got.PublishedFrom != entities.DateFromMeta {
		t.Fatalf("published = %v/%q, want the date and its layer", got.PublishedAt, got.PublishedFrom)
	}

	// The refresh upsert carries the entity, not the date: the column is set by SetSourcePublished.
	src.Title = "Ein Artikel, neu gelesen"
	if err := repo.UpsertSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetSourceByHash(ctx, hash)
	if !got.PublishedAt.UTC().Equal(at) {
		t.Fatalf("published after a plain upsert = %v, want the date to survive", got.PublishedAt)
	}

	// A row created WITH a date keeps it on the insert path too.
	second := strings.Repeat("b2", 32)
	if err := repo.UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: second, Kind: entities.SourceKindURL, Ext: ".md", Path: second + ".md",
		TreeState: entities.TreeNone, ImportedAt: at, Meta: "{}",
		PublishedAt: at.AddDate(-1, 0, 0), PublishedFrom: entities.DateFromFeed,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.GetSourceByHash(ctx, second)
	if !got.PublishedAt.UTC().Equal(at.AddDate(-1, 0, 0)) || got.PublishedFrom != entities.DateFromFeed {
		t.Fatalf("insert = %v/%q, want the date the row was created with", got.PublishedAt, got.PublishedFrom)
	}
}
