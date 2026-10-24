package entities

import (
	"slices"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// VocabularyNamespace is the name vocabulary the agent maintains: what a thing is CALLED, as opposed
// to what it says. Three of them: the flat SUBJECT of a claim, and the coarse TAGS of a wiki page and
// of a source (two shelves, kept apart because a page and a source are filed differently).
//
// Names grow with every run and drift apart (caching, cache-invalidation, Caching Strategies), so
// they are curated afterwards: merge what means the same, and re-derive a claim's subject from the
// claim itself. A tag is the one name level ABOVE a topic — the shelf a page is filed under — and it
// is curated the same way, by renaming the page's frontmatter.
type VocabularyNamespace string

const (
	// VocabularyTopics is the flat subject of a CLAIM (`caching`, `llm-agents`).
	VocabularyTopics VocabularyNamespace = "topics"
	// VocabularyWikiTags is the coarse label of a WIKI PAGE: what a reader filters the page list by.
	VocabularyWikiTags VocabularyNamespace = "wiki-tags"
	// VocabularySourceTags is the coarse label of a SOURCE: what a reader filters the source list by.
	// It is a separate namespace from wiki tags on purpose: the same word can be a useful shelf in one
	// list and noise in the other.
	VocabularySourceTags VocabularyNamespace = "source-tags"
)

// VocabularyNamespaces is every namespace.
func VocabularyNamespaces() []VocabularyNamespace {
	return []VocabularyNamespace{VocabularyTopics, VocabularyWikiTags, VocabularySourceTags}
}

// Vocabulary is one namespace's canonical names, plus what they absorbed.
//
// It is a RECORD, not a cache: it is the agent's decision about what things are called, so it lives in
// the vault and a reindex reads it back. The OBSERVED catalog (which names exist right now) stays where
// it belongs — in the index — and the two answer different questions.
type Vocabulary struct {
	Namespace VocabularyNamespace
	// Canonical is the curated set. For topics these are full paths ("software-engineering/caching");
	// for branches and tags they are plain names.
	Canonical []string
	// Absorbed maps a name that was merged away onto the canonical name it became. It is the alias
	// table: a later write with the old name is mapped onto the canonical one, and the next
	// curation does not have to ask about it again.
	Absorbed map[string]string
	// BroadTokens are the tokens that are too generic to distinguish one topic name from another in
	// THIS vault ("go", "rust", "benchmark", ...). They are learned from the observed catalog (with
	// the model) and used only by the near-duplicate detector, never removed from a topic name.
	BroadTokens []string
	// Decided are the names the model has already judged — merged or deliberately kept. Without it a
	// curation asks the same head of the catalog forever: the baseline makes every observed name
	// "canonical", so nothing reads as new, and a catalog of thousands never moves past its top.
	Decided []string
	Updated time.Time
	// Run is the run that last wrote this vocabulary (zero for a file written before the runs existed).
	Run ulid.ULID
}

// BroadTokenSet is the learned broad-token set as a lookup table.
func (v Vocabulary) BroadTokenSet() map[string]bool {
	out := make(map[string]bool, len(v.BroadTokens))
	for _, t := range v.BroadTokens {
		out[strings.ToLower(strings.TrimSpace(t))] = true
	}
	return out
}

// Judged reports whether the model has already decided about a name (kept it, or merged it away).
func (v Vocabulary) Judged(name string) bool {
	return slices.Contains(v.Decided, name)
}

// Known reports whether a name is one this vocabulary has an opinion about: canonical, or absorbed into
// one. A name that is neither is NEW — it arrived after the last curation, and it is what the next
// pass looks at.
func (v Vocabulary) Known(name string) bool {
	if _, ok := v.Absorbed[name]; ok {
		return true
	}
	return slices.Contains(v.Canonical, name)
}

// Resolve maps a name onto its canonical form: an absorbed name becomes the one that absorbed it,
// everything else is itself.
func (v Vocabulary) Resolve(name string) string {
	if to, ok := v.Absorbed[name]; ok {
		return to
	}
	return name
}

// Names is every name the vocabulary knows: the canonical ones and the ones they absorbed. It is how a
// curation tells "new since the last pass" from "already decided".
func (v Vocabulary) Names() []string {
	out := make([]string, 0, len(v.Canonical)+len(v.Absorbed))
	out = append(out, v.Canonical...)
	for from := range v.Absorbed {
		out = append(out, from)
	}
	return out
}
