package usecases

import (
	"context"
	"sort"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// The orientation digest `list_topics` answers. The agent loop cuts one tool answer at 6 000
// characters, so the shelf is shown as a HEAD, never in full: the names the question itself resolves
// onto, then the most used ones. A shelf of a thousand names would not fit, and an unfiltered catalog
// was once answered as a truncated JSON array. Eighty rows of name and count weigh about 4.5 kB even
// when every match carries its mark — a truncated array would be worse than a shorter head.
const (
	topicDigestLimit = 80
	// topicDigestMatches bounds the names put in front because the question names them. A broad
	// question can carry words that many names share, and the head must stay a head.
	topicDigestMatches = 20
)

// topicDigestEntry is one line of the digest: a shelf name, the claims filed under it, and whether the
// question itself resolved onto it.
type topicDigestEntry struct {
	Topic   string
	Claims  int
	Matched bool
}

// topicDigest is the topic SHELF as Ask reads it: the names the claims may be filed under, most used
// first, with the names the QUESTION resolves onto in front.
//
// It reads the shelf record, not the live catalog: the catalog is what the claims carry, so it shows a
// catch-all the refit has not evicted yet and hides a shelf name that carries few claims. A vault with
// no shelf record (one below its target has none — its catalog IS its shelf) falls back to the catalog,
// unfiltered, because there is no target to judge against.
func (uc Query) topicDigest(ctx context.Context, question string) ([]topicDigestEntry, error) {
	topics, err := uc.Atoms.ListClaimTopics(ctx)
	if err != nil {
		return nil, err
	}
	total := claimTotal(topics)
	counts := make(map[string]int, len(topics))
	for _, t := range topics {
		counts[t.Topic] = t.Count
	}

	names, bounded := uc.shelfNames()
	uses := topicUses(names, counts, uc.TopicShelf, total, bounded)
	if len(uses) == 0 && len(topics) > 0 {
		// A shelf whose every name is out of band (a band the corpus cannot fill) must not answer an
		// empty orientation: the catalog is what the vault has. No record, no promise, nothing filtered.
		names = make([]string, 0, len(topics))
		for _, t := range topics {
			names = append(names, t.Topic)
		}
		uses = topicUses(names, counts, uc.TopicShelf, total, false)
	}

	// The catalog is the derivation's IDF matcher: a name scores by the RARE words it shares with the
	// text, so "what does the vault say about caching?" resolves onto `caching` instead of onto every
	// name that happens to carry "software".
	catalog := newTopicCatalog(uses)
	catalog.judgeBy(uc.TopicShelf, total)
	matched := catalog.candidates(question, topicDigestMatches)

	out := make([]topicDigestEntry, 0, min(len(uses), topicDigestLimit))
	seen := make(map[string]bool, len(out))
	add := func(name string, isMatch bool) {
		if len(out) >= topicDigestLimit || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, topicDigestEntry{Topic: name, Claims: counts[name], Matched: isMatch})
	}
	for _, name := range matched {
		add(name, true)
	}
	for _, u := range uses {
		add(u.Topic, false)
	}
	return out, nil
}

// topicUses joins names with their claim counts and orders them most used first. When the names come
// from a SHELF, the share band decides which of them may be shown: a catch-all distinguishes nothing
// and a thin name is not a subject, so the write path may file no claim under either (ADR 0009) and the
// orientation does not offer them.
func topicUses(names []string, counts map[string]int, limits ShelfLimits, total int, bounded bool) []entities.TopicUse {
	uses := make([]entities.TopicUse, 0, len(names))
	for _, name := range names {
		n := counts[name]
		if bounded && (limits.CatchAll(n, total) || limits.Thin(n, total)) {
			continue
		}
		uses = append(uses, entities.TopicUse{Topic: name, Count: n})
	}
	sort.SliceStable(uses, func(i, j int) bool {
		if uses[i].Count != uses[j].Count {
			return uses[i].Count > uses[j].Count
		}
		return uses[i].Topic < uses[j].Topic
	})
	return uses
}

// shelfNames reads the topics SHELF. It answers the names and whether a shelf record exists at all: an
// empty shelf is a vault whose catalog was never bounded, not a shelf of zero names.
func (uc Query) shelfNames() ([]string, bool) {
	if uc.Vocab == nil {
		return nil, false
	}
	v, err := uc.Vocab.Read(entities.VocabularyTopics)
	if err != nil {
		if uc.Log != nil {
			uc.Log.Warn("query: the topic shelf could not be read; the catalog is used instead", "err", err)
		}
		return nil, false
	}
	if len(v.Shelf) == 0 {
		return nil, false
	}
	return v.Shelf, true
}
