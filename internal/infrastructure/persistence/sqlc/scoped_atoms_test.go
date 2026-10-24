package sqlc

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/infrastructure/persistence"
)

var scopedHash = strings.Repeat("ab", 32)

func scopedAtom(t *testing.T, n byte, topic string) entities.Atom {
	t.Helper()
	var id ulid.ULID
	id[15] = n
	return entities.Atom{
		ID: id, Topic: topic,
		Claim: "A scoped claim about the scoped thing.", Body: "With a body.",
		Type: entities.AtomFact, Status: entities.AtomActive, Path: "atoms/" + string(rune('a'+n)) + ".md",
		Sources:   []entities.AtomSource{{Hash: scopedHash, NodeID: "1.2"}},
		Links:     []entities.Link{{RawTarget: "A scoped claim about the scoped thing.", Position: 0}},
		CreatedAt: time.UnixMilli(1700000000000).UTC(), UpdatedAt: time.UnixMilli(1700000000000 + int64(n)).UTC(),
	}
}

// TestScopedAtomReads guards the stream and the page-scoped queries end to end: each of them runs
// through sqlc's IN-slice expansion, which the sqlite engine has mis-generated before — a broken
// statement shows up as a read error here, not as silently missing provenance in production.
func TestScopedAtomReads(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1_700_000_000_000).UTC()
	if err := NewSourceRepository(db).UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: scopedHash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: scopedHash + ".md", Title: "src", Bytes: 3, TreeState: entities.TreeBuilt,
		ImportedAt: now, Meta: "{}",
	}); err != nil {
		t.Fatalf("source: %v", err)
	}
	repo := NewAtomRepository(db)
	for _, n := range []byte{3, 1, 2} {
		if err := repo.SaveAtom(ctx, scopedAtom(t, n, "caching")); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	if n, err := repo.AtomCount(ctx); err != nil || n != 3 {
		t.Fatalf("count = %d (%v), want 3", n, err)
	}

	var streamed []ulid.ULID
	var streamedSources int
	if err := repo.StreamAtomTexts(ctx, func(a entities.Atom) error {
		streamed = append(streamed, a.ID)
		streamedSources += len(a.Sources) // the stream carries TEXT only - provenance is another read
		return nil
	}); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(streamed) != 3 || streamed[0][15] != 1 || streamed[2][15] != 3 {
		t.Errorf("stream order = %v, want id ascending", streamed)
	}
	if streamedSources != 0 {
		t.Errorf("the stream carried %d provenance rows; it must be text only", streamedSources)
	}

	texts, err := repo.AtomTextsForIDs(ctx, []ulid.ULID{streamed[1]})
	if err != nil || len(texts) != 1 || texts[0].Claim == "" {
		t.Fatalf("texts for ids = %v (%v)", texts, err)
	}

	prov, err := repo.SourcesForAtoms(ctx, streamed)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	if len(prov) != 3 || prov[streamed[0]][0].NodeID != "1.2" {
		t.Errorf("provenance = %v, want one node ref per claim", prov)
	}

	// The list read must hydrate EXACTLY its window — the scoped twin is the whole point.
	// ORDER BY updated_at DESC with offset 1: the newest claim (id 3) is skipped, ids 2 and 1 remain.
	page, err := repo.ListAtoms(ctx, "", 2, 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page) != 2 || page[0].ID[15] != 2 || page[1].ID[15] != 1 {
		t.Fatalf("page = %v, want ids 2,1", page)
	}
	for _, a := range page {
		if len(a.Sources) != 1 || a.SourceCount != 1 || len(a.Links) != 1 {
			t.Errorf("atom %v not hydrated from the scoped reads", a.ID)
		}
	}

	oldest, err := repo.ListOldestAtoms(ctx, 2) // updated_at ASC: ids 1 and 2
	if err != nil || len(oldest) != 2 || oldest[0].ID[15] != 1 || oldest[1].ID[15] != 2 {
		t.Fatalf("oldest = %v (%v), want ids 1,2", oldest, err)
	}
}

