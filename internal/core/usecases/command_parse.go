package usecases

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// ParsedCommand is one utterance read as a lens plus the criteria it named. Notes are the honest
// footnotes about the reading ("topic was read as provenance"), and they travel into the record.
type ParsedCommand struct {
	Lens     entities.CommandLens
	Criteria entities.CommandCriteria
	Notes    []string
	// Route is how the request must be answered: "filter" (the criteria are exact), "research" (the
	// vault has to be read and judged) or "ask" (a question). The parser sets it when the words say
	// it unambiguously, so the common case costs NO classification call.
	Route string
	// PendingRef names a reference the request points at but that is not available ("this claim"
	// with no claim open). It is a refusal with a reason, not a silent misreading.
	PendingRef string
}

// ErrUnparsed is what an utterance that names no lens and no criterion becomes. It is never turned
// into "the whole vault": an assistant that answers a request it did not understand with an
// unfiltered list is worse than one that says it did not understand.
func ErrUnparsed() error {
	return types.New("unparsed", "the request could not be read as a command", nil)
}

// The deterministic parser is the FALLBACK and the no-model path. It reads the shapes people
// actually type in English and German — a lens, a tag or topic, a kind, a time window, a derivation
// state — and it is deliberately narrow: anything it does not recognise is left in the free text,
// which is handed to the vault's lexical search rather than guessed at.
var (
	// A tag/topic is introduced by a marker; the name is one word, which is what a tag is.
	reTagged = regexp.MustCompile(`(?i)(?:^|\s)(?:tag(?:ged)?|thema|topic|label)\s+([\p{L}\p{N}_-]+)`)
	// The marker words are matched after a space, not with \b: Go's word boundary is ASCII-only, so
	// "\büber" never matches.
	reAbout = regexp.MustCompile(`(?i)(?:^|\s)(?:zu\s+dem|über|ueber|about|zu)\s+([\p{L}\p{N}_-]+)`)
)

// lensWords maps the words that name a lens. The order matters: a question beats a list, and a
// failure word alone is an Attention command, not a list of everything.
var lensWords = []struct {
	lens entities.CommandLens
	word []string
}{
	{entities.CommandAsk, []string{"frage", "question", "was sagt", "was sagen", "erklär", "explain", "why", "warum", "weshalb", "wie ", "how "}},
	{entities.CommandAttention, []string{"attention", "fehlgeschlagen", "failed", "fehler", "error", "problem"}},
	{entities.CommandClaims, []string{"claim", "claims", "aussage", "aussagen", "atom", "atoms", "behauptung"}},
	{entities.CommandWiki, []string{"wiki", "seite", "seiten", "page", "pages"}},
	{entities.CommandStatus, []string{"status", "bericht", "report", "queue", "warteschlange", "speicher", "memory"}},
	{entities.CommandSources, []string{"quelle", "quellen", "source", "sources", "dokument", "dokumente", "datei", "dateien", "buch", "bücher", "paper", "papers"}},
}

// lensWordSet is built once: a lens word is never free text. "Wiki Seiten über GitHub" searches
// for "github", not for "wiki" and "seiten" — every page is a wiki page, so those words would only
// water down the match.
var lensWordSet = func() map[string]bool {
	set := map[string]bool{}
	for _, candidate := range lensWords {
		for _, word := range candidate.word {
			for _, token := range strings.Fields(strings.ToLower(word)) {
				set[token] = true
			}
		}
	}
	return set
}()

