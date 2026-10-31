// Package interests decides whether a source belongs to a reader's interest.
//
// The rule is deliberately lexical and explainable. A source matches when its CLAIM TOPICS, its
// labels, its title, or enough of its claim texts carry the interest's terms. The result is a BAND,
// never a score the reader has to trust: "fits" (queue it), "maybe" (show it, do not queue it), or
// nothing. Every band carries its evidence, because an automatism that cannot say WHY it put
// something in the queue is an automatism nobody keeps.
package interests

import (
	"sort"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/textmatch"
)

// Bands. "fits" is the only band that queues; "maybe" is a suggestion the reader may accept.
const (
	BandFits  = "fits"
	BandMaybe = "maybe"
)

// ClaimText is one claim as the matcher sees it: its flat topic, the sentence and the body.
type ClaimText struct {
	Topic string
	Claim string
	Body  string
}

// SourceText is everything the matcher reads from one source. It is built from the index (claims,
// title, tags) and never from the source's bytes: an interest is a filter over what the vault knows.
type SourceText struct {
	Title    string
	Tags     []string
	AutoTags []string
	Claims   []ClaimText
}

// TermSignal is what one term found in one source.
type TermSignal struct {
	Topics      []string // the claim topics the term names
	TopicClaims int      // claims under those topics
	Claims      int      // claims whose text carries the term
	Title       bool     // the title carries the term
	Tags        []string // the source labels that carry the term
}

// Empty reports a term that found nothing.
func (s TermSignal) Empty() bool {
	return s.TopicClaims == 0 && s.Claims == 0 && !s.Title && len(s.Tags) == 0
}

// Signals maps a term (as the reader wrote it) to what it found.
type Signals map[string]TermSignal

// TermEvidence is the wire/rail view of one term's contribution.
type TermEvidence struct {
	Term   string   `json:"term"`
	Topics []string `json:"topics,omitempty"`
	Claims int      `json:"claims,omitempty"`
	Title  bool     `json:"title,omitempty"`
	Tags   []string `json:"tags,omitempty"`
}

// Match is one source's result for one interest. Band is empty when the source does not match.
type Match struct {
	Band   string         `json:"band,omitempty"`
	Terms  []TermEvidence `json:"terms,omitempty"`
	Topics []string       `json:"topics,omitempty"`
	Score  int            `json:"-"`
}

// Matched reports a band that is worth showing.
func (m Match) Matched() bool { return m.Band != "" }

// ForSource builds the signals of one source for one interest. It is the incremental path: the
// source's claims and labels are already in hand, so the answer is a few string comparisons.
func ForSource(src SourceText, in entities.Interest) Signals {
	out := Signals{}
	tags := append(append([]string(nil), src.Tags...), src.AutoTags...)
	// Excluded terms are measured too: a veto can only fire on evidence, and the evidence is the
	// same four signals.
	raws := append(append([]string(nil), in.Terms...), in.Exclude...)
	seen := map[string]bool{}
	for _, raw := range raws {
		if seen[raw] {
			continue
		}
		seen[raw] = true
		term := textmatch.NewTerm(raw)
		if term.Empty() {
			continue
		}
		var sig TermSignal
		if term.InText(src.Title) {
			sig.Title = true
		}
		for _, tag := range tags {
			if term.InText(tag) {
				sig.Tags = append(sig.Tags, tag)
			}
		}
		seenTopic := map[string]bool{}
		for _, claim := range src.Claims {
			if term.InTopic(claim.Topic) {
				sig.TopicClaims++
				if !seenTopic[claim.Topic] {
					seenTopic[claim.Topic] = true
					sig.Topics = append(sig.Topics, claim.Topic)
				}
			}
			tokens := textmatch.Tokens(claim.Claim + " " + claim.Body)
			if term.InTokens(tokens) {
				sig.Claims++
			}
		}
		if !sig.Empty() {
			sort.Strings(sig.Topics)
			sort.Strings(sig.Tags)
			out[raw] = sig
		}
	}
	return out
}

// Evaluate turns the signals into a band and its evidence. It is the ONE decision every path
// shares: the incremental matcher, the preview and the backfill all end here.
//
// The rule is intentionally conservative:
//   - a claim topic, a label or the title is a strong hit;
//   - TWO claim mentions are a theme; ONE mention is only a suggestion ("maybe").
//
// One mention is easy to produce by accident (a comparison, a footnote). Two are a topic. That is
// why a single-term interest with one claim waits for a second mention before it queues, and why
// "maybe" is shown in the source's rail but never becomes a chip in the list.
func Evaluate(in entities.Interest, signals Signals) Match {
	if vetoed(in, signals) {
		return Match{}
	}
	var (
		evidence  []TermEvidence
		topics    = map[string]bool{}
		totalHits int
		score     int
		meta      bool
	)
	for _, raw := range in.Terms {
		sig, ok := signals[raw]
		if !ok || sig.Empty() {
			continue
		}
		evidence = append(evidence, TermEvidence{
			Term: raw, Topics: sig.Topics, Claims: sig.Claims, Title: sig.Title, Tags: sig.Tags,
		})
		for _, topic := range sig.Topics {
			topics[topic] = true
		}
		totalHits += sig.Claims
		if sig.Title || len(sig.Tags) > 0 || sig.TopicClaims > 0 {
			meta = true
		}
		score += sig.TopicClaims*4 + sig.Claims + boolInt(sig.Title)*5 + len(sig.Tags)*5
	}
	if len(evidence) == 0 {
		return Match{}
	}
	band := BandMaybe
	if meta || totalHits >= 2 {
		band = BandFits
	}
	out := Match{Band: band, Terms: evidence, Score: score}
	for topic := range topics {
		out.Topics = append(out.Topics, topic)
	}
	sort.Strings(out.Topics)
	return out
}

// vetoed answers whether an exclude term names something in this source. An exclusion is absolute:
// a source that is also about the wrong meaning is not a match, however many right words it has.
func vetoed(in entities.Interest, signals Signals) bool {
	for _, raw := range in.Exclude {
		if sig, ok := signals[raw]; ok && !sig.Empty() {
			return true
		}
	}
	return false
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
