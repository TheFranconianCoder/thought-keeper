package usecases

import (
	"maps"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// claimCorpus is the term vectors of one set of claims in a FLAT shape: a term interned to an id,
// and per claim one sorted array of ids with their IDF weights.
//
// The association step used to hold three Go maps per claim (the term counts, the weighted vector
// and a normalised copy of it). At 170k claims that is ~900 MB of hash tables, and because ATTACH
// scores a free claim against every existing cluster, those maps were built for the WHOLE vault —
// a pass with 135 claims to place paid for 170k. The corpus is the same vectors with the hash
// tables removed: a term exists as a string exactly ONCE (the intern table), a claim's vector is a
// slice of (int32 id, float64 weight), and "ids ascending" turns the map join into a binary search.
// The weights stay float64: the scores are compared against thresholds, and a smaller type would
// move claims across them.
type claimCorpus struct {
	atoms []entities.Atom
	// terms is the intern table: a term's id is its index, so one term costs one string in TOTAL,
	// not one per claim that carries it.
	terms []string
	index map[string]int32
	// starts[i]..starts[i+1] is claim i's slice of ids and weights.
	starts  []int32
	ids     []int32
	weights []float64
	norms   []float64
	l1s     []float64
	// top is the highest-weighted head of each claim's vector, weight descending and ties by term —
	// the order the candidate scan walks. Padded to a fixed width so it stays addressable.
	top  []int32
	topW []float64
}

// topTermsPerClaim bounds how much of a claim's vector the candidate scan walks: a term below this
// rank contributes more noise than signal, and the full vector still scores the edge.
const topTermsPerClaim = 16

// corpusSeed is a corpus built on statistics someone else counted: the association pass walks the
// WHOLE vault as a stream and learns the vocabulary and the document frequency there, then asks for
// the vectors of one subset (the free claims) with those global numbers. A seeded corpus numbers
// its terms exactly like the global walk would, so the vectors are the same bits the two-pass read
// produced — only the memory changes. total is the corpus size the IDF divides by; it is the size
// of the WHOLE corpus, not of the subset.
type corpusSeed struct {
	df    []int32
	terms []string
	index map[string]int32
	total int
}

// buildClaimCorpus turns claims into their vectors. Two passes over the text: the first counts the
// terms and learns the document frequency the IDF needs, the second weights the same counts.
func buildClaimCorpus(atoms []entities.Atom) *claimCorpus {
	return newClaimCorpus(atoms, nil)
}

func newClaimCorpus(atoms []entities.Atom, seed *corpusSeed) *claimCorpus {
	c := &claimCorpus{
		atoms:   atoms,
		index:   make(map[string]int32, 4096),
		starts:  make([]int32, len(atoms)+1),
		norms:   make([]float64, len(atoms)),
		l1s:     make([]float64, len(atoms)),
		top:     make([]int32, len(atoms)*topTermsPerClaim),
		topW:    make([]float64, len(atoms)*topTermsPerClaim),
		ids:     make([]int32, 0, len(atoms)*24),
		weights: make([]float64, 0, len(atoms)*24),
	}
	var df []int32
	if seed != nil {
		c.terms = slices.Clone(seed.terms)
		maps.Copy(c.index, seed.index)
		// Copy by appending (from a zero-length slice): the pass-1 grow path below must stay the
		// ONLY thing that lengthens df, so the two can never disagree about a term's id.
		df = make([]int32, 0, len(seed.df))
		df = append(df, seed.df...)
	}
	if len(atoms) == 0 {
		return c
	}

	// One counting map for the tokenization, cleared per claim: 170k small hash tables were the bulk
	// of the old cost, and a claim shares most of its terms with the claim before it.
	counts := map[string]float64{}
	var tokenizer termScratch
	var scratch []int32
	var keys []string
	for i := range atoms {
		clear(counts)
		addWeightedTerms(counts, atoms[i].Claim, 2.0, &tokenizer)
		addWeightedTerms(counts, atoms[i].Body, 1.0, &tokenizer)
		addWeightedTerms(counts, atoms[i].Topic, 0.25, &tokenizer)
		scratch = scratch[:0]
		// Interning order is a DECISION, not an accident: the ids decide the order of every later
		// float accumulation, and a map range would hand out a different numbering on every run.
		// Sorted term names give a claim the same ids in every process.
		keys = keys[:0]
		for term := range counts {
			keys = append(keys, term)
		}
		slices.Sort(keys)
		for _, term := range keys {
			id, ok := c.index[term]
			if !ok {
				id = int32(len(c.terms))
				c.terms = append(c.terms, term)
				c.index[term] = id
				df = append(df, 0)
			}
			scratch = append(scratch, id)
		}
		slices.Sort(scratch)
		c.starts[i] = int32(len(c.ids))
		for _, id := range scratch {
			c.ids = append(c.ids, id)
			c.weights = append(c.weights, counts[c.terms[id]])
			if seed == nil {
				// A seed's df already counted this claim — the stream that built it walked the
				// whole corpus, the free claims included. Counting again would deflate every IDF.
				df[id]++
			}
		}
	}
	c.starts[len(atoms)] = int32(len(c.ids))

	// Second pass: tf -> IDF weight. The write cursor never passes the read cursor, so the vectors
	// compact in place and a term that carries no weight is gone from the arrays — not just from the
	// ranking, because the group sums and the postings index walk the arrays.
	n := float64(len(atoms) + 1)
	if seed != nil {
		n = float64(seed.total + 1)
	}
	ranked := make([]int, 0, 64)
	write := int32(0)
	for i := range atoms {
		readStart, readEnd := c.starts[i], c.starts[i+1]
		c.starts[i] = write
		ranked = ranked[:0]
		normSq, l1 := 0.0, 0.0
		for k := readStart; k < readEnd; k++ {
			id := c.ids[k]
			weight := c.weights[k] * (math.Log(n/float64(df[id]+1)) + 0.25)
			if weight <= 0 {
				continue
			}
			c.ids[write] = id
			c.weights[write] = weight
			ranked = append(ranked, int(write))
			normSq += weight * weight
			l1 += weight
			write++
		}
		c.norms[i] = math.Sqrt(normSq)
		c.l1s[i] = l1
		// The order is total (weight, then term), so a stable sort buys nothing — and a sort that
		// allocates once per claim would be the map cost back in the side door.
		slices.SortFunc(ranked, func(a, b int) int {
			wa, wb := c.weights[a], c.weights[b]
			if wa != wb {
				if wa > wb {
					return -1
				}
				return 1
			}
			return strings.Compare(c.terms[c.ids[a]], c.terms[c.ids[b]])
		})
		base := i * topTermsPerClaim
		for k := 0; k < topTermsPerClaim && k < len(ranked); k++ {
			c.top[base+k] = c.ids[ranked[k]]
			c.topW[base+k] = c.weights[ranked[k]]
		}
	}
	c.starts[len(atoms)] = write
	c.ids = c.ids[:write]
	c.weights = c.weights[:write]
	return c
}

// raw is the claim's unnormalised IDF vector: parallel arrays, ids ascending.
func (c *claimCorpus) raw(i int) ([]int32, []float64) {
	start, end := c.starts[i], c.starts[i+1]
	return c.ids[start:end], c.weights[start:end]
}

// size is how many claims the corpus holds.
func (c *claimCorpus) size() int { return len(c.atoms) }

// name is the term an id stands for.
func (c *claimCorpus) name(term int32) string { return c.terms[term] }

// norm is the euclidean length of a claim's vector.
func (c *claimCorpus) norm(i int) float64 { return c.norms[i] }

// l1 is a claim's total term mass.
func (c *claimCorpus) l1(i int) float64 { return c.l1s[i] }

// weight is one term of one claim, 0 when the claim does not carry it.
func (c *claimCorpus) weight(i int, term int32) float64 {
	start, end := int(c.starts[i]), int(c.starts[i+1])
	k := sort.Search(end-start, func(x int) bool { return c.ids[start+x] >= term })
	if start+k >= end || c.ids[start+k] != term {
		return 0
	}
	return c.weights[start+k]
}

// rawVec is a claim's unnormalised vector in sorted term order. It aliases the corpus's own slices:
// a merge only reads it, so no copy is needed.
func (c *claimCorpus) rawVec(i int) sparseVec {
	ids, weights := c.raw(i)
	return sparseVec{ids: ids, w: weights}
}

// topTerms is the highest-weighted head of a claim's vector with its weights, weight descending.
func (c *claimCorpus) topTerms(i int) ([]int32, []float64) {
	base := i * topTermsPerClaim
	end := base + topTermsPerClaim
	for end > base && c.topW[end-1] == 0 {
		end--
	}
	return c.top[base:end], c.topW[base:end]
}

// termWeight is one term of a group sum in the ranked shape the postings index wants: weight
// descending, ties by the term itself, so the ranking does not depend on map order.
type termWeight struct {
	term   int32
	weight float64
}
