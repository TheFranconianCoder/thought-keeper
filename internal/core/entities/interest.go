package entities

import "time"

// Interest is one standing rule the reader keeps: the terms that describe what they want to read,
// and the label the queue shows when a new source matches it.
//
// It is NOT a tag and NOT a topic. A tag labels a thing; a topic labels a claim; an interest is the
// reader's attention over time. The file interests/<slug>.md is the truth (it is a decision, not an
// observation), the index never stores it, and a reindex reads it back — exactly like the
// vocabulary. Its matches are computed when a source arrives, never written into the source's
// labels: the queue entry carries the slug, and the source's evidence is computed on demand.
type Interest struct {
	// Slug is the identity: derived from the name at creation and stable for the rule's lifetime,
	// so renaming the display name does not orphan the queue entries that point at it.
	Slug string
	Name string
	// Terms are the words or phrases that make a source relevant. A multi-word term is a
	// conjunction. They are stored as the reader typed them; matching folds and stems them.
	Terms []string
	// Exclude vetoes a match: a term that names something the reader does not mean ("korrosion"
	// for an interest called Rust).
	Exclude []string
	// Kinds narrows the rule to source kinds (md/pdf/txt/epub/url). Empty means every kind.
	Kinds []string
	// Enabled pauses the rule without losing it or its queue history.
	Enabled bool
	// Rejected is the per-source correction: "this source is not about this interest". It lives
	// with the rule because it is a judgement about the pair, and deleting the rule should delete it.
	Rejected []string
	Created  time.Time
	Updated  time.Time
}

// MatchesKind reports whether the rule applies to a source kind. An empty list applies to all.
func (i Interest) MatchesKind(kind SourceKind) bool {
	if len(i.Kinds) == 0 {
		return true
	}
	for _, k := range i.Kinds {
		if k == string(kind) {
			return true
		}
	}
	return false
}

// Rejects reports whether this source was already ruled out for the interest.
func (i Interest) Rejects(hash string) bool {
	for _, h := range i.Rejected {
		if h == hash {
			return true
		}
	}
	return false
}
