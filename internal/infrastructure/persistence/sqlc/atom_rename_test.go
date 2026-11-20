package sqlc

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// TestRenameAtomTopicsMovesOnlyTheTopic: a coarsening renames a claim, it does not rewrite it. The
// text, the provenance and the search row stay, and one transaction carries every claim of the batch.
func TestRenameAtomTopicsMovesOnlyTheTopic(t *testing.T) {
	ctx := context.Background()
	db, atoms, _, _, atomID := seedVault(t)
	if !persistence.EnsureFTS(db, slog.New(slog.NewTextHandler(io.Discard, nil))) {
		t.Skip("this SQLite build has no FTS5")
	}

	before, err := atoms.GetAtom(ctx, atomID)
	if err != nil {
		t.Fatalf("get before: %v", err)
	}
	hits := func() int {
		var n int
		if qErr := db.QueryRowContext(ctx,
			`SELECT count(*) FROM atoms_fts WHERE atoms_fts MATCH 'claim'`).Scan(&n); qErr != nil {
			t.Fatalf("fts lookup: %v", qErr)
		}
		return n
	}
	if got := hits(); got != 1 {
		t.Fatalf("fts hits before = %d, want 1", got)
	}

	if err := atoms.RenameAtomTopics(ctx, map[ulid.ULID]string{atomID: "caching"}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	after, err := atoms.GetAtom(ctx, atomID)
	if err != nil {
		t.Fatalf("get after: %v", err)
	}
	if after.Topic != "caching" {
		t.Errorf("topic = %q, want caching", after.Topic)
	}
	if after.Claim != before.Claim || after.Body != before.Body {
		t.Errorf("the rename rewrote the text: %q / %q", after.Claim, after.Body)
	}
	if len(after.Sources) != 1 || after.Sources[0].Hash != before.Sources[0].Hash {
		t.Errorf("the rename touched the provenance: %+v", after.Sources)
	}
	// The search index still answers for the unchanged text.
	if got := hits(); got != 1 {
		t.Errorf("fts hits = %d, want 1 after a rename", got)
	}
}

// TestRenameAtomTopicsKeepsTheAtomInItsSource: the provenance survives, so the source's claim list
// still answers after a coarsening — an atom that loses it becomes invisible and unattributable.
func TestRenameAtomTopicsKeepsTheAtomInItsSource(t *testing.T) {
	ctx := context.Background()
	_, atoms, _, _, atomID := seedVault(t)

	before, err := atoms.GetAtom(ctx, atomID)
	if err != nil {
		t.Fatalf("get before: %v", err)
	}
	hash := before.Sources[0].Hash
	if err := atoms.RenameAtomTopics(ctx, map[ulid.ULID]string{atomID: "testing"}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	claims, err := atoms.ListAtomsBySource(ctx, hash)
	if err != nil {
		t.Fatalf("list by source: %v", err)
	}
	found := false
	for _, a := range claims {
		if a.ID == atomID && a.Topic == "testing" {
			found = true
		}
	}
	if !found {
		t.Errorf("the renamed claim left its source: %+v", claims)
	}
}
