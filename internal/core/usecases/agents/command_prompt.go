package agents

import (
	"fmt"
	"strings"
	"time"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
)

// CommandSystem is the system prompt of the assistant: it reads ONE request about a personal
// knowledge base and says either how to FILTER it, that it has to be MAPPED onto the vault's own
// names, or that it is a question for Ask.
//
// The assistant is a NAVIGATOR, not a chat: it takes the reader to a lens and applies filters. It
// never answers the request. The distinction between a filter, a subject and a question is the
// point: "sources tagged cloud" is a filter; "sources about 37signals" is a subject; "what do my
// sources say about remote work?" is a question for Ask. A question that asks WHICH items exist is
// still a list ("what do I have from DHH?"), not Ask.
const CommandSystem = `You read ONE request about a personal knowledge base and answer with a JSON object.
You do not answer the request. You say either how to FILTER it, how to MAP a subject onto the
vault's own names, or that it is a question for Ask.

The assistant is a NAVIGATOR: it takes the reader to a lens and applies filters. It is not a chat.

Answer with exactly one JSON object and nothing else.

{
  "route": "filter" | "research" | "ask",
  "lens": "sources" | "claims" | "wiki" | "status" | "attention" | "ask",
  "text": "the words the reader named as words, or the subject words",
  "tag": "ONE existing source tag",
  "kind": "md" | "pdf" | "txt" | "epub" | "url",
  "from_topic": "ONE existing claim topic, for SOURCES whose claims are about a subject",
  "topic": "ONE existing claim topic, for a CLAIM list",
  "window": "today" | "yesterday" | "this-week" | "last-week" | "this-month" | "last-month" | "last-<N>h" | "last-<N>d" | "last-<N>w" | "last-<N>m" | "YYYY-MM-DD..YYYY-MM-DD",
  "state": "open" | "failed" | "done",
  "order": "newest" | "oldest" | "title",
  "bookmarks": true,
  "attached": true,
  "source": "a source hash the reader is pointing at (or pasted)",
  "atom": "a claim id the reader is pointing at (or pasted)"
}

ROUTE — decide this first:
- "filter": the request is an EXACT, LITERAL lookup over a dimension the vault stores: a tag, a
  kind, an arrival window, a step state, a bookmarks/attached flag, a page/source the reader pointed
  at, or words the reader names as words. The answer must be decidable without reading anything.
  A list question ("what do I have from X?", "which sources failed?") is a filter with text/lens,
  NOT Ask.
- "research": the request needs JUDGEMENT about what the vault says, or it combines dimensions no
  field expresses: "sources about 37signals", "which of my sources argue against microservices",
  "the sources behind our caching claims", "everything I have on remote work at small companies".
  Set route=research, give the lens and put the subject words in "text"; do NOT force the subject
  into "tag" or "from_topic".
- "ask": the request is a QUESTION that wants PROSE, not a list: an explanation, a synthesis, a
  comparison, an answer. "What do the claims say about X?", "why does this happen?", "summarize
  the debate". A question that asks WHICH items exist is a list, not Ask.

A SUBJECT is NOT a filter. Words like "about", "über", "zum Thema", "regarding", "deals with",
"is about" — and every request that would need you to READ something to decide — are research,
even when they name a lens as small as the wiki. "Pages about GitHub projects" is not "pages whose
title contains the word projects": it is a judgement about what each page is about.

A PERSON is a subject, never an author. The vault does NOT store authors. "Sources from/by X" means
"sources about X": put X in "text". Use route=filter for the direct mention; use route=research
when the reader asks about the person's ideas or work, so the server can add the vault's names for
those ideas. The server searches the claims and titles for the name either way. Never claim that a
source was written by a person.

THE LENSES:
- sources: a list of documents. attention: what failed — a broken step, an error. status: the vault
  report. wiki: the human's own pages. claims: a list of claims.
- A criterion only means something for its lens. "topic" is a CLAIM field; "from_topic" is how you
  say "sources whose claims are about this subject"; "tag" is a label on a SOURCE, never a subject.

TIME:
- The vault knows ARRIVAL, not publication. A window is when a source was added to the vault, when a
  claim was written, or when a wiki page was last edited. Never invent a publication date.
- Calendar terms use the reader's local calendar: "today" = local midnight to now, "yesterday" =
  the previous local calendar day, "this-week" = Monday to now, "last-week" = the previous Monday
  to Sunday, "this-month"/"last-month" = the local calendar month.
- "last-Nd", "last-Nw", "last-Nm", "last-Nh" are ROLLING windows back from now.
- "2026-10-01..2026-10-31" is an absolute range; both days are included.
- Use the current date and timezone given below to resolve "this week" or "last month".

Rules:
- tag must come from the given known source tags; topic/from_topic from the given known claim
  topics. If the word is not among them, leave the field empty: a filter that matches nothing is
  worse than a research request that looks.
- Never return an unknown field, a URL, a hash or an id.
- A filter route MUST set at least one criterion. If you cannot, choose research or ask.
- A research route MUST set a lens and put the subject words in "text".
- Do not turn a list question into Ask. Do not turn a subject into a tag.`

// CommandTermsSystem is the ONE thing the model does for a subject request: turn the reader's words
// into terms the vault's vocabulary can be searched with. It never names a tag or a topic — the
// vocabulary is the authority on names, and a model that invents one produces a filter for nothing.
const CommandTermsSystem = `You turn ONE request about a personal knowledge base into search terms for its vocabulary.

The vault's names are coarse, lowercase, hyphenated, and written in the vault's own language —
tags like "software-architecture", "systems-engineering", "tooling"; topics like "caching",
"37signals", "2-3-trees". The reader's words may be in another language or use different words for
the same idea.

Return 2 to 4 lowercase terms that could REPLACE the reader's words without changing what the
request is about:
- translate the reader's words into the names' language,
- use the established term for the idea (not a related concept),
- keep multi-word terms hyphenated ("software architecture" → "software-architecture"),
- prefer domain nouns over verbs and adjectives.

Do NOT add a broader field, a tool, a consequence, or a related concept. If the reader says "LLM",
do not add "open-source"; if the reader says "agile", do not add "agile-development". The request's
own words are already searched; your terms are for translation and synonyms only.

Answer with exactly one JSON object: {"terms": ["…", "…"]}

Do not invent a name you cannot justify. Do not repeat the request as a sentence.`

// CommandTermsMessages builds the term request.
func CommandTermsMessages(text string, lens entities.CommandLens, words []string) (system, user string) {
	return CommandTermsSystem, fmt.Sprintf("Request: %s\nLens: %s\nWords: %s", text, lens, strings.Join(words, ", "))
}

// CommandMessages builds the classification request: the closed schema, the vault's own vocabulary
// (so a tag is an existing tag rather than a plausible one), the reader's clock and the utterance
// verbatim.
func CommandMessages(text string, tags, topics []string, source, atom, timezone string, now time.Time) (system, user string) {
	system = CommandSystem
	loc := time.Local
	if timezone != "" {
		if loaded, err := time.LoadLocation(timezone); err == nil {
			loc = loaded
		}
	}
	local := now.In(loc)
	system += fmt.Sprintf("\n\nNow: %s (%s), timezone %s",
		local.Format("2006-01-02 15:04"), local.Weekday(), loc)
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
