package usecases

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// The golden fingerprint is the guard for the association layer's ONE promise: the same claims and
// the same settings always produce the same graph. Replacing the per-claim term maps with a flat,
// interned representation must not move a single claim — so this test runs the deterministic phase
// over a fixed synthetic vault and hashes the result. It is written against the shapes the pass
// produces (memberships, metrics, attached claims), never against the shapes inside the code.

// fixedID makes a deterministic claim id. ulid.Make() draws random bytes, and the graph tie-breaks
// on the id string, so a corpus that wants to be reproducible must fix them itself.
func fixedID(seed *uint64, n int) ulid.ULID {
	var u ulid.ULID
	*seed = *seed*6364136223846793005 + 1442695040888963407
	u[0] = byte(n >> 8)
	u[1] = byte(n)
	for i := 2; i < len(u); i++ {
		*seed = *seed*6364136223846793005 + 1442695040888963407
		u[i] = byte(*seed >> 33)
	}
	return u
}

// goldenCorpus is a small vault with the shapes the pass is tuned on: several tight subjects, a
// broad pile that must be split, isolated singletons that only the topic can group, and provenance
// that ties claims to outline nodes.
func goldenCorpus() ([]entities.Atom, []entities.Cluster) {
	subjects := []struct {
		topic string
		terms []string
	}{
		{"caching", []string{"cache", "invalidation", "ttl", "eviction", "coherence", "entry"}},
		{"raii", []string{"destructor", "lifetime", "acquisition", "initialization", "scope", "resource"}},
		{"std-containers", []string{"vector", "map", "iterator", "contiguous", "allocator", "element"}},
		{"move-semantics", []string{"ownership", "rvalue", "steal", "transfer", "constructor", "source"}},
		{"sql-indexing", []string{"btree", "covering", "planner", "index", "scan", "vacuum"}},
		{"wal-concurrency", []string{"checkpoint", "journal", "writer", "snapshot", "commit", "readers"}},
		{"http-caching", []string{"etag", "validation", "freshness", "proxy", "cache", "header"}},
		{"graph-algorithms", []string{"component", "traversal", "centroid", "overlap", "bridge", "sparse"}},
	}
	// A shared filler vocabulary makes the near-duplicate and the generic-term noise real.
	filler := []string{"system", "pattern", "approach", "mechanism", "property", "behaviour", "layer", "design"}

	var seed uint64 = 20261023
	next := func() int {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int(seed>>33) & 0xffff
	}

	atoms := make([]entities.Atom, 0, 900)
	for si, subject := range subjects {
		count := 60 + (si%3)*20 // 60, 80, 100 claims per subject
		for c := range count {
			var claim, body strings.Builder
			// Two or three content terms per claim, always from the subject's vocabulary, plus
			// filler so the graph has to weigh rare against common.
			for k := 0; k < 2+(next()%2); k++ {
				claim.WriteString(subject.terms[next()%len(subject.terms)])
				claim.WriteByte(' ')
			}
			fmt.Fprintf(&claim, "keeps %s of the %s bounded by %d.", filler[next()%len(filler)], subject.topic, next()%97)
			for k := 0; k < 2+(next()%3); k++ {
				body.WriteString(subject.terms[next()%len(subject.terms)])
				body.WriteByte(' ')
			}
			fmt.Fprintf(&body, "The %s of %s is what %s observes in %s.", filler[next()%len(filler)], subject.topic, filler[next()%len(filler)], subject.terms[next()%len(subject.terms)])
			id := fixedID(&seed, si*1000+c)
			atoms = append(atoms, entities.Atom{
				ID:    id,
				Topic: subject.topic,
				Claim: claim.String(),
				Body:  body.String(),
				// Two claims per (subject, node): the node seed is the strongest signal the graph has.
				Sources: []entities.AtomSource{{Hash: fmt.Sprintf("%064x", si*7+1), NodeID: fmt.Sprintf("%d.%d", si, c/2)}},
			})
		}
	}
	// A pile of isolated claims sharing only one generic word: mergeLeftovers and splitOversized.
	for c := range 200 {
		atoms = append(atoms, entities.Atom{
			ID:    fixedID(&seed, 90000+c),
			Topic: fmt.Sprintf("misc-%d", c%25),
			Claim: fmt.Sprintf("A system note %d mentions the cache and the %s design.", c, filler[c%len(filler)]),
			Body:  fmt.Sprintf("Observed while reading %s of the layer.", filler[(c*7)%len(filler)]),
		})
	}
	// Claims with no topic at all: the unsorted branch of splitBroad.
	for c := range 40 {
		atoms = append(atoms, entities.Atom{
			ID:    fixedID(&seed, 95000+c),
			Claim: fmt.Sprintf("Untitled observation %d about %s and %s.", c, filler[c%len(filler)], subject(filler, c)),
			Body:  fmt.Sprintf("Nothing labels %s, so only the words travel.", filler[(c*3)%len(filler)]),
		})
	}

	// Existing records own the FIRST six subjects (every eleventh claim stays free of them, so the
	// pass has both paths to run: ATTACH into what exists and GRAPH what does not). The seventh
	// subject, the misc pile and the untitled claims are no cluster's member yet — the graph has to
	// group them from scratch.
	ownedPrefix := 0
	for si := range 6 {
		ownedPrefix += 60 + (si%3)*20
	}
	owned := make([]int, 0, ownedPrefix)
	for i := 0; i < ownedPrefix; i++ {
		if i%11 != 0 {
			owned = append(owned, i)
		}
	}
	records := make([]entities.Cluster, 0, 40)
	for i := 0; i < len(owned); i += 17 {
		end := min(i+17, len(owned))
		members := make([]ulid.ULID, 0, end-i)
		for _, idx := range owned[i:end] {
			members = append(members, atoms[idx].ID)
		}
		records = append(records, entities.Cluster{ID: fixedID(&seed, 70000+i), Atoms: members})
	}
	// One record names a claim that is gone: the prune path must be part of the fingerprint too.
	ghost := entities.Cluster{ID: fixedID(&seed, 79000), Atoms: []ulid.ULID{atoms[3].ID, fixedID(&seed, 79999)}}
	records = append(records, ghost)
	return atoms, records
}

