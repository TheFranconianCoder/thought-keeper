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

// TestSourceListAggregates pins the numbers the Sources lens shows. The list read used to compute
// five aggregates over the WHOLE tables; it now runs the same five as page-scoped twins
// (`...ForSources`). A missing filter there would not fail anything — it would silently show a
// wrong count — so the values are asserted for a page of exactly two sources, with the kinds of
// overlap that matter: an atom citing one source twice (provenance rows > claims), a node without
// text (not a part), a part that is `done` but `source_relative` (finished, not evidence).
func TestSourceListAggregates(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatal(err)
	}
	sources := NewSourceRepository(db)
	parts := NewPartRepository(db)
	atoms := NewAtomRepository(db)
	clusters := NewClusterRepository(db)

	now := time.UnixMilli(1_700_000_000_000).UTC()
	hashA, hashB := strings.Repeat("a1", 32), strings.Repeat("b2", 32)
	idA, idB := ulid.Make(), ulid.Make()
	for i, src := range []struct {
		id   ulid.ULID
		hash string
		at   time.Time
	}{
		{idA, hashA, now},
		{idB, hashB, now.Add(time.Second)}, // newest: first on the list
	} {
		if err := sources.UpsertSource(ctx, entities.Source{
			ID: src.id, Hash: src.hash, Kind: entities.SourceKindMD, Ext: ".md",
			Path: src.hash + ".md", Title: "src", Bytes: int64(i + 1),
			TreeState: entities.TreeBuilt, ImportedAt: src.at, Meta: "{}",
		}); err != nil {
			t.Fatalf("source: %v", err)
		}
	}

	// A: one text-bearing node, one node without text (metadata, not a part).
	if err := sources.ReplaceSourceNodes(ctx, idA, []entities.SourceNode{
		{NodeID: "0.1", Depth: 1, Ord: 0, Title: "A part", Text: "Some prose with a statement."},
		{NodeID: "0.2", Depth: 1, Ord: 1, Title: "Metadata", Text: ""},
	}); err != nil {
		t.Fatalf("nodes A: %v", err)
	}
	// Two done parts: one derived a claim, one was document metadata.
	if err := parts.SetPartState(ctx, idA, entities.PartState{NodeID: "0.1", State: entities.PartDone, UpdatedAt: now}); err != nil {
		t.Fatalf("part 0.1: %v", err)
	}
	if err := parts.SetPartState(ctx, idA, entities.PartState{NodeID: "0.2", State: entities.PartDone, Reason: "source_relative", UpdatedAt: now}); err != nil {
		t.Fatalf("part 0.2: %v", err)
	}

	newAtom := func(n int, src, node string, at time.Time) entities.Atom {
		id := ulid.Make()
		return entities.Atom{
			ID: id, Topic: "caching", Claim: "A claim that stands alone.", Body: "b",
			Type: entities.AtomFact, Status: entities.AtomActive, Path: id.String() + ".md",
			Sources:   []entities.AtomSource{{Hash: src, NodeID: node}},
			CreatedAt: at, UpdatedAt: at,
		}
	}
	// a1 cites A twice (two nodes, two provenance rows, ONE claim) and is clustered.
	a1 := newAtom(1, hashA, "0.1", now)
	a1.Sources = append(a1.Sources, entities.AtomSource{Hash: hashA, NodeID: "0.2"})
	if err := atoms.SaveAtom(ctx, a1); err != nil {
		t.Fatalf("atom a1: %v", err)
	}
	// a2 cites A once and is NOT clustered: that is one claim left for the association layer.
	a2 := newAtom(2, hashA, "0.1", now)
	if err := atoms.SaveAtom(ctx, a2); err != nil {
		t.Fatalf("atom a2: %v", err)
	}
	// b1 cites B and is clustered.
	b1 := newAtom(3, hashB, "0.1", now)
	if err := atoms.SaveAtom(ctx, b1); err != nil {
		t.Fatalf("atom b1: %v", err)
	}
	if err := clusters.SaveCluster(ctx, entities.Cluster{
		ID: ulid.Make(), Atoms: []ulid.ULID{a1.ID, b1.ID}, Created: now, Updated: now,
	}); err != nil {
		t.Fatalf("cluster: %v", err)
	}

	byHash := func(list []entities.Source) map[string]entities.Source {
		out := make(map[string]entities.Source, len(list))
		for _, s := range list {
			out[s.Hash] = s
		}
		return out
	}
	list, err := sources.ListSources(ctx, entities.SourceFilter{}, 50, 0)
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	got := byHash(list)
	if len(got) != 2 {
		t.Fatalf("sources = %d, want 2", len(got))
	}
	a, b := got[hashA], got[hashB]
	// A's numbers, field by field: two claims cite A (a1 twice, a2 once) but TWO DISTINCT claims;
	// ONE text-bearing node is a part (the second node carries no text, so it is not a work unit);
	// both parts are recorded done, but only one of them was claim-bearing (the other is
	// source_relative); one of A's claims is clustered (a1), so one is left; and A has THREE
	// provenance rows for two claims — the raw-row count the damaged-index hint needs.
	if a.AtomCount != 2 || a.PartsTotal != 1 || a.PartsDone != 2 || a.PartsDerived != 1 ||
		a.AtomsLeft != 1 || a.ProvenanceRows != 3 {
		t.Errorf("A = {atoms:%d parts:%d done:%d derived:%d left:%d provenance:%d}, want {2 1 2 1 1 3}",
			a.AtomCount, a.PartsTotal, a.PartsDone, a.PartsDerived, a.AtomsLeft, a.ProvenanceRows)
	}
	if b.AtomCount != 1 || b.PartsTotal != 0 || b.PartsDone != 0 || b.AtomsLeft != 0 || b.ProvenanceRows != 1 {
		t.Errorf("B = {atoms:%d parts:%d done:%d left:%d provenance:%d}, want {1 0 0 0 1}",
			b.AtomCount, b.PartsTotal, b.PartsDone, b.AtomsLeft, b.ProvenanceRows)
	}

	// The total behind the page: the list shows it and pages on it.
	if n, err := sources.CountSources(ctx, entities.SourceFilter{}); err != nil || n != 2 {
		t.Fatalf("count = %d (%v), want 2", n, err)
	}
	if n, err := sources.CountSources(ctx, entities.SourceFilter{Kind: string(entities.SourceKindMD)}); err != nil || n != 2 {
		t.Fatalf("count by kind = %d (%v), want 2", n, err)
	}

	// The page scoping: B is newest, so limit 1 must return B — and B's aggregates must be its own
	// (never A's, which a missing filter would leak).
	page, err := sources.ListSources(ctx, entities.SourceFilter{}, 1, 0)
	if err != nil || len(page) != 1 || page[0].Hash != hashB {
		t.Fatalf("first page = %v (%v), want the newest source", page, err)
	}
	if page[0].AtomCount != 1 || page[0].ProvenanceRows != 1 || page[0].PartsTotal != 0 {
		t.Errorf("first page aggregates = %+v, want B's own numbers", page[0])
	}
}