// stopwords are the words a command is built from. They carry no search meaning of their own, so
// leaving them in the free text would make every search match everything.
var commandStopwords = map[string]bool{
	"zu": true, "über": true, "ueber": true, "about": true,
	"wir": true, "uns": true, "unser": true, "unsere": true, "unseren": true, "euch": true,
	"ihr": true, "ihre": true, "deren": true, "dessen": true, "we": true, "our": true, "us": true,
	"dieser": true, "diese": true, "diesem": true, "diesen": true,
	"dieses": true, "this": true, "these": true,
	"a": true, "added": true, "all": true, "alle": true, "allen": true, "alles": true,
	"alphabetisch": true, "als": true, "an": true, "and": true, "angehaengt": true, "angehängt": true,
	"are": true, "atom": true, "atoms": true, "attached": true, "auch": true, "auf": true,
	"aus": true, "bitte": true, "bookmarks": true, "claim": true, "claims": true, "complete": true,
	"das": true, "day": true, "days": true, "dazu": true, "dem": true, "den": true,
	"der": true, "des": true, "destilliert": true, "die": true, "distilled": true, "done": true,
	"ein": true, "eine": true, "einem": true, "einen": true, "einer": true, "failed": true,
	"fehlgeschlagen": true, "fertig": true, "find": true, "finde": true, "for": true, "from": true,
	"fuer": true, "für": true, "habe": true, "haben": true, "has": true, "hat": true,
	"have": true, "hinzugefuegt": true, "hinzugefügt": true, "hinzugekommen": true, "hour": true, "hours": true,
	"i": true, "ich": true, "im": true, "in": true, "is": true, "ist": true,
	"last": true, "lesezeichen": true, "letzte": true, "letzten": true, "letzter": true, "list": true,
	"liste": true, "markdown": true, "me": true, "mich": true, "mir": true, "mir ": true,
	"mit": true, "monat": true, "month": true, "my": true, "neu": true, "neue": true,
	"neuen": true, "neueste": true, "neuesten": true, "new": true, "newest": true, "nicht": true,
	"noch": true, "not": true, "nur": true, "oder": true, "of": true, "offen": true,
	"ohne": true, "oldest": true, "on": true, "open": true, "or": true, "past": true,
	"pdf": true, "please": true, "previous": true, "quelle": true, "quellen": true, "schon": true,
	"search": true, "show": true, "sind": true, "source": true, "sources": true, "still": true,
	"stunde": true, "stunden": true, "such": true, "suche": true, "tag": true, "tage": true,
	"tagen": true, "text": true, "the": true, "titel": true, "title": true, "to": true,
	"und": true, "url": true, "urls": true, "vom": true, "von": true, "war": true,
	"waren": true, "was": true, "week": true, "weeks": true, "welche": true, "welcher": true,
	"welches": true, "were": true, "which": true, "with": true, "without": true, "woche": true,
	"wochen": true, "yet": true, "zeig": true, "zeige": true, "zum": true, "zur": true,
	"älteste": true,
}

// CommandRefs is the surface a request was made from: the lens the reader was looking at and the id
// in the address. It is how "this claim" and "this source" have a meaning — and how an id pasted
// into the field is recognised as what it is.
type CommandRefs struct {
	View string
	ID   string
}

// SourceHash is the referenced source, when the reader stands on one.
func (r CommandRefs) SourceHash() string {
	if (r.View == "source" || r.View == "sources") && entities.IsSHA256Hex(r.ID) {
		return r.ID
	}
	return ""
}

// AtomID is the referenced claim, when the reader stands on one.
func (r CommandRefs) AtomID() string {
	if r.View != "claims" || r.ID == "" {
		return ""
	}
	if _, err := ulid.ParseStrict(r.ID); err == nil {
		return r.ID
	}
	return ""
}

