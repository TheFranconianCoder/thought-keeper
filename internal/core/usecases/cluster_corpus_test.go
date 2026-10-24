package usecases

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// syntheticCorpus is a scaled vault: the same shape as a real one (short claims, a subject
// vocabulary per topic, a shared filler vocabulary, provenance that ties claims to outline nodes).
// The association step is the one place where the cost is per CLAIM, so it is the place that needs a
// size to argue against.
func syntheticCorpus(n int) []entities.Atom {
	seed := uint64(20261023)
	next := func() int {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int(seed>>33) & 0xffff
	}
	subjects := make([][]string, 0, 16)
	for s := range 16 {
		terms := make([]string, 0, 6)
		for k := range 6 {
			terms = append(terms, fmt.Sprintf("subject%d-term%d", s, k))
		}
		subjects = append(subjects, terms)
	}
	filler := make([]string, 0, 24)
	for k := range 24 {
		filler = append(filler, fmt.Sprintf("mechanism%d", k))
	}
	atoms := make([]entities.Atom, 0, n)
	for i := range n {
		subject := subjects[i%len(subjects)]
		var claim, body strings.Builder
		for range 3 {
			claim.WriteString(subject[next()%len(subject)])
			claim.WriteByte(' ')
		}
		fmt.Fprintf(&claim, "bounds the %s of %d.", filler[next()%len(filler)], i)
		for range 4 {
			body.WriteString(subject[next()%len(subject)])
			body.WriteByte(' ')
		}
		fmt.Fprintf(&body, "The %s observes %s %d.", filler[next()%len(filler)], filler[next()%len(filler)], i)
		atoms = append(atoms, entities.Atom{
			ID:    fixedID(&seed, i),
			Topic: fmt.Sprintf("topic-%d", i%len(subjects)),
			Claim: claim.String(),
			Body:  body.String(),
			Sources: []entities.AtomSource{
				{Hash: fmt.Sprintf("%064x", i/500+1), NodeID: fmt.Sprintf("%d.%d", i/50, i%50)},
			},
		})
	}
	return atoms
}

// BenchmarkClaimCorpus is the memory budget of the association step: the term vectors are the one
// structure that scales with the CLAIM COUNT, and the pass used to hold three hash maps per claim
// (3.8 kB of them at a real vault's 170k claims — a 900 MB peak for a step that had 135 claims to
// place). The corpus is the same vectors flat: this benchmark is the gate that keeps it flat.
func BenchmarkClaimCorpus(b *testing.B) {
	for _, size := range []int{1_000, 10_000, 100_000} {
		atoms := syntheticCorpus(size)
		b.Run(fmt.Sprintf("claims=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var terms int
			for range b.N {
				corpus := buildClaimCorpus(atoms)
				terms += len(corpus.terms)
			}
			_ = terms
		})
	}
}

// BenchmarkClaimGraph is the whole sparse graph over the free claims: the candidate scan that the
// posting bounds keep proportional to the claim count.
func BenchmarkClaimGraph(b *testing.B) {
	atoms := syntheticCorpus(10_000)
	b.ReportAllocs()
	for range b.N {
		g := buildClaimGraph(atoms, ClusterSettings{})
		_ = g.candidates()
	}
}

// TestClaimCorpusMemory is the regression gate in the shape the suite runs: the vectors may cost
// bytes per CLAIM, but not hash maps per claim. 10 kB a claim was the old shape; the flat corpus
// holds the same vectors in a fraction of it.
func TestClaimCorpusMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("measuring the heap is not a short test")
	}
	const n = 20_000
	atoms := syntheticCorpus(n)
	before := heapAlloc()
	corpus := buildClaimCorpus(atoms)
	held := heapAlloc() - before
	per := float64(held) / float64(n)
	t.Logf("corpus: %d claims, %d terms, %.0f B/claim", corpus.size(), len(corpus.terms), per)
	// The claim texts themselves are in `atoms` and stay there; the corpus adds ids, weights, the
	// head of each vector and the intern table. The map shape measured 3.792 B/claim at the same
	// corpus size — three hash tables per claim — and that is what made a 170k-claim vault cost
	// ~900 MB for a pass that had 135 claims to place. The gate is set loose enough for the
	// intern table and the vector head, tight enough to notice a map coming back.
	if per > 2048 {
		t.Errorf("the corpus costs %.0f B/claim: the vectors went back to hash maps (want < 2048, was 3792)", per)
	}
	corpus = nil
	runtime.GC()
	if after := heapAlloc(); after > before+1<<20 {
		t.Logf("heap after release: %d B (before %d B)", after, before)
	}
}

func heapAlloc() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}
