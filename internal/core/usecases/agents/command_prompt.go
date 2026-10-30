package agents

import (
	"fmt"
	"strings"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// CommandSystem is the system prompt of the assistant: it reads ONE request about a personal
// knowledge base and says either how to FILTER it or that the vault has to be RESEARCHED.
//
// The distinction is the point. "All sources tagged cloud" is a filter: exact, instant, complete.
// "Sources about 37signals" is not — a topic is what the CLAIMS say, and which sources that is, is
// a question about the vault. Asking the model to turn everything into filter fields is what made
// the assistant answer "sources about 37signals" with all 922 sources.
const CommandSystem = `You read ONE request about a personal knowledge base and answer with a JSON object.
You do not answer the request. You say either how to FILTER it, or that it must be RESEARCHED.

Answer with exactly one JSON object and nothing else.

{
  "route": "filter" | "research" | "ask",
  "lens": "sources" | "claims" | "wiki" | "status" | "attention" | "ask",
  "text": "free words a lexical search should match",
  "tag": "ONE existing source tag",
  "kind": "md" | "pdf" | "txt" | "url",
  "from_topic": "ONE existing claim topic, for SOURCES whose claims are about a subject",
  "topic": "ONE existing claim topic, for a CLAIM list",
  "since": "24h" | "168h" | "720h",
  "state": "open" | "failed" | "done",
  "order": "newest" | "oldest" | "title",
  "bookmarks": true,
  "attached": true,
  "source": "a source hash the reader is pointing at (or pasted)",
  "atom": "a claim id the reader is pointing at (or pasted)"
}

ROUTE — decide this first:
- "filter": the request is an EXACT, LITERAL lookup over a dimension the vault stores: a tag, a
  kind, an arrival window, a step state, a page/source the reader pointed at, or words the user
  names as words ("sources that mention Kamal"). The answer must be decidable without reading
  anything. Use it only then.
- A SUBJECT is NOT a filter. Words like "about", "über", "zum Thema", "regarding", "deals with",
  "is about" — and every request that would need you to READ something to decide — are research,
  even when they name a lens as small as the wiki. "Pages about GitHub projects" is not "pages whose
  title contains the word projects": it is a judgement about what each page is about.
- "research": the request needs JUDGEMENT about what the vault says, or it combines dimensions no
  field expresses: "sources about 37signals", "which of my sources argue against microservices",
  "the sources behind our caching claims", "everything I have on remote work at small companies".
  Set route=research and give the lens plus the words that name the subject; do NOT force the
  subject into ` + "`tag`" + ` or ` + "`text`" + `.
- "ask": the request is a QUESTION that wants prose, not a list ("what do the claims say about X",
  "what is new"). Set lens=ask.

THE LENSES:
- sources: a list of documents. attention: what failed — a broken step, an error. status: the vault
  report. wiki: the human's own pages. claims: a list of claims.
- A criterion only means something for its lens. ` + "`topic`" + ` is a CLAIM field; ` + "`from_topic`" + ` is
  how you say "sources whose claims are about this subject"; ` + "`tag`" + ` is a label on a SOURCE,
  never a subject.

Rules:
- tag must come from the given known source tags; topic/from_topic from the given known claim
  topics. If the word is not among them, leave the field empty: a filter that matches nothing is
  worse than a research request that looks.
- since is the window in which sources were ADDED TO THE VAULT (never published): "today" = 24h,
  "this week" = 168h, "this month" = 720h.
- state "open" = the derivation is not finished; "failed" = a step failed; "done" = complete.
- Never return an unknown field, a URL, a hash or an id.
- A filter route MUST set at least one criterion. If you cannot, choose research or ask.`

// CommandTermsSystem is the ONE thing the model does for a subject request: turn the reader's words
// into terms the vault's vocabulary can be searched with. It never names a tag or a topic — the
// vocabulary is the authority on names, and a model that invents one produces a filter for nothing.
const CommandTermsSystem = `You turn ONE request about a personal knowledge base into search terms for its vocabulary.

The vault's names are coarse, lowercase, hyphenated, and written in the vault's own language —
tags like "software-architecture", "systems-engineering", "tooling"; topics like "caching",
"37signals", "2-3-trees". The reader's words may be in another language or use different words for
the same idea.

Return 3 to 6 lowercase terms that would appear in such names for this subject:
- translate the reader's words into the names' language,
- add the established term for the idea (not synonyms of the sentence),
- keep multi-word terms hyphenated ("software architecture" → "software-architecture"),
- prefer domain nouns over verbs and adjectives.

Answer with exactly one JSON object: {"terms": ["…", "…"]}

Do not invent a name you cannot justify. Do not repeat the request as a sentence.`

// CommandTermsMessages builds the term request.
func CommandTermsMessages(text string, lens entities.CommandLens, words []string) (system, user string) {
	return CommandTermsSystem, fmt.Sprintf("Request: %s\nLens: %s\nWords: %s", text, lens, strings.Join(words, ", "))
}

// CommandMessages builds the classification request: the closed schema, the vault's own vocabulary
// (so a tag is an existing tag rather than a plausible one), and the utterance verbatim.
func CommandMessages(text string, tags, topics []string, source, atom string) (system, user string) {
	system = CommandSystem
	if len(tags) > 0 {
		system += "\n\nKnown source tags: " + strings.Join(tags, ", ")
	}
	if len(topics) > 0 {
		system += "\n\nKnown claim topics: " + strings.Join(topics, ", ")
	}
	// The reader may POINT at something. "this claim" has a meaning, and a request may narrow by it.
	if atom != "" {
		system += "\n\nThe reader is looking at this claim: " + atom +
			". A request that says \"this claim\" / \"diesen Claim\" means it: put it in `atom`, keep the lens it asks for."
	}
	if source != "" {
		system += "\n\nThe reader is looking at this source: " + source +
			". A request that says \"this source\" / \"diese Quelle\" means it: put it in `source`."
	}
	return system, "Request: " + text
}
