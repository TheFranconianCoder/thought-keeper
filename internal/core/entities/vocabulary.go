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
	// Shelf is the namespace's TARGET vocabulary: the names a subject may be filed under. It is what
	// the coarsening maps the long tail onto, and what the derivation may choose from. Empty means no
	// shelf has been built yet (a vault whose names were never bounded).
	Shelf []string
	// Target is the size the shelf was built for (vocabulary.<namespace>.max_names at the time). It
	// is a RECORD of the decision, so a later pass knows what the shelf was aiming at. It is a
	// CEILING, not a quota: the share band (ADR 0009) decides how many names are really usable.
	Target int
	// ShelfBuilt is the version of the RULE the shelf was built under, the sibling of Settle. Version
	// 0 — a file that does not carry one — was built from the catalog in ALPHABETICAL order, so its
	// names were the alphabetically first ones instead of the most used. Such a shelf is rebuilt once:
	// the record cannot be repaired by a refit alone because the names it carries were never a real
	// target. A rebuild also re-opens Decided and Relabelled, which were judged against the old shelf.
	ShelfBuilt int
	// Decided are the names the model has already judged — merged, deliberately kept, or passed over
	// by a coarsening that could not file them onto the shelf. Without it a curation asks the same
	// head of the catalog forever: the baseline makes every observed name "canonical", so nothing
	// reads as new, and a catalog of thousands never moves past its top. For the coarsening it is the
	// same memory: a tail name is a QUESTION once, and the answer is a decision either way.
	Decided []string
	// Relabelled are the names whose CLAIMS were re-derived one by one against the shelf (the
	// retopic pass). A name that keeps its claims after that is the subject itself, not an unplaced
	// tail: it is a remainder the report shows, never a question that comes back.
	Relabelled []string
	// Settle is the version of the ASSIGNMENT RULE the coarsening decided under. A new rule has to
	// reach the names an older one settled as "cannot be filed", or the tail it closed stays closed
	// forever; a version the file does not carry is version 0 (the rule that let two thirds of the tail
	// through), and the next pass asks those names again.
	Settle  int
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

// Known reports whether a name is one this vocabulary has an opinion about: on the shelf, canonical,
// absorbed into one, or judged and kept as it is — a name a coarsening asked about and the shelf could
// not cover is a decision too. A name that is none of those is NEW: it arrived after the last pass, and
// it is what the next one looks at.
func (v Vocabulary) Known(name string) bool {
	if _, ok := v.Absorbed[name]; ok {
		return true
	}
	if slices.Contains(v.Shelf, name) || slices.Contains(v.Canonical, name) {
		return true
	}
	return slices.Contains(v.Decided, name)
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
