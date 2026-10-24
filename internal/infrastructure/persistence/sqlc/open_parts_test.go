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

// TestAClaimlessDocumentIsScheduledForDistillation: a document with text parts must never be left
// without claims — that is an error the app acts on, not a state it displays. Two queries carry that
// duty, and both must see it:
//
//   - SourcesWithOpenParts: the part is NOT recorded done (a part that produced no claim is
//     `incomplete`, so nothing covers it), so the derivation is unfinished and the next run derives
//     that part again.
//   - SourcesWithoutAtoms: the document has a built outline and no claim at all, which is the
//     other half of the same sweep.
//
// Without them a claim-less document would sit amber with nothing happening, which is exactly the
// "it must be scheduled" the lights promise.
func TestAClaimlessDocumentIsScheduledForDistillation(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.UnixMilli(1_700_000_000_000).UTC()
	hash := strings.Repeat("ef", 32)
	id := ulid.Make()
	sources := NewSourceRepository(db)
	parts := NewPartRepository(db)

	if err := sources.UpsertSource(ctx, entities.Source{
		ID: id, Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: hash + ".md", Title: "src", Bytes: 9, TreeState: entities.TreeBuilt,
		ImportedAt: now, Meta: "{}",
	}); err != nil {
		t.Fatalf("source: %v", err)
	}
	// One text-bearing node: one part, and the reason this document owes claims.
	if err := sources.ReplaceSourceNodes(ctx, id, []entities.SourceNode{
		{NodeID: "0.1", Depth: 1, Ord: 0, Title: "A part", Text: "Some prose with a statement."},
	}); err != nil {
		t.Fatalf("nodes: %v", err)
	}

	has := func(srcs []entities.Source) bool {
		for _, s := range srcs {
			if s.Hash == hash {
				return true
			}
		}
		return false
	}

	// The outline is built and nothing is derived yet: the part is open work.
	open, err := sources.SourcesWithOpenParts(ctx)
	if err != nil {
		t.Fatalf("open parts: %v", err)
	}
	if !has(open) {
		t.Error("a text part with no record must be open work")
	}

	// The part produced no claim: it is recorded incomplete, and that must NOT count as finished —
	// the document still owes its claims, so the derivation stays unfinished.
	if err := parts.SetPartState(ctx, id, entities.PartState{
		NodeID: "0.1", State: entities.PartIncomplete, Reason: "no_claims",
		Atoms: 0, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("part state: %v", err)
	}
	open, err = sources.SourcesWithOpenParts(ctx)
	if err != nil {
		t.Fatalf("open parts: %v", err)
	}
	if !has(open) {
		t.Error("a part that produced no claim must stay open work — it is derived again")
	}
	missing, err := sources.SourcesWithoutAtoms(ctx)
	if err != nil {
		t.Fatalf("without atoms: %v", err)
	}
	if !has(missing) {
		t.Error("a document with a built outline and no claim must be scheduled for distillation")
	}

	// A part recorded done WITHOUT a claim is not finished in fact: the record hangs on a derivation
	// that is gone, so the source is open work again. "No atoms yet" still finds it as well.
	if err := parts.SetPartState(ctx, id, entities.PartState{
		NodeID: "0.1", State: entities.PartDone, Reason: "", Atoms: 2, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("part state: %v", err)
	}
	open, err = sources.SourcesWithOpenParts(ctx)
	if err != nil {
		t.Fatalf("open parts: %v", err)
	}
	if !has(open) {
		t.Error("a part recorded done without a claim must stay open work — its claims are gone")
	}
	missing, err = sources.SourcesWithoutAtoms(ctx)
	if err != nil {
		t.Fatalf("without atoms: %v", err)
	}
	if !has(missing) {
		t.Error("a document whose parts produced no claim is still a document without claims")
	}
}

// TestSourceOpenPartsIgnoresSourceRelative: the per-source count the pipeline asks when a step
// record is ambiguous. A `source_relative` part correctly has no claim, so it is not open work; a
// part recorded done without a claim (its claims were lost) and an incomplete part are.
func TestSourceOpenPartsIgnoresSourceRelative(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if mErr := persistence.Migrate(db); mErr != nil {
		t.Fatalf("migrate: %v", mErr)
	}

	now := time.UnixMilli(1_700_000_000_000).UTC()
	hash := strings.Repeat("ab", 32)
	id := ulid.Make()
	sources := NewSourceRepository(db)
	parts := NewPartRepository(db)
	if uErr := sources.UpsertSource(ctx, entities.Source{
		ID: id, Hash: hash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: hash + ".md", Title: "Links", Bytes: 9, TreeState: entities.TreeBuilt,
		ImportedAt: now, Meta: "{}",
	}); uErr != nil {
		t.Fatalf("source: %v", uErr)
	}
	if nErr := sources.ReplaceSourceNodes(ctx, id, []entities.SourceNode{
		{NodeID: "0.1", Depth: 1, Ord: 0, Title: "A part", Text: "A list of links."},
	}); nErr != nil {
		t.Fatalf("nodes: %v", nErr)
	}

	if pErr := parts.SetPartState(ctx, id, entities.PartState{
		NodeID: "0.1", State: entities.PartDone, Reason: "source_relative", UpdatedAt: now,
	}); pErr != nil {
		t.Fatalf("part: %v", pErr)
	}
	if n, qErr := sources.SourceOpenParts(ctx, hash); qErr != nil || n != 0 {
		t.Fatalf("open parts = %d, %v; want 0 for a source_relative part", n, qErr)
	}

	if pErr := parts.SetPartState(ctx, id, entities.PartState{
		NodeID: "0.1", State: entities.PartDone, Reason: "", UpdatedAt: now,
	}); pErr != nil {
		t.Fatalf("part: %v", pErr)
	}
	if n, qErr := sources.SourceOpenParts(ctx, hash); qErr != nil || n != 1 {
		t.Fatalf("open parts = %d, %v; want 1 for a done part with no claim", n, qErr)
	}

	if pErr := parts.SetPartState(ctx, id, entities.PartState{
		NodeID: "0.1", State: entities.PartIncomplete, Reason: "no_claims", UpdatedAt: now,
	}); pErr != nil {
		t.Fatalf("part: %v", pErr)
	}
	if n, qErr := sources.SourceOpenParts(ctx, hash); qErr != nil || n != 1 {
		t.Fatalf("open parts = %d, %v; want 1 for an incomplete part", n, qErr)
	}
}