// TestClusterListMembershipIsScoped: a paged cluster read carries the membership of the PAGE's
// clusters only. The whole-table read that replaced the N+1 was correct for the two full-vault
// callers and wrong for any page — the same mistake the Sources list made.
func TestClusterListMembershipIsScoped(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1_700_000_000_000).UTC()
	if err := NewSourceRepository(db).UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: scopedHash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: scopedHash + ".md", Title: "src", Bytes: 3, TreeState: entities.TreeBuilt,
		ImportedAt: now, Meta: "{}",
	}); err != nil {
		t.Fatalf("source: %v", err)
	}
	atoms := NewAtomRepository(db)
	ids := make([]ulid.ULID, 0, 4)
	for n := byte(11); n < 15; n++ {
		a := scopedAtom(t, n, "caching")
		if err := atoms.SaveAtom(ctx, a); err != nil {
			t.Fatalf("atom: %v", err)
		}
		ids = append(ids, a.ID)
	}
	repo := NewClusterRepository(db)
	clusters := make([]ulid.ULID, 0, 2)
	for i := range 2 {
		id := ulid.Make()
		clusters = append(clusters, id)
		if err := repo.SaveCluster(ctx, entities.Cluster{
			ID: id, Atoms: ids[i*2 : i*2+2], Created: now, Updated: now,
		}); err != nil {
			t.Fatalf("cluster %d: %v", i, err)
		}
	}
	// Stable order is by id, so the page after the first is deterministic.
	page, err := repo.ListClusters(ctx, 1, 1)
	if err != nil || len(page) != 1 {
		t.Fatalf("page = %v (%v)", page, err)
	}
	want := clusters[1]
	if clusters[0].String() > clusters[1].String() {
		want = clusters[0]
	}
	if page[0].ID != want || len(page[0].Atoms) != 2 {
		t.Errorf("page cluster %s with %d members, want %s with 2", page[0].ID, len(page[0].Atoms), want)
	}
}

// TestMembershipsForAtomsIsScoped: the list rows ask "in which clusters does this claim travel?"
// for the page they return. Asking for one claim must not answer with another's membership.
func TestMembershipsForAtomsIsScoped(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := persistence.Migrate(db); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1_700_000_000_000).UTC()
	if err := NewSourceRepository(db).UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: scopedHash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: scopedHash + ".md", Title: "src", Bytes: 3, TreeState: entities.TreeBuilt,
		ImportedAt: now, Meta: "{}",
	}); err != nil {
		t.Fatalf("source: %v", err)
	}
	atoms := NewAtomRepository(db)
	var ids []ulid.ULID
	for n := byte(21); n < 23; n++ {
		a := scopedAtom(t, n, "caching")
		if err := atoms.SaveAtom(ctx, a); err != nil {
			t.Fatalf("atom: %v", err)
		}
		ids = append(ids, a.ID)
	}
	repo := NewClusterRepository(db)
	cluster := ulid.Make()
	if err := repo.SaveCluster(ctx, entities.Cluster{ID: cluster, Atoms: ids[:1], Created: now, Updated: now}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.MembershipsForAtoms(ctx, ids[:1])
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].AtomID != ids[0] || got[0].ClusterID != cluster {
		t.Fatalf("memberships = %v, want only the requested claim", got)
	}
	if rest, err := repo.MembershipsForAtoms(ctx, ids[1:]); err != nil || len(rest) != 0 {
		t.Fatalf("unclustered claim returned %v (%v), want none", rest, err)
	}
}

// TestAtomReadsBatchLargeIDLists: a cluster pass reads the text and provenance of every member of
// every cluster it touched, and that can be tens of thousands of ids. SQLite accepts a finite number
// of bound variables per statement, so the repository batches instead — one IN (…) with the whole
// list failed with "too many SQL variables" on a real vault.
func TestAtomReadsBatchLargeIDLists(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if mErr := persistence.Migrate(db); mErr != nil {
		t.Fatal(mErr)
	}
	now := time.UnixMilli(1_700_000_000_000).UTC()
	if uErr := NewSourceRepository(db).UpsertSource(ctx, entities.Source{
		ID: ulid.Make(), Hash: scopedHash, Kind: entities.SourceKindMD, Ext: ".md",
		Path: scopedHash + ".md", Title: "src", Bytes: 3, TreeState: entities.TreeBuilt,
		ImportedAt: now, Meta: "{}",
	}); uErr != nil {
		t.Fatalf("source: %v", uErr)
	}
	repo := NewAtomRepository(db)
	ids := make([]ulid.ULID, 0, 1200)
	for i := 0; i < 1200; i++ {
		id := ulid.Make()
		if sErr := repo.SaveAtom(ctx, entities.Atom{
			ID: id, Topic: "batch", Claim: fmt.Sprintf("A batched claim number %d.", i), Body: "b",
			Type: entities.AtomFact, Status: entities.AtomActive, Path: fmt.Sprintf("atoms/batch-%d.md", i),
			Sources:   []entities.AtomSource{{Hash: scopedHash, NodeID: "1.2"}},
			CreatedAt: now, UpdatedAt: now,
		}); sErr != nil {
			t.Fatalf("save %d: %v", i, sErr)
		}
		ids = append(ids, id)
	}

	texts, err := repo.AtomTextsForIDs(ctx, ids)
	if err != nil {
		t.Fatalf("texts: %v", err)
	}
	if len(texts) != len(ids) {
		t.Fatalf("texts = %d, want %d", len(texts), len(ids))
	}
	for i := 1; i < len(texts); i++ {
		if texts[i-1].ID.Compare(texts[i].ID) >= 0 {
			t.Fatalf("texts are not in id order at %d", i)
		}
	}

	prov, err := repo.SourcesForAtoms(ctx, ids)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	if len(prov) != len(ids) {
		t.Fatalf("provenance = %d, want %d", len(prov), len(ids))
	}
}
