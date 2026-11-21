package usecases

import (
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/textmatch"
)

// topicCandidateLimit is how many names a distill request is offered. One request with two dozen
// candidates costs ~250 tokens; the tool call it replaces cost a whole round trip plus ~1,500 tokens
// of a truncated 57k-name list (measured: 1,988 extra requests and 5.0 Mio. tokens in one morning).
const topicCandidateLimit = 24

// topicCatalog is the claim-topic vocabulary as the DERIVATION sees it: the names in use with their
// counts, and an inverted index from a word to the names that carry it. It is built once per distill
// run (one read of the index) and shared by every part, which is what makes a per-part candidate
// question affordable at all.
//
// The parts of one document run in parallel, so every read of the shared state and every write of the
// resolution cache is guarded. The work per call is a map lookup after the first claim of a subject.
type topicCatalog struct {
	names   []string
	counts  []int
	set     map[string]bool
	posting map[string][]int32
	idf     map[string]float64

	mu       sync.Mutex
	resolved map[string]topicResolution
	added    int

	// budget is the configured ceiling of the topic vocabulary. Zero means "no ceiling": a vault
	// without a shelf keeps accepting new names, which is what an unconfigured vault does today.
	budget int
	// headroom is how many names OUTSIDE the shelf a run may keep instead of mapping them onto the
	// best fitting shelf name. It is what lets the shelf follow new content: a genuinely new subject
	// survives as a fine name, the vocabulary report shows it, and the shelf refit either promotes it
	// (it earned the mass) or the coarsening files it in. It is a bounded allowance (a tenth of the
	// shelf), never an open door, and a vault that is still filing its tail has none.
	headroom int
	// limits and total judge a name by its share of ALL claims (ADR 0009): a catch-all or a thin name is
	// neither offered to the model nor used as a mapping target, so a claim is never filed under one
	// just to make the count work. Zero limits judge nothing.
	limits ShelfLimits
	total  int
}

// judgeBy makes the catalog judge its names by their share. total is every claim of the namespace, not
// only the shelf's.
func (c *topicCatalog) judgeBy(l ShelfLimits, total int) {
	c.limits, c.total = l, total
}

// inBand reports whether a name may be offered or used as a mapping target.
func (c *topicCatalog) inBand(idx int32) bool {
	n := c.counts[idx]
	return !c.limits.CatchAll(n, c.total) && !c.limits.Thin(n, c.total)
}

// claimTotal is the number of claims the catalog's names carry together.
func claimTotal(topics []entities.TopicUse) int {
	total := 0
	for _, t := range topics {
		total += t.Count
	}
	return total
}

// newTopicCatalog builds the catalog from the names in use. A catalog with a single name yields no
// useful signal, so candidates come back empty and the model names the subject itself.
func newTopicCatalog(topics []entities.TopicUse) *topicCatalog {
	c := &topicCatalog{
		names:    make([]string, 0, len(topics)),
		counts:   make([]int, 0, len(topics)),
		set:      make(map[string]bool, len(topics)),
		posting:  make(map[string][]int32, len(topics)*2),
		idf:      make(map[string]float64, len(topics)*2),
		resolved: make(map[string]topicResolution, len(topics)/4),
	}
	for _, t := range topics {
		name := strings.TrimSpace(t.Topic)
		if name == "" || c.set[name] {
			continue
		}
		idx := int32(len(c.names))
		c.names = append(c.names, name)
		c.counts = append(c.counts, t.Count)
		c.set[name] = true
		for _, token := range uniqueTokens(name) {
			c.posting[token] = append(c.posting[token], idx)
		}
	}
	// The inverse document frequency is what makes "architecture" identify a name that "software"
	// (in three names) does not. A token that lives in a tenth of the catalog says nothing about a
	// subject, and dropping it is what keeps a part about "the system" from matching everything.
	limit := max(len(c.names)/10, 4)
	n := float64(max(len(c.names), 1))
	for token, names := range c.posting {
		if len(names) > limit {
			delete(c.posting, token)
			continue
		}
		c.idf[token] = math.Log(1+n/float64(len(names))) + 1
	}
	return c
}

