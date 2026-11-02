package usecases

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

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
	// A list question is still a LIST. "Was haben wir von DHH?" asks which sources exist, not for
	// prose; the same is true of "what do I have about X?". Ask is for an explanation, never for
	// "which items do I have?".
	reHaveFrom  = regexp.MustCompile(`(?i)^\s*(?:was\s+(?:haben|habe)\s+(?:wir|ich)\s+von|was\s+gibt\s+es\s+von|what\s+do\s+(?:we|i)\s+have\s+(?:from|by))\s+(.+)$`)
	reHaveAbout = regexp.MustCompile(`(?i)^\s*(?:was\s+(?:haben|habe)\s+(?:wir|ich)\s+über|was\s+gibt\s+es\s+über|what\s+do\s+(?:we|i)\s+have\s+about)\s+(.+)$`)
	// Time phrases. The order matters: "seit gestern" before "gestern", the numbered window before
	// the bare "letzte Woche" (which has no number and must stay a calendar week).
	reWindowSinceYesterday = regexp.MustCompile(`(?i)\b(?:seit\s+gestern|since\s+yesterday)\b`)
	reWindowToday          = regexp.MustCompile(`(?i)\b(?:heute|today)\b`)
	reWindowYesterday      = regexp.MustCompile(`(?i)\b(?:gestern|yesterday)\b`)
	reWindowThisWeek       = regexp.MustCompile(`(?i)\b(?:diese\s+woche|this\s+week)\b`)
	reWindowLastWeek       = regexp.MustCompile(`(?i)\b(?:letzte\s+woche|last\s+week)\b`)
	reWindowThisMonth      = regexp.MustCompile(`(?i)\b(?:diesen\s+monat|dieser\s+monat|this\s+month)\b`)
	reWindowLastMonth      = regexp.MustCompile(`(?i)\b(?:letzten\s+monat|letzter\s+monat|last\s+month)\b`)
	reWindowLastHour       = regexp.MustCompile(`(?i)\b(?:letzte\s+stunde|last\s+hour)\b`)
	reWindowPastWeek       = regexp.MustCompile(`(?i)\b(?:past\s+week|vergangene\s+woche)\b`)
	reWindowPastMonth      = regexp.MustCompile(`(?i)\b(?:past\s+month|vergangener\s+monat)\b`)
	reWindowLast24         = regexp.MustCompile(`(?i)\b(?:letzte[nr]?\s+24|last\s+24)\b`)
	reWindowLastN          = regexp.MustCompile(`(?i)\b(?:letzte[nr]?\s+|last\s+)(\d+)\s*(stunden?|hours?|tage?n?|days?|wochen?|weeks?|monate?n?|months?)\b`)
	reWindowDuration       = regexp.MustCompile(`(?i)\b(\d+)(h|d|w)\b`)
)

