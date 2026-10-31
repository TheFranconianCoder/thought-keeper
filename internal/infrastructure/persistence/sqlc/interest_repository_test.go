package sqlc

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// TestInterestCandidatesCarryEverySignal: the editor's preview reads the whole vault once, and it
// must see the same four signals the ingest matcher sees — the title, the labels, the claim topics
// and the claim texts. A missing signal here would make the preview lie about a rule.
func TestInterestCandidatesCarryEverySignal(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if !persistence.EnsureFTS(db, slog.Default()) {
		t.Skip("no FTS5")
	}
	index := NewSourceRepository(db)
	atoms := NewAtomRepository(db)
	now := time.UnixMilli(1_700_000_000_000).UTC()
	hash := "7f3a1c0000000000000000000000000000000000000000000000000000000000"
	if err := index.UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: hash + ".md", Title: "The Tokio scheduler", Bytes: 10,
		TreeState: entities.TreeBuilt, ImportedAt: now, Meta: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := index.SetSourceTags(ctx, hash, []string{"rust"}); err != nil {
		t.Fatal(err)
	}
	if err := index.SetSourceAutoTags(ctx, hash, []string{"async"}, now); err != nil {
		t.Fatal(err)
	}
	if err := atoms.SaveAtom(ctx, entities.Atom{
		ID: ulid.Make(), Topic: "async-runtime",
		Path: "async-runtime/tokio.md", Claim: "Tokio schedules asynchronous tasks.",
		Body: "The runtime polls futures.", Status: entities.AtomActive,
		Sources: []entities.AtomSource{{Hash: hash, NodeID: "0.1"}},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	repo := NewInterestRepository(db, true)
	candidates, err := repo.Candidates(ctx, []string{"tokio", "async-runtime", "async", "rust"})
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want one", len(candidates))
	}
	c := candidates[0]
	if c.Hash != hash || c.Title != "The Tokio scheduler" {
		t.Fatalf("candidate = %+v", c)
	}
	if sig, ok := c.Signals["tokio"]; !ok || !sig.Title || sig.Claims != 1 {
		t.Fatalf("tokio signal = %+v", sig)
	}
	if sig := c.Signals["async-runtime"]; sig.TopicClaims != 1 || len(sig.Topics) != 1 {
		t.Fatalf("topic signal = %+v", sig)
	}
	if sig := c.Signals["async"]; len(sig.Tags) != 1 || sig.Tags[0] != "async" {
		t.Fatalf("auto tag signal = %+v", sig)
	}
	if sig := c.Signals["rust"]; len(sig.Tags) != 1 || sig.Tags[0] != "rust" {
		t.Fatalf("human tag signal = %+v", sig)
	}

	// An excluded term must be measurable too: the veto reads the same evidence.
	excluded, err := repo.Candidates(ctx, []string{"corrosion"})
	if err != nil || len(excluded) != 0 {
		t.Fatalf("a term the source does not carry produced candidates: %v (%v)", excluded, err)
	}
}