// Explicit references in the utterance itself: a hash, or a claim id. They are stronger than a
// deictic word, because they cannot be misread.
var (
	reHash = regexp.MustCompile(`\b[0-9a-fA-F]{64}\b`)
	reULID = regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`)
)

// deicticSource and deicticAtom are how people point at what they are looking at.
var (
	deicticSource = []string{"diese quelle", "dieser quelle", "diesem dokument", "dieses dokument",
		"diesem paper", "dieses paper", "this source", "this document", "this paper"}
	// subjectMarkers say the request is about CONTENT, not about a field: it must be read.
	subjectMarkers = []string{"über", "ueber", "zum thema", "thema", "about", "dealing with",
		"deals with", "regarding", "handelt von", "handeln von", "beschäftigt", "concerned with",
		"which of my", "welche meiner"}
	deicticAtom = []string{"dieser claim", "diesen claim", "diesem claim", "dieses claim",
		"dieses claims", "diesen claims", "dieser aussage", "diese aussage", "dieser behauptung",
		"this claim", "this statement"}
)

// ParseCommand reads an utterance without a model.
func ParseCommand(text string, refs CommandRefs) (ParsedCommand, error) {
	raw := strings.Join(strings.Fields(text), " ")
	lower := strings.ToLower(raw)
	if raw == "" {
		return ParsedCommand{}, ErrUnparsed()
	}

	out := ParsedCommand{Lens: entities.CommandSources, Criteria: entities.CommandCriteria{
		Order: entities.CommandOrderNewest,
	}}
	recognised := false
	rest := raw

	// An id pasted into the field IS the reference. A hash means "this source", a claim id means
	// "this claim" — and the lens follows from what was named when the request names no other.
	if m := reHash.FindString(raw); m != "" {
		out.Criteria.Source = strings.ToLower(m)
		rest = strings.Replace(rest, m, " ", 1)
		recognised = true
		if !hasLensWord(lower) {
			out.Lens = entities.CommandSources
		}
	}
	if m := reULID.FindString(raw); m != "" {
		out.Criteria.Atom = m
		rest = strings.Replace(rest, m, " ", 1)
		recognised = true
		if !hasLensWord(lower) {
			out.Lens = entities.CommandClaims
		}
	}
	// "This claim" / "this source" names the OBJECT of the request, so the phrase leaves the text
	// even when there is nothing open — otherwise "the sources of this claim" would be read as a
	// claim list (the word "claim" would win the lens).
	if mentionsAtom := containsAny(lower, deicticAtom...); mentionsAtom {
		rest = stripPhrases(rest, deicticAtom)
		recognised = true
		if id := refs.AtomID(); id != "" {
			out.Criteria.Atom = id
		} else {
			out.PendingRef = "claim"
		}
	}
	if containsAny(lower, deicticSource...) {
		rest = stripPhrases(rest, deicticSource)
		recognised = true
		if hash := refs.SourceHash(); hash != "" {
			out.Criteria.Source = hash
		} else if out.PendingRef == "" {
			out.PendingRef = "source"
		}
	}

	// A question is answered, not filtered: Ask writes prose and cites what it used. This is tested
	// BEFORE the lens words, because "what do the sources say about X?" is a question even though it
	// names the source list.
	if isQuestion(lower) {
		out.Lens = entities.CommandAsk
		out.Criteria.Text = strings.TrimSpace(raw)
		return out, nil
	}

	// The lens is decided by what is LEFT after the reference: in "the sources of this claim" the
	// object is the sources, and "claim" only names the thing being pointed at.
	lensText := strings.ToLower(rest)

	// A lens word is the strongest signal for a list. The loop keeps its OWN flag: a reference found
	// above must not end the search for a lens word before the first candidate is even looked at.
	lensFound := false
	for _, candidate := range lensWords {
		for _, word := range candidate.word {
			if strings.Contains(lensText, word) {
				out.Lens = candidate.lens
				lensFound = true
				break
			}
		}
		if lensFound {
			break
		}
	}
	recognised = recognised || lensFound

	// Kind, bookmarks and attached snapshots. They are source dimensions; for claims they are only
	// free text, so they are read only when the lens is a source lens.
	if out.Lens == entities.CommandSources || out.Lens == entities.CommandAttention {
		switch {
		case containsAny(lower, "pdf", "pdfs"):
			out.Criteria.Kind = "pdf"
			recognised = true
		case containsAny(lower, "markdown", "md-datei"):
			out.Criteria.Kind = "md"
			recognised = true
		case containsAny(lower, "textdatei", "txt"):
			out.Criteria.Kind = "txt"
			recognised = true
		case containsAny(lower, "webseite", "website", "url", "links"):
			out.Criteria.Kind = "url"
			recognised = true
		}
		if containsAny(lower, "lesezeichen", "bookmark", "bookmarks") {
			out.Criteria.BookmarksOnly = true
			recognised = true
		}
		if containsAny(lower, "angehängt", "angehaengt", "attached") {
			out.Criteria.AttachedOnly = true
			recognised = true
		}
		if containsAny(lower, "nicht destilliert", "noch nicht destilliert", "not distilled",
			"noch offen", "still open", "pending") {
			out.Criteria.State = entities.CommandStateOpen
			recognised = true
		} else if containsAny(lower, "fehlgeschlagen", "failed", "fehler") {
			out.Criteria.State = entities.CommandStateFailed
			recognised = true
		} else if containsAny(lower, "fertig destilliert", "vollständig", "complete") {
			out.Criteria.State = entities.CommandStateDone
			recognised = true
		}
	}

	// A time window. It is the vault-arrival window, exactly like the query agent's: "added to the
	// vault", never "published".
	if d, ok := readWindow(lower); ok {
		out.Criteria.Since = d
		recognised = true
	}

	// Order.
	switch {
	case containsAny(lower, "älteste", "oldest", "zuerst"):
		out.Criteria.Order = entities.CommandOrderOldest
	case containsAny(lower, "alphabetisch", "nach titel", "by title"):
		out.Criteria.Order = entities.CommandOrderTitle
	}

	// A tag or a topic. For claims the marker names a flat topic; for sources it names a tag.
	if m := reTagged.FindStringSubmatch(rest); len(m) == 2 {
		name := strings.ToLower(m[1])
		// "Tag"/"label" is a LABEL; "Thema"/"topic" is the subject the claims are about. For a
		// source list the subject is a question about its claims, not a substring of its text.
		marker := strings.ToLower(strings.TrimSpace(m[0]))
		if strings.HasPrefix(marker, "tag") || strings.HasPrefix(marker, "label") {
			out.Criteria.Tag = name
		} else {
			out.Criteria.Topic = name
		}
		recognised = true
		rest = strings.Replace(rest, m[0], " ", 1)
	} else if m := reAbout.FindStringSubmatch(rest); len(m) == 2 && out.Lens == entities.CommandClaims {
		// "Claims über X" names a topic. For any other lens "über X" is prose: a wiki page or a
		// source is matched by its text, not by a claim's subject.
		out.Criteria.Topic = strings.ToLower(m[1])
		recognised = true
		rest = strings.Replace(rest, m[0], " ", 1)
	}

	if !recognised {
		return ParsedCommand{}, ErrUnparsed()
	}

	// A subject marker is a ROUTE decision, not a criterion: "sources about X" cannot be answered
	// by matching X, so it must be read. This is what spares the classification call — and the
	// reader a second round trip.
	if containsAny(lower, subjectMarkers...) && out.Lens != entities.CommandAsk {
		out.Route = "research"
	}

	// What is left is the free text. A question keeps its whole wording: the answer is written from
	// the question, not from a keyword soup.
	if out.Lens == entities.CommandAsk {
		out.Criteria.Text = strings.TrimSpace(rest)
		return out, nil
	}
	out.Criteria.Text = residualText(rest)

	// An enumerated filter — a tag, a kind, a step state, a bookmarks/attached flag, a reference —
	// with no free text is decidable here, exactly. It needs no model and must not wait for one.
	if out.Route == "" && out.Criteria.Text == "" &&
		(out.Criteria.Tag != "" || out.Criteria.Kind != "" || out.Criteria.State != entities.CommandStateAny ||
			out.Criteria.BookmarksOnly || out.Criteria.AttachedOnly ||
			out.Criteria.Source != "" || out.Criteria.Atom != "") {
		out.Route = "filter"
	}
	return out, nil
}

// isQuestion reports whether an utterance asks for prose rather than for a list. A question mark is
// the clearest signal; the opening word catches dictation, which often loses the punctuation.
func isQuestion(lower string) bool {
	if strings.Contains(lower, "?") {
		return true
	}
	for _, opener := range []string{
		"was ", "wie ", "warum", "weshalb", "wann", "wer ", "welche", "welcher", "welches",
		"erzähl", "erzaehl", "erklär", "erklaer", "fass ", "zusammenfass",
		"what ", "why", "how ", "when ", "who ", "which ", "explain", "summarise", "summarize", "tell me",
	} {
		if strings.HasPrefix(lower, opener) {
			return true
		}
	}
	return false
}

// readWindow turns the phrases people use into a duration. The set is small on purpose: a wrong
// window silently answers a different question.
func readWindow(lower string) (time.Duration, bool) {
	switch {
	case containsAny(lower, "heute", "today", "letzte stunde", "last hour"):
		return time.Hour, true
	case containsAny(lower, "letzte 24", "letzten 24", "last 24", "gestern", "yesterday"):
		return 24 * time.Hour, true
	case containsAny(lower, "letzte woche", "letzten woche", "letzten 7", "letzte 7", "last week",
		"past week", "letzten sieben", "letzte sieben", "diese woche", "this week"):
		return 7 * 24 * time.Hour, true
	case containsAny(lower, "letzten monat", "letzter monat", "last month", "letzte 30",
		"letzten 30", "last 30"):
		return 30 * 24 * time.Hour, true
	}
	// An explicit duration ("48h", "2d", "1w") is taken as written.
	for _, field := range strings.Fields(lower) {
		if d, ok := parseWindowDuration(strings.Trim(field, ".,;:")); ok && d > 0 {
			return d, true
		}
	}
	return 0, false
}

// residualText is what the parser did not consume: the words a lexical search should actually see.
func residualText(text string) string {
	// A quoted phrase is free text on purpose.
	if i := strings.IndexByte(text, '"'); i >= 0 {
		if j := strings.IndexByte(text[i+1:], '"'); j >= 0 {
			return strings.TrimSpace(text[i+1 : i+1+j])
		}
	}
	words := strings.Fields(text)
	kept := make([]string, 0, len(words))
	for _, w := range words {
		clean := strings.Trim(w, ".,;:!?()[]{}'\"")
		if clean == "" {
			continue
		}
		lower := strings.ToLower(clean)
		if commandStopwords[lower] || lensWordSet[lower] {
			continue
		}
		kept = append(kept, clean)
	}
	return strings.Join(kept, " ")
}

// hasLensWord reports whether the utterance names a lens; an id alone does not.
func hasLensWord(lower string) bool {
	for _, candidate := range lensWords {
		for _, word := range candidate.word {
			if strings.Contains(lower, word) {
				return true
			}
		}
	}
	return false
}

// stripPhrases removes the pointing words from the leftover text, so "this claim" never becomes a
// search term.
func stripPhrases(text string, phrases []string) string {
	out := text
	// Longest first: "dieses claims" must go before "dieses claim", or the leftover "s" becomes a
	// search term.
	sorted := append([]string(nil), phrases...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, p := range sorted {
		for {
			idx := strings.Index(strings.ToLower(out), p)
			if idx < 0 {
				break
			}
			out = out[:idx] + " " + out[idx+len(p):]
		}
	}
	return out
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
