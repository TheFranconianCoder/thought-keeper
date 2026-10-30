package sqlc

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/objects"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

// TestManyAtomsResolveAndCount: the source list counts and the detail view resolves from the
// same rows — with 150 atoms both must see 150. (A row count that disagrees with the detail
// view is not an opinion, it is an inconsistency, and it is what made "143 vs 0" possible.)
func TestManyAtomsResolveAndCount(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	store := objects.New(t.TempDir())
	index := NewSourceRepository(db)
	atomsRepo := NewAtomRepository(db)
	src, err := usecases.IntakeSource{Store: store, Index: index, Clock: testClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "book.md", Reader: strings.NewReader("# Book\nbody\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}

	const n = 150
	for i := range n {
		if err := atomsRepo.SaveAtom(ctx, entities.Atom{
			ID: ulid.Make(), Topic: "eng",
			Path: fmt.Sprintf("eng/a-%d.md", i), Claim: fmt.Sprintf("claim %d", i), Body: "body",
			Status: entities.AtomActive, Sources: []entities.AtomSource{{Hash: src.Hash, NodeID: fmt.Sprintf("1.%d", i)}},
			CreatedAt: testClock{}.Now(), UpdatedAt: testClock{}.Now(),
		}); err != nil {
			t.Fatalf("save atom %d: %v", i, err)
		}
	}

	// The detail view.
	listed, err := atomsRepo.ListAtomsBySource(ctx, src.Hash)
	if err != nil {
		t.Fatalf("list by source: %v", err)
	}
	if len(listed) != n {
		t.Fatalf("detail view resolves %d atoms, want %d", len(listed), n)
	}

	// The list counter (it joins the atoms, so it can never count a claim that cannot be shown).
	rows, err := index.ListSources(ctx, entities.SourceFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("sources = %d, want 1", len(rows))
	}
	if rows[0].AtomCount != n {
		t.Errorf("list counter = %d, want %d", rows[0].AtomCount, n)
	}
}

// TestAClaimCountsOnceEvenWhenItCitesSeveralNodes: the source list's number and the detail view
// must both mean "claims". A claim that cites three nodes of one source is one claim — a
// provenance row per node would otherwise inflate the number and repeat the claim.
func TestAClaimCountsOnceEvenWhenItCitesSeveralNodes(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := objects.New(t.TempDir())
	index := NewSourceRepository(db)
	atomsRepo := NewAtomRepository(db)
	src, err := usecases.IntakeSource{Store: store, Index: index, Clock: testClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "d.md", Reader: strings.NewReader("# D\nbody\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}

	// One claim citing three nodes of the same source.
	if err := atomsRepo.SaveAtom(ctx, entities.Atom{
		ID: ulid.Make(), Topic: "eng", Path: "eng/one.md", Claim: "one claim",
		Body: "b", Status: entities.AtomActive,
		Sources: []entities.AtomSource{
			{Hash: src.Hash, NodeID: "0.1"}, {Hash: src.Hash, NodeID: "0.2"}, {Hash: src.Hash, NodeID: "0.3"},
		},
		CreatedAt: testClock{}.Now(), UpdatedAt: testClock{}.Now(),
	}); err != nil {
		t.Fatalf("save atom: %v", err)
	}

	listed, err := atomsRepo.ListAtomsBySource(ctx, src.Hash)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 {
		t.Errorf("the detail view lists %d entries for one claim, want 1", len(listed))
	}
	rows, err := index.ListSources(ctx, entities.SourceFilter{}, 10, 0)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	if len(rows) != 1 || rows[0].AtomCount != 1 {
		t.Errorf("the list shows %d claims, want 1 (%+v)", rows[0].AtomCount, rows[0])
	}
	if rows[0].ProvenanceRows != 3 {
		t.Errorf("provenance rows = %d, want 3 (raw rows stay raw)", rows[0].ProvenanceRows)
	}
}

// TestTheDetailViewReportsTheSameDerivedStateAsTheList: the overlay is opened FROM a row, and the
// single read filled nothing derived — so a source read "pending distill" in its own detail view
// while the row it came from said "complete". Both now answer from the same numbers.
func TestTheDetailViewReportsTheSameDerivedStateAsTheList(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := persistence.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := objects.New(t.TempDir())
	index := NewSourceRepository(db)
	atomsRepo := NewAtomRepository(db)
	clustersRepo := NewClusterRepository(db)
	parts := NewPartRepository(db)

	src, err := usecases.IntakeSource{Store: store, Index: index, Clock: testClock{}}.
		Execute(ctx, usecases.IntakeInput{Name: "book.md", Reader: strings.NewReader("# Book\nbody\n")})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	now := testClock{}.Now()

	// Two claims: one a cluster owns, one still waiting for one.
	cited := entities.Atom{
		ID: ulid.Make(), Topic: "eng", Path: "eng/cited.md", Claim: "a claim on a page",
		Body: "b", Status: entities.AtomActive,
		Sources: []entities.AtomSource{{Hash: src.Hash, NodeID: "0.1"}}, CreatedAt: now, UpdatedAt: now,
	}
	open := entities.Atom{
		ID: ulid.Make(), Topic: "eng", Path: "eng/open.md", Claim: "a claim without a page",
		Body: "b", Status: entities.AtomActive,
		Sources: []entities.AtomSource{{Hash: src.Hash, NodeID: "0.2"}}, CreatedAt: now, UpdatedAt: now,
	}
	for _, a := range []entities.Atom{cited, open} {
		if err := atomsRepo.SaveAtom(ctx, a); err != nil {
			t.Fatalf("save atom: %v", err)
		}
	}
	if err := clustersRepo.SaveCluster(ctx, entities.Cluster{
		ID: ulid.Make(), Atoms: []ulid.ULID{cited.ID}, Created: now, Updated: now,
	}); err != nil {
		t.Fatalf("save cluster: %v", err)
	}

	// Three nodes, two of them carrying text (a work unit is one text-bearing node); one part done.
	if err := index.ReplaceSourceNodes(ctx, src.ID, []entities.SourceNode{
		{NodeID: "0.1", Ord: 0, Title: "one", Text: "text one"},
		{NodeID: "0.2", Ord: 1, Title: "two", Text: "text two"},
		{NodeID: "0.3", Ord: 2, Title: "three"},
	}); err != nil {
		t.Fatalf("nodes: %v", err)
	}
	if err := parts.SetPartState(ctx, src.ID, entities.PartState{
		NodeID: "0.1", State: entities.PartDone, Atoms: 1, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("part state: %v", err)
	}
	// A metadata part is done without a claim; it counts for progress, not as a derived part.
	if err := parts.SetPartState(ctx, src.ID, entities.PartState{
		NodeID: "0.2", State: entities.PartDone, Reason: "source_relative", UpdatedAt: now,
	}); err != nil {
		t.Fatalf("metadata part state: %v", err)
	}

	rows, err := index.ListSources(ctx, entities.SourceFilter{}, 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list sources: %v (%d rows)", err, len(rows))
	}
	detail, err := index.GetSourceByHash(ctx, src.Hash)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}

	for _, c := range []struct {
		name     string
		list     int
		detail   int
		expected int
	}{
		{"claims", rows[0].AtomCount, detail.AtomCount, 2},
		{"provenance rows", rows[0].ProvenanceRows, detail.ProvenanceRows, 2},
		{"parts", rows[0].PartsTotal, detail.PartsTotal, 2},
		{"parts done", rows[0].PartsDone, detail.PartsDone, 2},
		{"parts derived", rows[0].PartsDerived, detail.PartsDerived, 1},
		{"open claims", rows[0].AtomsLeft, detail.AtomsLeft, 1},
	} {
		if c.detail != c.expected {
			t.Errorf("detail %s = %d, want %d", c.name, c.detail, c.expected)
		}
		if c.list != c.detail {
			t.Errorf("%s disagrees: list = %d, detail = %d", c.name, c.list, c.detail)
		}
	}
}
