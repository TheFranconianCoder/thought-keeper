package usecases

import (
	"sort"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// The deterministic term vocabulary of the association graph: which words of a claim are content,
// and how a group that is too large is cut into similar chunks. No embeddings, no model.

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// stopwords is a deliberately small list: enough to keep "the page is about the" out of the graph,
// short enough to stay out of the way of technical terms.
var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "from": true,
	"are": true, "was": true, "were": true, "its": true, "his": true, "her": true, "their": true,
	"they": true, "them": true, "you": true, "your": true, "our": true, "not": true, "but": true,
	"can": true, "has": true, "have": true, "had": true, "one": true, "two": true, "all": true,
	"nicht": true, "eine": true, "einer": true, "eines": true, "und": true, "oder": true, "auch": true,
	"more": true, "most": true, "some": true, "such": true, "which": true, "when": true, "where": true,
	"into": true, "over": true, "than": true, "then": true, "these": true, "those": true, "there": true,
	"because": true, "used": true, "using": true, "use": true, "example": true, "also": true,
	"written": true, "develop": true, "developed": true, "makes": true, "provides": true,
	"provide": true, "includes": true, "include": true, "based": true, "many": true, "often": true,
	"other": true, "others": true, "first": true, "different": true, "according": true,
}

// termSet is an atom's content terms: lowercase words of some length, without the usual filler.
func termSet(a entities.Atom) map[string]bool {
	out := map[string]bool{}
	text := a.Claim + " " + a.Body
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '+' && r != '#'
	}) {
		if len(w) < 3 || stopwords[w] || isNumeric(w) {
			continue
		}
		out[w] = true
	}
	return out
}

// splitBySimilarity cuts one group's claims into chunks of at most maxPerPage, keeping related claims
// together: a chunk grows from a seed by the candidate that shares most terms with the chunk's union,
// and falls back to document order once nothing shares a term.
func splitBySimilarity(atoms []entities.Atom, maxPerPage int, common func(string) bool) [][]entities.Atom {
	if maxPerPage <= 0 || len(atoms) <= maxPerPage {
		return [][]entities.Atom{atoms}
	}
	terms := make([]map[string]bool, len(atoms))
	for i, a := range atoms {
		terms[i] = distinguishingTerms(a, common)
	}
	taken := make([]bool, len(atoms))
	var groups [][]entities.Atom
	for {
		seed := -1
		for i := range atoms {
			if !taken[i] {
				seed = i
				break
			}
		}
		if seed < 0 {
			break
		}
		taken[seed] = true
		group := []entities.Atom{atoms[seed]}
		union := map[string]bool{}
		for t := range terms[seed] {
			union[t] = true
		}
		for len(group) < maxPerPage {
			best, bestScore := -1, 0.0
			for i := range atoms {
				if taken[i] {
					continue
				}
				score := overlap(union, terms[i])
				if score > bestScore {
					best, bestScore = i, score
				}
			}
			if best < 0 {
				for i := range atoms {
					if !taken[i] {
						best = i
						break
					}
				}
				if best < 0 {
					break
				}
			}
			taken[best] = true
			group = append(group, atoms[best])
			for t := range terms[best] {
				union[t] = true
			}
		}
		groups = append(groups, group)
	}
	return groups
}

// overlap is the share of the group's terms that the candidate also carries (0..1).
func overlap(group, candidate map[string]bool) float64 {
	if len(group) == 0 || len(candidate) == 0 {
		return 0
	}
	hits := 0
	for t := range candidate {
		if group[t] {
			hits++
		}
	}
	return float64(hits) / float64(len(candidate))
}

// distinguishingTerms is an atom's content terms without the ones the whole group shares.
func distinguishingTerms(a entities.Atom, common func(string) bool) map[string]bool {
	out := map[string]bool{}
	for t := range termSet(a) {
		if common != nil && common(t) {
			continue
		}
		out[t] = true
	}
	return out
}

// clusterTerms names what a group is about: its most frequent content terms, ties broken by first
// appearance (so the result is stable). It is used only for logging/probe output now: a cluster has
// no title.
func clusterTerms(atoms []entities.Atom, common func(string) bool) []string {
	counts := map[string]int{}
	var firstSeen []string
	for _, a := range atoms {
		for t := range distinguishingTerms(a, common) {
			if counts[t] == 0 {
				firstSeen = append(firstSeen, t)
			}
			counts[t]++
		}
	}
	if len(firstSeen) == 0 {
		for _, a := range atoms {
			for t := range termSet(a) {
				if counts[t] == 0 {
					firstSeen = append(firstSeen, t)
				}
				counts[t]++
			}
		}
	}
	sort.SliceStable(firstSeen, func(i, j int) bool { return counts[firstSeen[i]] > counts[firstSeen[j]] })
	if len(firstSeen) > 6 {
		firstSeen = firstSeen[:6]
	}
	return firstSeen
}
