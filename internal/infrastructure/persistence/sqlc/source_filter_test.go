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

// TestSourceListWindowFiltersTheCount: the Sources lens asks for a WINDOW, but the count it gets
// back is the count of the FILTERED list. A filter must therefore be part of both statements, not
// applied to a page after the fact.
func TestSourceListWindowFiltersTheCount(t *testing.T) {
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
	insert := func(hash, kind, title, meta string) {
		t.Helper()
		if err := repo.UpsertSource(ctx, entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKind(kind), Ext: "." + kind,
			Path: hash + "." + kind, Title: title, Bytes: 1, TreeState: entities.TreeNone,
			ImportedAt: now, Meta: meta,
		}); err != nil {
			t.Fatalf("source %s: %v", title, err)
		}
	}

	hashRust := strings.Repeat("11", 32)
	hashGo := strings.Repeat("22", 32)
	hashPDF := strings.Repeat("33", 32)
	hashAttached := strings.Repeat("44", 32)
	hashBookmark := strings.Repeat("55", 32)
	hashFailed := strings.Repeat("66", 32)
	insert(hashRust, "md", "Rust ownership", `{"language":"en"}`)
	insert(hashGo, "md", "Go interfaces", `{"language":"en"}`)
	insert(hashPDF, "pdf", "Rust in practice", `{"language":"en"}`)
	insert(hashAttached, "url", "A walled page", `{"url":"https://example.com","attached":true}`)
	insert(hashFailed, "md", "A failed capture", `{"language":"en"}`)
	// A bookmark is captured but never outlined/distilled; the failed source records a red light.
	if err := repo.UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: hashBookmark, Kind: entities.SourceKindURL, Ext: ".url",
		Path: hashBookmark + ".url", Title: "A saved link", Bytes: 1, TreeState: entities.TreeNone,
		Bookmark: true, ImportedAt: now, Meta: `{"url":"https://example.org"}`,
	}); err != nil {
		t.Fatalf("bookmark: %v", err)
	}
	if err := repo.SetSourceAutoTags(ctx, hashBookmark, []string{"reading"}, now); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSourceSteps(ctx, hashFailed, entities.StepSet{
		entities.StepCapture: {State: entities.StepFailed, Error: "endpoint timeout"},
	}); err != nil {
		t.Fatalf("failed steps: %v", err)
	}

	if err := repo.SetSourceTags(ctx, hashRust, []string{"rust", "systems"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSourceTags(ctx, hashGo, []string{"go"}); err != nil {
		t.Fatal(err)
	}

	check := func(name string, filter entities.SourceFilter, want int) {
		t.Helper()
		rows, err := repo.ListSources(ctx, filter, 100, 0)
		if err != nil {
			t.Fatalf("%s: list: %v", name, err)
		}
		if len(rows) != want {
			t.Errorf("%s: rows = %d, want %d", name, len(rows), want)
		}
		n, err := repo.CountSources(ctx, filter)
		if err != nil {
			t.Fatalf("%s: count: %v", name, err)
		}
		if n != want {
			t.Errorf("%s: count = %d, want %d", name, n, want)
		}
	}

	check("all", entities.SourceFilter{}, 6)
	check("kind", entities.SourceFilter{Kind: "pdf"}, 1)
	check("query title", entities.SourceFilter{Query: "rust"}, 2)
	check("query tag", entities.SourceFilter{Query: "systems"}, 1)
	check("tag", entities.SourceFilter{Tag: "go"}, 1)
	check("attached", entities.SourceFilter{AttachedOnly: true}, 1)
	check("bookmarks", entities.SourceFilter{BookmarksOnly: true}, 1)
	check("failed", entities.SourceFilter{FailedOnly: true}, 1)
	check("bookmark + query", entities.SourceFilter{BookmarksOnly: true, Query: "saved"}, 1)
	check("no match", entities.SourceFilter{Query: "no-such-source"}, 0)

	// A page's count is the filtered total even when only one row was asked for.
	list, err := repo.ListSources(ctx, entities.SourceFilter{Query: "rust"}, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("window = %d rows, want 1", len(list))
	}
	if n, err := repo.CountSources(ctx, entities.SourceFilter{Query: "rust"}); err != nil || n != 2 {
		t.Fatalf("filtered total = %d (%v), want 2", n, err)
	}

	attached, err := repo.CountAttachedSources(ctx)
	if err != nil || attached != 1 {
		t.Fatalf("attached = %d (%v), want 1", attached, err)
	}
}
