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

// TestListRefreshCandidatesOrdersByLastCheck: the paced refresh asks "what waited longest?" once per
// gap, and the answer must be the same list the cadence would have walked: URL sources only, never
// checked first, an attached snapshot left out (its check time never advances, so it would sit at
// the front forever), and a kind that is not a URL never in it.
func TestListRefreshCandidatesOrdersByLastCheck(t *testing.T) {
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

	insert := func(hash, kind, url, meta string) {
		t.Helper()
		if err := repo.UpsertSource(ctx, entities.Source{
			ID: ulid.Make(), Hash: hash, Kind: entities.SourceKind(kind), Ext: "." + kind,
			Path: hash + "." + kind, Title: url, Bytes: 1, TreeState: entities.TreeNone,
			URL: url, ImportedAt: now, Meta: meta,
		}); err != nil {
			t.Fatalf("source %s: %v", url, err)
		}
	}
	checked := func(hash string, ago time.Duration) {
		t.Helper()
		if err := repo.SetSourceURLState(ctx, hash, 200, now.Add(-ago)); err != nil {
			t.Fatalf("check time %s: %v", hash, err)
		}
	}

	never := strings.Repeat("d4", 32)
	oldest := strings.Repeat("a1", 32)
	newer := strings.Repeat("b2", 32)
	attached := strings.Repeat("c3", 32)
	markdown := strings.Repeat("e5", 32)

	insert(never, "url", "https://example.com/never", "{}")
	insert(oldest, "url", "https://example.com/oldest", "{}")
	insert(newer, "url", "https://example.com/newer", "{}")
	insert(attached, "url", "https://example.com/attached", `{"attached":true}`)
	insert(markdown, "md", "", "{}")
	checked(oldest, 30*24*time.Hour)
	checked(newer, 2*24*time.Hour)
	checked(attached, 60*24*time.Hour)

	candidates, err := repo.ListRefreshCandidates(ctx, 10)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	hashes := make([]string, 0, len(candidates))
	for _, src := range candidates {
		hashes = append(hashes, src.Hash)
	}
	want := []string{never, oldest, newer}
	if len(hashes) != len(want) {
		t.Fatalf("candidates = %d (%v), want %d", len(hashes), hashes, len(want))
	}
	for i := range want {
		if hashes[i] != want[i] {
			t.Errorf("candidate %d = %s, want %s (the least recently checked first)", i, hashes[i], want[i])
		}
	}

	one, err := repo.ListRefreshCandidates(ctx, 1)
	if err != nil {
		t.Fatalf("list one candidate: %v", err)
	}
	if len(one) != 1 || one[0].Hash != never {
		t.Fatalf("limit 1 = %+v, want the never-checked source", one)
	}
}