func subject(words []string, i int) string { return words[i%len(words)] }

// clusterFingerprint is the canonical text of one deterministic pass: memberships, then the metrics
// the pass measured, then what attach decided. Everything is sorted, so the hash says ONE thing:
// the association did not move.
func clusterFingerprint(t *testing.T, atoms []entities.Atom, candidates []clusterAssignment, g *claimGraph, attached []string, written map[string]string) string {
	t.Helper()
	var b strings.Builder
	for _, a := range candidates {
		names := make([]string, 0, len(a.Members))
		for _, m := range a.Members {
			names = append(names, g.atoms[m].ID.String())
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "C|%s\n", strings.Join(names, ","))
	}
	for _, a := range candidates {
		m := g.metricsOf(a.Members, g.settings)
		fmt.Fprintf(&b, "M|%d|%.6f|%.6f|%.6f|%d|%d|%s\n", m.Claims, m.Internal, m.External, m.TopicEntropy, m.Topics, m.Cores, strings.Join(m.CoreTerms, "+"))
	}
	fmt.Fprintf(&b, "A|%s\n", strings.Join(sortedCopy(attached), ","))
	ids := make([]string, 0, len(written))
	for id := range written {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(&b, "W|%s|%s\n", id, written[id])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestClusterPassGoldenFingerprint(t *testing.T) {
	atoms, records := goldenCorpus()
	settings := DefaultClusterSettings()
	ctx := context.Background()
	repo := &goldenAtomRepo{atoms: atoms}
	store := &goldenClusterStore{written: map[string]string{}}
	cRepo := &goldenClusterRepo{}
	uc := ClusterPass{Atoms: repo, Store: store, Clusters: cRepo}

	// 1. prune: a record that names a vanished claim.
	exists := make(map[ulid.ULID]bool, len(atoms))
	for _, a := range atoms {
		exists[a.ID] = true
	}
	records, pruned, err := uc.prune(ctx, records, exists)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	// 2. attach, over the streamed corpus exactly as the pass runs it.
	attachedCount, changed, free, err := uc.attach(ctx, records, settings)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}

	// 3. the graph over what is STILL free, and the overlap pass.
	ownedAgain := map[ulid.ULID]bool{}
	for _, rec := range records {
		for _, id := range rec.Atoms {
			ownedAgain[id] = true
		}
	}
	remaining := make([]entities.Atom, 0, len(free))
	for _, a := range free {
		if !ownedAgain[a.ID] {
			remaining = append(remaining, a)
		}
	}
	ids := make([]ulid.ULID, 0, len(remaining))
	for i := range remaining {
		ids = append(ids, remaining[i].ID)
	}
	prov, err := repo.SourcesForAtoms(ctx, ids)
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	for i := range remaining {
		remaining[i].Sources = prov[remaining[i].ID]
	}
	// The fingerprint is only worth something if BOTH paths ran: attaching to what exists AND
	// graphing what does not. A corpus that quietly made one of them empty would freeze nothing.
	if attachedCount == 0 {
		t.Fatal("the golden corpus attached nothing: the attach path is not covered")
	}
	if len(remaining) < 50 {
		t.Fatalf("the golden corpus left %d claims for the graph: the graph path is not covered", len(remaining))
	}
	g := buildClaimGraph(remaining, settings)
	candidates := g.candidates()
	assignments := make([]clusterAssignment, 0, len(candidates))
	for i := range candidates {
		assignments = append(assignments, clusterAssignment{ID: candidates[i].ID, Members: append([]int(nil), candidates[i].Members...)})
	}
	assignments = addOverlap(assignments, g, settings)
	if len(assignments) == 0 {
		t.Fatal("the golden corpus produced no candidates: the graph path is not covered")
	}

	attached := make([]string, 0, attachedCount)
	for _, id := range changed {
		attached = append(attached, id.String())
	}
	for _, id := range pruned {
		attached = append(attached, "pruned:"+id.String())
	}
	got := clusterFingerprint(t, atoms, assignments, g, attached, store.written)

	var members int
	for _, a := range assignments {
		members += len(a.Members)
	}
	t.Logf("GOLDEN claims=%d free=%d graph=%d attached=%d candidates=%d members=%d\n  fingerprint=%s",
		len(atoms), len(free), len(remaining), attachedCount, len(assignments), members, got)

	if want := goldenFingerprint; got != want {
		t.Fatalf("the deterministic pass moved: fingerprint\n got %s\nwant %s", got, want)
	}
}

// goldenAtomRepo is the claim index of the golden vault: enough of the AtomRepository for the pass
// to run its stream. The real index streams claims in id order (the primary key) — so does this.
type goldenAtomRepo struct {
	interfaces.AtomRepository
	atoms []entities.Atom
}

func (r *goldenAtomRepo) sorted() []entities.Atom {
	out := append([]entities.Atom(nil), r.atoms...)
	slices.SortStableFunc(out, func(a, b entities.Atom) int { return bytes.Compare(a.ID[:], b.ID[:]) })
	return out
}

func (r *goldenAtomRepo) StreamAtomTexts(_ context.Context, fn func(entities.Atom) error) error {
	for _, a := range r.sorted() {
		if err := fn(entities.Atom{ID: a.ID, Topic: a.Topic, Claim: a.Claim, Body: a.Body}); err != nil {
			return err
		}
	}
	return nil
}

func (r *goldenAtomRepo) AtomTextsForIDs(_ context.Context, ids []ulid.ULID) ([]entities.Atom, error) {
	want := make(map[ulid.ULID]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []entities.Atom
	for _, a := range r.atoms {
		if want[a.ID] {
			out = append(out, entities.Atom{ID: a.ID, Topic: a.Topic, Claim: a.Claim, Body: a.Body})
		}
	}
	return out, nil
}

func (r *goldenAtomRepo) AtomCount(context.Context) (int, error) { return len(r.atoms), nil }

func (r *goldenAtomRepo) SourcesForAtoms(_ context.Context, ids []ulid.ULID) (map[ulid.ULID][]entities.AtomSource, error) {
	want := make(map[ulid.ULID]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := map[ulid.ULID][]entities.AtomSource{}
	for _, a := range r.atoms {
		if want[a.ID] && len(a.Sources) > 0 {
			out[a.ID] = append(out[a.ID], a.Sources...)
		}
	}
	return out, nil
}

// goldenFingerprint is recorded from the map-based implementation. It is the promise the flat
// representation has to keep: the same claims and settings produced 258 attachments and 105
// candidates with exactly these memberships and metrics.
const goldenFingerprint = "2fc0622802ac38df7f493844244fcf7c08b9e315a77f82fadf2dd14392fff35e"

// goldenClusterStore records what the pass wrote so the fingerprint can name it; the rest of the
// interface is never reached by these tests and is embedded on purpose.
type goldenClusterStore struct {
	interfaces.ClusterStore
	written map[string]string
}

func (s *goldenClusterStore) WriteMeta(_ context.Context, c entities.Cluster) error {
	s.written[c.ID.String()] = fmt.Sprintf("%d|%s", len(c.Atoms), c.Signature[:16])
	return nil
}

func (s *goldenClusterStore) RemoveMeta(_ context.Context, id ulid.ULID) error {
	s.written["removed:"+id.String()] = "1"
	return nil
}

type goldenClusterRepo struct {
	interfaces.ClusterRepository
	saved []ulid.ULID
}

func (r *goldenClusterRepo) SaveCluster(_ context.Context, c entities.Cluster) error {
	r.saved = append(r.saved, c.ID)
	return nil
}

func (r *goldenClusterRepo) DeleteCluster(_ context.Context, id ulid.ULID) error {
	r.saved = append(r.saved, id)
	return nil
}