// lensWords maps the words that name a lens. The order matters: a question beats a list, and a
// failure word alone is an Attention command, not a list of everything.
var lensWords = []struct {
	lens entities.CommandLens
	word []string
}{
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
			for token := range strings.FieldsSeq(strings.ToLower(word)) {
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
	"aus": true, "bitte": true, "bookmarks": true, "by": true, "claim": true, "claims": true, "complete": true,
	"das": true, "day": true, "days": true, "dazu": true, "dem": true, "den": true,
	"der": true, "des": true, "destilliert": true, "did": true, "die": true, "distilled": true,
	"do": true, "does": true, "done": true,
	"ein": true, "eine": true, "einem": true, "einen": true, "einer": true, "failed": true,
	"fehlgeschlagen": true, "fertig": true, "find": true, "finde": true, "for": true, "frage": true,
	"from": true, "question": true,
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
	"what":    true,
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
	// Timezone is the reader's IANA timezone (the PWA sends it). It decides what "yesterday" and
	// "this week" mean; an empty value falls back to the server's local zone.
	Timezone string
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
	deicticSource = []string{
		"diese quelle", "dieser quelle", "diesem dokument", "dieses dokument",
		"diesem paper", "dieses paper", "this source", "this document", "this paper",
	}
	// subjectMarkers say the request is about CONTENT, not about a field: it must be read.
	subjectMarkers = []string{
		"über", "ueber", "zum thema", "thema", "about", "dealing with",
		"deals with", "regarding", "handelt von", "handeln von", "beschäftigt", "concerned with",
		"which of my", "welche meiner",
	}
	deicticAtom = []string{
		"dieser claim", "diesen claim", "diesem claim", "dieses claim",
		"dieses claims", "diesen claims", "dieser aussage", "diese aussage", "dieser behauptung",
		"this claim", "this statement",
	}
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

	// Ask is for PROSE. A question that asks which items exist is still a list: "Was haben wir von
	// DHH?" is a Sources filter, not an answer. The list-question shapes are read deterministically
	// so the no-model path is as good as the model path.
	if isAsk(lower) {
		out.Lens = entities.CommandAsk
		out.Criteria.Text = strings.TrimSpace(raw)
		out.Route = "ask"
		return out, nil
	}
	if m := reHaveFrom.FindStringSubmatch(rest); len(m) == 2 {
		out.Lens = entities.CommandSources
		rest = strings.TrimSpace(m[1])
		recognised = true
	} else if m := reHaveAbout.FindStringSubmatch(rest); len(m) == 2 {
		out.Lens = entities.CommandSources
		rest = strings.TrimSpace(m[1])
		recognised = true
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
		case containsAny(lower, "epub", "epubs", "ebook", "ebooks"):
			out.Criteria.Kind = "epub"
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

	// A time window. It is the vault's own timestamp — arrival for a source, creation for a claim,
	// the last edit for a wiki page — never publication. The parser only records the SPEC; the run
	// resolves it against the reader's clock and timezone.
	if spec, next, ok := readWindowSpec(rest); ok {
		out.Criteria.Window.Spec = spec
		rest = next
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
		out.Route = "ask"
		return out, nil
	}
	out.Criteria.Text = residualText(rest)

	// An enumerated filter — a tag, a kind, a step state, a bookmarks/attached flag, a reference,
	// a time window — with no free text is decidable here, exactly. It needs no model and must not
	// wait for one.
	if out.Route == "" && out.Criteria.Text == "" &&
		(out.Criteria.Tag != "" || out.Criteria.Kind != "" || out.Criteria.State != entities.CommandStateAny ||
			out.Criteria.BookmarksOnly || out.Criteria.AttachedOnly ||
			out.Criteria.Source != "" || out.Criteria.Atom != "" ||
			!out.Criteria.Window.Empty()) {
		out.Route = "filter"
	}
	// Words the reader named as words are a FILTER, not a subject: "Quellen von DHH" matches the
	// words, it does not map a subject. A subject marker ("about", "über", "zum Thema") still wins
	// and sends the request to the mapped route.
	if out.Route == "" && out.Criteria.Text != "" && out.Lens != entities.CommandAsk &&
		!containsAny(lower, subjectMarkers...) {
		out.Route = "filter"
	}
	return out, nil
}

// isAsk reports whether the reader wants PROSE. A question mark alone is not enough: "Was haben wir
// von DHH?" is a list request. The markers are checked at the START of the utterance: "why" or
// "warum" inside a title ("Quellen über Why We Sleep") is part of the subject, not a question.
func isAsk(lower string) bool {
	trimmed := strings.TrimSpace(lower)
	for _, marker := range []string{
		"frage", "question",
		"erklär", "erklaer", "erzähl", "erzaehl", "fass zusammen", "zusammenfass",
		"warum", "weshalb", "wieso", "was sagt", "was sagen", "was wissen wir",
		"was sind die claims", "what are the claims",
		"wie hängt", "wie haengt", "wie funktioniert", "vergleich",
		"kannst du", "könntest du", "can you", "could you",
		"explain", "tell me about", "summarize", "summarise", "why", "how does", "how do",
		"what do the sources say", "what do the claims say",
	} {
		if strings.HasPrefix(trimmed, marker) {
			return true
		}
	}
	return false
}

// readWindowSpec turns the phrases people use into a window SPEC and removes the phrase from the
// text. The spec is resolved later against the reader's clock and timezone; the parser must stay
// pure. The set is small on purpose: a wrong window silently answers a different question.
func readWindowSpec(text string) (spec, rest string, ok bool) {
	rest = text
	// "letzte 2 Tage" / "last 3 weeks" first: it carries a number and must not be read as the bare
	// "letzte Woche" (which is a calendar week, not a rolling one).
	if m := reWindowLastN.FindStringSubmatch(rest); m != nil {
		n, parseErr := strconv.Atoi(m[1])
		unit := windowUnit(m[2])
		if parseErr == nil && n > 0 && unit != 0 {
			return fmt.Sprintf("last-%d%c", n, unit), strings.TrimSpace(reWindowLastN.ReplaceAllString(rest, " ")), true
		}
	}
	for _, phrase := range []struct {
		re   *regexp.Regexp
		spec string
	}{
		{reWindowSinceYesterday, "since-yesterday"},
		{reWindowToday, "today"},
		{reWindowYesterday, "yesterday"},
		{reWindowThisWeek, "this-week"},
		{reWindowLastWeek, "last-week"},
		{reWindowThisMonth, "this-month"},
		{reWindowLastMonth, "last-month"},
		{reWindowLastHour, "last-1h"},
		{reWindowPastWeek, "last-7d"},
		{reWindowPastMonth, "last-30d"},
		{reWindowLast24, "last-24h"},
	} {
		if phrase.re.MatchString(rest) {
			return phrase.spec, strings.TrimSpace(phrase.re.ReplaceAllString(rest, " ")), true
		}
	}
	// An explicit duration ("48h", "2d", "1w") is taken as written.
	if m := reWindowDuration.FindStringSubmatch(rest); m != nil {
		n, parseErr := strconv.Atoi(m[1])
		if parseErr == nil && n > 0 {
			return fmt.Sprintf("last-%d%s", n, strings.ToLower(m[2])), strings.TrimSpace(reWindowDuration.ReplaceAllString(rest, " ")), true
		}
	}
	return "", text, false
}

// windowUnit maps the words a reader uses to the single-letter unit of a window spec.
func windowUnit(raw string) byte {
	switch strings.ToLower(raw) {
	case "stunde", "stunden", "hour", "hours":
		return 'h'
	case "tag", "tage", "tagen", "day", "days":
		return 'd'
	case "woche", "wochen", "week", "weeks":
		return 'w'
	case "monat", "monate", "monaten", "month", "months":
		return 'm'
	}
	return 0
}

// commandTimeWords are the words a time phrase is built from. They are not search terms: a source
// titled "Yesterday" is found by quoting it, not by leaving the window's glue in the residual text.
var commandTimeWords = map[string]bool{
	"today": true, "yesterday": true, "heute": true, "gestern": true,
	"this": true, "diese": true, "diesen": true, "dieser": true,
	"seit": true, "since": true, "past": true, "vergangene": true, "vergangener": true,
	"hour": true, "hours": true, "stunde": true, "stunden": true,
	"day": true, "days": true, "tag": true, "tage": true, "tagen": true,
	"week": true, "weeks": true, "woche": true, "wochen": true,
	"month": true, "months": true, "monat": true, "monate": true, "monaten": true,
	"year": true, "years": true, "jahr": true, "jahre": true,
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
		if commandStopwords[lower] || commandTimeWords[lower] || lensWordSet[lower] {
			continue
		}
		// A misspelled lens word is sentence glue, not a search term: "show me sourecs tagged
		// cloud" must still be a filter for the tag, not a search for "sourecs".
		if isLensWordTypo(lower) {
			continue
		}
		kept = append(kept, clean)
	}
	return strings.Join(kept, " ")
}

// isLensWordTypo reports whether a word is a close misspelling of a lens word. It is deliberately
// narrow: one edit, or one adjacent transposition. A real word that merely resembles a lens word
// ("resource") stays a search term.
func isLensWordTypo(word string) bool {
	if len(word) < 5 {
		return false
	}
	for candidate := range lensWordSet {
		if len(candidate) < 5 {
			continue
		}
		if levenshtein(word, candidate) <= 1 || isAdjacentTransposition(word, candidate) {
			return true
		}
	}
	return false
}

// isAdjacentTransposition reports whether a and b are the same word with one pair of neighbouring
// letters swapped — the "sourecs"/"sources" typo that plain edit distance calls two edits.
func isAdjacentTransposition(a, b string) bool {
	if len(a) != len(b) || a == b {
		return false
	}
	ra, rb := []rune(a), []rune(b)
	first := -1
	for i := range ra {
		if ra[i] != rb[i] {
			first = i
			break
		}
	}
	if first < 0 || first+1 >= len(ra) {
		return false
	}
	if ra[first] != rb[first+1] || ra[first+1] != rb[first] {
		return false
	}
	return string(ra[first+2:]) == string(rb[first+2:])
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