// uniqueTokens folds and splits a name into its words once.
func uniqueTokens(text string) []string {
	tokens := textmatch.Tokens(text)
	seen := make(map[string]bool, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// candidates returns the names that fit a text, most relevant first: a name scores by the summed
// IDF of the words it shares with the text, and a name that shares a rare word beats one that shares
// two common ones.
func (c *topicCatalog) candidates(text string, limit int) []string {
	if c == nil || len(c.names) < 2 {
		return nil
	}
	if limit <= 0 {
		limit = topicCandidateLimit
	}
	scores := make(map[int32]float64, limit*4)
	for _, token := range uniqueTokens(text) {
		idf, ok := c.idf[token]
		if !ok {
			continue
		}
		for _, idx := range c.posting[token] {
			if !c.inBand(idx) {
				continue
			}
			scores[idx] += idf
		}
	}
	if len(scores) == 0 {
		return nil
	}
	type scored struct {
		idx   int32
		score float64
	}
	ranked := make([]scored, 0, len(scores))
	for idx, score := range scores {
		ranked = append(ranked, scored{idx: idx, score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return c.names[ranked[i].idx] < c.names[ranked[j].idx]
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]string, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, c.names[r.idx])
	}
	return out
}

// topicResolution is what the write does with a name the model proposed.
type topicResolution struct {
	// Name is the canonical name the claim is written with.
	Name string
	// Proposed is the name the model offered, for the source log.
	Proposed string
	// Mapped reports that the proposed name was replaced (an alias, a near-duplicate or the
	// budget), which is what the source log records as topic_mapped.
	Mapped bool
	// Why names the reason, for the log: "alias", "near-duplicate", "budget".
	Why string
}

// resolve maps a proposed topic onto the catalog: an absorbed name onto the one that took it, a
// spelling of an existing subject onto the existing name, and a genuinely new name is accepted while
// the vocabulary has room. Once the budget is spent the best fitting name wins, so the catalog can
// grow only by making room, never by drifting.
func (c *topicCatalog) resolve(aliases map[string]string, proposed, text string) topicResolution {
	name := strings.TrimSpace(proposed)
	if name == "" {
		return topicResolution{}
	}
	if to, ok := aliases[name]; ok && to != "" && to != name {
		return topicResolution{Name: to, Mapped: true, Why: "alias"}
	}
	if c == nil {
		return topicResolution{Name: name}
	}
	c.mu.Lock()
	if got, ok := c.resolved[name]; ok {
		c.mu.Unlock()
		return got
	}
	c.mu.Unlock()

	if c.set[name] {
		return c.remember(name, topicResolution{Name: name})
	}
	// Two spellings of one subject: every word of the proposal already names an existing name.
	// "cache-invalidation" and "caching-invalidation" are one subject; "caching" and
	// "cache-invalidation" are not, so a single shared word is never enough.
	if best, ok := c.contained(name); ok {
		return c.remember(name, topicResolution{Name: best, Mapped: true, Why: "near-duplicate"})
	}
	c.mu.Lock()
	over := c.budget > 0 && len(c.names)+c.added >= c.budget && c.added >= c.headroom
	c.mu.Unlock()
	if over {
		if best := c.candidates(text, 1); len(best) > 0 {
			// A budget mapping depends on the part's text, so it is not cached: the same spelling
			// in another part may belong to another shelf.
			return topicResolution{Name: best[0], Mapped: true, Why: "budget"}
		}
	}
	c.mu.Lock()
	c.added++
	c.mu.Unlock()
	return c.remember(name, topicResolution{Name: name})
}

// remember caches a resolution that does not depend on the part's text. It is keyed by the name the
// model PROPOSED: keying it by the result would answer a later proposal of the canonical name with
// the mapping that produced it.
func (c *topicCatalog) remember(proposed string, res topicResolution) topicResolution {
	c.mu.Lock()
	c.resolved[proposed] = res
	c.mu.Unlock()
	return res
}

// contained finds the most used name whose words are ALL present in the proposal, which is longer:
// "cache-invalidation-strategy" is a spelling of "cache-invalidation", never a new subject. The
// proposal must genuinely contain the name (a two-word name plus a modifier), and a name of a single
// word never absorbs the proposal — "caching-invalidation" is not "caching".
func (c *topicCatalog) contained(name string) (string, bool) {
	proposed := uniqueTokens(name)
	if len(proposed) < 3 {
		return "", false
	}
	in := make(map[string]bool, len(proposed))
	for _, token := range proposed {
		in[token] = true
	}
	best, bestCount := "", 0
	for _, token := range proposed {
		for _, idx := range c.posting[token] {
			if !c.inBand(idx) {
				continue
			}
			existing := uniqueTokens(c.names[idx])
			if len(existing) < 2 || len(existing) >= len(proposed) {
				continue
			}
			if !allTokensIn(existing, in) {
				continue
			}
			if c.counts[idx] > bestCount {
				best, bestCount = c.names[idx], c.counts[idx]
			}
		}
	}
	return best, best != ""
}

// allTokensIn reports whether every word of an existing name is one of the proposal's words.
func allTokensIn(tokens []string, in map[string]bool) bool {
	if len(tokens) == 0 {
		return false
	}
	for _, token := range tokens {
		if !in[token] {
			return false
		}
	}
	return true
}
