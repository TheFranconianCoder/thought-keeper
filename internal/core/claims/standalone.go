// Package claims holds the rules that make one claim stand on its own.
//
// A claim is knowledge, not a reading note. Its topic, claim and body must be understandable
// without the source: no "Chapter 11", no "this section", no "the book", no reading order. The
// source remains provenance in `sources[]`; it is never part of the statement.
package claims

import (
	"errors"
	"regexp"
	"strings"
)

// Structural topic names are labels of a DOCUMENT, not subjects. They cannot be derived from the
// claim and body alone, and they become meaningless as soon as the source is gone.
var structuralTopic = regexp.MustCompile(`(?i)^(?:chapters?|appendix|appendices|appendixes|part|section|figure|table|listing|exercise)[-_]?(?:[0-9]+|[a-z])?$|^(?:book|reading-order|table-of-contents|front-matter|back-matter)(?:[-_].*)?$|.*-book$`)

// sourceRelative matches statements that point back into the source instead of saying what is true.
var sourceRelative = []*regexp.Regexp{
	// Chapter 4, Appendix A, Section 2.3, Figure 7, Table 3, Listing 1, Exercise 5.
	regexp.MustCompile(`(?i)\b(?:chapters?|appendices?|appendix|sections?|figures?|tables?|listings?|exercises?)\s+(?:[0-9]+|[A-Z])\b`),
	// this chapter, the book, the author, the reader, this edition, the text, the paper.
	regexp.MustCompile(`(?i)\b(?:this|the|that)\s+(?:chapters?|sections?|parts?|appendices?|appendix|book|document|paper|article|text|author|authors|reader|readers|edition|page)\b`),
	// later chapters, the previous section, the next part, earlier chapters.
	regexp.MustCompile(`(?i)\b(?:above|below|earlier|later|previous|next|preceding|following)\s+(?:chapters?|sections?|parts?|appendices?|appendix)\b`),
	// as we saw, as mentioned earlier, as discussed above.
	regexp.MustCompile(`(?i)\bas\s+(?:we|you|i)\s+(?:saw|seen|mentioned|discussed|described|said)\b`),
	// the Rust book, the reference book (a title or description of the source).
	regexp.MustCompile(`(?i)\b(?:this|the|that)\s+(?:\w+\s+){0,2}book\b`),
	// table of contents, reading order, front to back, the rest of the book.
	regexp.MustCompile(`(?i)\b(?:table\s+of\s+contents|reading\s+order|front\s+to\s+back|the\s+rest\s+of\s+the\s+book)\b`),
}

// ErrNotStandalone marks a claim that cannot be understood without its source.
var ErrNotStandalone = errors.New("claim is not standalone")

// NotStandaloneError carries the reasons a claim was rejected.
type NotStandaloneError struct {
	Reasons []string
}

func (e NotStandaloneError) Error() string {
	return ErrNotStandalone.Error() + ": " + strings.Join(e.Reasons, "; ")
}

func (e NotStandaloneError) Unwrap() error { return ErrNotStandalone }

// TopicIsStructural reports a topic that names the document instead of a subject.
func TopicIsStructural(topic string) bool {
	return structuralTopic.MatchString(strings.TrimSpace(topic))
}

// Issues returns the reasons a claim is not self-contained. An empty result means it can stand on
// its own: topic, claim and body, with no source in hand.
func Issues(topic, claim, body string) []string {
	var out []string
	if t := strings.TrimSpace(topic); t == "" {
		out = append(out, "topic is empty")
	} else if TopicIsStructural(t) {
		out = append(out, `topic "`+t+`" names the document's structure, not a subject`)
	}
	text := strings.TrimSpace(claim + "\n" + body)
	for _, re := range sourceRelative {
		if hit := re.FindString(text); hit != "" {
			out = append(out, `statement refers to the source ("`+strings.TrimSpace(hit)+`")`)
		}
	}
	return out
}

// Standalone is the boolean form of Issues, for callers that only need to reject.
func Standalone(topic, claim, body string) bool {
	return len(Issues(topic, claim, body)) == 0
}
