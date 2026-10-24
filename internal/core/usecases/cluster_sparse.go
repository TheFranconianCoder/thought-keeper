package usecases

import (
	"slices"
	"sort"
	"strings"
)

// sparseVec is a term vector whose ids are sorted ascending. Every group sum in the association
// layer is one of these.
//
// Sorted order is the point. A Go map iterates in a random order, so summing squares or taking a dot
// product over a map adds floats in a different order on every run — the same vault could produce
// scores that differ in the last bits, and a score that sits on a threshold decides differently. A
// sorted vector makes every accumulation happen in ONE fixed order, and the run-over-run result
// becomes bit-identical. It is also faster: two groups merge in a linear pass and a dot product is a
// merge join, where the map versions hashed on every probe.
type sparseVec struct {
	ids []int32
	w   []float64
}

// get is the vector's weight for a term, 0 when it does not carry it. Binary search: the postings
// scan asks this question for every candidate it touches.
func (v sparseVec) get(term int32) float64 {
	k := sort.Search(len(v.ids), func(i int) bool { return v.ids[i] >= term })
	if k == len(v.ids) || v.ids[k] != term {
		return 0
	}
	return v.w[k]
}

// normSq is the squared euclidean length, summed in term order.
func (v sparseVec) normSq() float64 {
	total := 0.0
	for _, weight := range v.w {
		total += weight * weight
	}
	return total
}

// l1 is the vector's total mass, summed in term order.
func (v sparseVec) l1() float64 {
	total := 0.0
	for _, weight := range v.w {
		total += weight
	}
	return total
}

// mergeInto adds another sorted vector into this one, keeping the order. A term both carry is summed
// once, in the fixed member order the caller never has to think about.
func (v *sparseVec) mergeInto(other sparseVec) {
	if len(other.ids) == 0 {
		return
	}
	ids := make([]int32, 0, len(v.ids)+len(other.ids))
	w := make([]float64, 0, cap(ids))
	i, j := 0, 0
	for i < len(v.ids) && j < len(other.ids) {
		switch {
		case v.ids[i] < other.ids[j]:
			ids, w = append(ids, v.ids[i]), append(w, v.w[i])
			i++
		case v.ids[i] > other.ids[j]:
			ids, w = append(ids, other.ids[j]), append(w, other.w[j])
			j++
		default:
			ids, w = append(ids, v.ids[i]), append(w, v.w[i]+other.w[j])
			i, j = i+1, j+1
		}
	}
	ids = append(ids, v.ids[i:]...)
	w = append(w, v.w[i:]...)
	ids = append(ids, other.ids[j:]...)
	w = append(w, other.w[j:]...)
	v.ids, v.w = ids, w
}

// dot is the scalar product of two sorted vectors: a merge join, summed in term order.
func dot(a, b sparseVec) float64 {
	i, j := 0, 0
	total := 0.0
	for i < len(a.ids) && j < len(b.ids) {
		switch {
		case a.ids[i] < b.ids[j]:
			i++
		case a.ids[i] > b.ids[j]:
			j++
		default:
			total += a.w[i] * b.w[j]
			i, j = i+1, j+1
		}
	}
	return total
}

// top is the highest-weighted part of a vector, weight descending and ties by term — a total order,
// so the ranking never depends on how the vector was built.
func (v sparseVec) top(names []string, limit int) []termWeight {
	out := make([]termWeight, 0, len(v.ids))
	for k, id := range v.ids {
		out = append(out, termWeight{term: id, weight: v.w[k]})
	}
	slices.SortStableFunc(out, func(a, b termWeight) int {
		if a.weight != b.weight {
			if a.weight > b.weight {
				return -1
			}
			return 1
		}
		return strings.Compare(names[a.term], names[b.term])
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// centroidVec folds a cluster's (term, tf) entries into one sorted IDF-weighted vector. The entries
// arrive in member order; a stable sort by term keeps that order within a term, so the accumulation
// is the same one the whole-corpus build did — just with a fixed iteration order.
func centroidVec(entries []centroidTerm, idf func(int32) float64) sparseVec {
	slices.SortStableFunc(entries, func(a, b centroidTerm) int { return int(a.term) - int(b.term) })
	v := sparseVec{ids: make([]int32, 0, len(entries)), w: make([]float64, 0, len(entries))}
	for k := range entries {
		weight := float64(entries[k].tf) * idf(entries[k].term)
		if n := len(v.ids); n > 0 && v.ids[n-1] == entries[k].term {
			v.w[n-1] += weight
			continue
		}
		v.ids = append(v.ids, entries[k].term)
		v.w = append(v.w, weight)
	}
	return v
}

// normalizedVec is a claim's unit vector in sorted term order — what a group sum accumulates.
func (c *claimCorpus) normalizedVec(i int) sparseVec {
	ids, weights := c.raw(i)
	norm := c.norms[i]
	if norm == 0 {
		return sparseVec{}
	}
	v := sparseVec{ids: make([]int32, len(ids)), w: make([]float64, len(weights))}
	copy(v.ids, ids)
	for k := range weights {
		v.w[k] = weights[k] / norm
	}
	return v
}

// dotNormalized is the claim's unit vector against a sorted group vector: a merge join over the
// claim's own (sorted) terms.
func (c *claimCorpus) dotNormalizedVec(i int, target sparseVec) float64 {
	ids, weights := c.raw(i)
	norm := c.norms[i]
	if norm == 0 {
		return 0
	}
	total := 0.0
	j := 0
	for k, id := range ids {
		for j < len(target.ids) && target.ids[j] < id {
			j++
		}
		if j == len(target.ids) {
			break
		}
		if target.ids[j] == id {
			total += (weights[k] / norm) * target.w[j]
		}
	}
	return total
}
