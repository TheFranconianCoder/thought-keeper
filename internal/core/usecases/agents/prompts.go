package agents

import (
	"fmt"
	"strings"
	"time"
)

// DistillSystem is the system prompt for the distill step.
//
// It carries the RULES only. The answer shape travels with the user message (DistillAnswerShape),
// because repeating it here cost 500 of 4,200 characters in every one of the 5,956 requests a single
// morning batch made — and the rules themselves repeated "stand alone" three times.
const DistillSystem = `You convert ONE part of a source document into atomic claims.

- One atom = exactly one self-contained claim (a fact, definition, example, counterpoint or quote).
  A sentence that bundles several facts is SEVERAL atoms: split it. A node of a few hundred
  characters usually carries more than one claim.
- Write the topic, claim and body in ENGLISH, regardless of the source's language. Keep proper
  names, titles, code and quoted terms in their original script. One claim is one sentence.
- THE ATOM MUST STAND ALONE: a reader who never saw the source must understand it. Never write
  "Chapter 11", "this section", "the book", "the author", "as we saw", "page 42" or any other
  pointer into the document. Restate the context a claim needs — system, version, condition,
  consequence — inside the claim and body.
- Do NOT extract statements ABOUT the document: what a chapter covers, its reading order, who wrote
  it, its table of contents. Page furniture is not a claim either: a running head, a page number, a
  copyright line, a link list. A part that carries nothing but furniture gets no atom.
- Give each atom a short, flat, ENGLISH topic: the subject of the claim, derived from the claim and
  body alone — never from the title, a chapter, an appendix or the document structure. No slash, no
  hierarchy. A list of names already in use comes with the text: reuse one whenever it fits, and
  invent a name only when none does.
- Cite provenance: pass the source node id(s) the claim came from as "nodes". Only cite ids that
  appear in the outline. Never invent node ids or facts that are not in the source.
- The body expands the claim in 1-3 sentences and does not repeat it verbatim.
- A claim has NO links: its associations are its clusters, not its text. Quote a declaration,
  expression, literal or grammar fragment as inline code (a backtick span), and name in the claim
  what the code is.
- You do NOT write anything. There is no write tool: you ANSWER with the claims as JSON, and the
  application records them.`

// DistillAnswerShape is the ONE answer the distill asks for. The model does not call a write tool: a
// tool call is a full round-trip per claim (measured on a real book: 70 claims, 52 iterations, 116k
// tokens), and a model that decides not to write can only answer in prose — which is the "wrote no
// claim at all" failure. Reading stays a tool; writing is data.
const DistillAnswerShape = `Answer with ONLY this JSON object, no prose and no code fence:
{"claims":[{"topic":"subject-of-the-claim","claim":"one self-contained sentence","body":"1-3 self-contained sentences",
 "type":"fact|definition|claim|example|counterpoint|quote",
 "nodes":["the node id you were given (optional, defaults to it)"]}]}
Name every claim the text carries, one object each. An empty list is only correct when the part
carries no claim at all — page furniture is not a claim.`

// TopicCandidatesBlock renders the names the part may reuse. The list is SELECTED (IDF-weighted from
// the part's own text) instead of a catalog dump: a vault with 57,181 topics cannot hand its
// vocabulary to the model at all — the old list_topics tool answered 2.3 MB and was cut to 6,000
// bytes mid-JSON, which cost a round trip per part and reused nothing.
func TopicCandidatesBlock(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Topics already in use that fit this text: ")
	b.WriteString(strings.Join(candidates, ", "))
	b.WriteString("\nReuse one when it names the claim's subject; invent a name only when none does.\n\n")
	return b.String()
}

// DistillPartUser describes ONE text-bearing node of a source. The work item is exactly that node
// and its complete text — nothing else. Completeness is a property of a single node, so a small
// work item is what makes it checkable: no plan to infer, no percentage to guess, and no answer
// has to swallow a whole document (which is what used to hit the output limit half way).
func DistillPartUser(title, hash, nodeID, nodeTitle, text string, candidates []string) string {
	return fmt.Sprintf(`Source hash (provenance only): %s
Source title (provenance only — never use it in a topic): %s

This is ONE part of that source: node %s "%s".
Its complete text is below. Turn every statement in it into an atom.
The node title is provenance only: do not put it, the chapter number or the document structure into
an atom. Every claim, body and topic must make sense without this source.

%sText:
%s

Give one claim per statement, citing node %s. Do not summarise the part into one claim, and do
not leave a statement out.

`+DistillAnswerShape, hash, title, nodeID, nodeTitle, TopicCandidatesBlock(candidates), text, nodeID)
}

// DistillPartAudit is the completeness check. The model reads the part's text against the claims
// that now exist for it and writes whatever no claim covers — so "complete" is verified against
// the text instead of being inferred from a count of cited nodes.
func DistillPartAudit(nodeID, nodeTitle, text, claims string, candidates []string) string {
	return fmt.Sprintf(`Check ONE part of a source for completeness: node %s "%s".

Its text:
%s

The claims that exist for this part so far:
%s

Read the text again, statement by statement, and compare it with those claims. ANSWER with the
claims that no existing claim covers yet (one object per statement, citing node %s). Do not
repeat or rewrite a claim that already exists, and do not invent facts.
If every statement of the text is covered, answer exactly: {"complete":true,"claims":[]}

%s`+DistillAnswerShape, nodeID, nodeTitle, text, claims, nodeID, TopicCandidatesBlock(candidates))
}

// DistillPartTruncated picks a cut-off answer up where it stopped. The model did not choose to end
// there — the endpoint's output limit did — so the statements it never reached are simply missing.
func DistillPartTruncated(nodeID, nodeTitle, claims string, candidates []string) string {
	return fmt.Sprintf(`Your previous answer was CUT OFF by the output limit — it did not end because the work was
finished. Continue with node %s "%s" and write the statements that are still missing.

The claims that exist for this part so far:
%s
One claim per statement, citing node %s. Do not repeat what is there.

%s`+DistillAnswerShape, nodeID, nodeTitle, claims, nodeID, TopicCandidatesBlock(candidates))
}

// QuerySystem is the system prompt for the query operation.
const QuerySystem = `You answer questions from a personal knowledge base of atomic CLAIMS and their
SOURCES. You are a careful researcher. Cluster prose does not exist: use only what you read through
the tools.

Classify the question first; the two branches do not mix:

A. A SUBJECT question ("what do the claims say about caching?"): ONE search with the question's key
   words. The top hits carry the claim and its body. The search already expands inflections and word
   families; if the vault's sources use another language than the question, search the key terms in
   that language too. If that is not enough, read at most THREE claims (read_atom). A second search
   only if the first found nothing.

B. A RECENCY or DIGEST question ("what is new?", "summarise the sources of the last 24 hours",
   "this week", "the newest sources"): call list_sources FIRST with the window the question names
   (since: 24h, 7d, 168h or an RFC3339 timestamp). Do not search claims first. The list carries each
   source's title, arrival time, tags, claim count, derivation state and a short excerpt of its
   overview. Open at most THREE full overviews (read_source_summary) when an excerpt is not enough.
   - The window is when a source ENTERED THE VAULT (imported_at), not when it was published. Write
     "added to the vault", never "published".
   - A source without an overview is still being processed: say so, or read its claims with
     list_atoms {source:<hash>}. Never invent its content.
   - An empty window is stated plainly. Do not silently widen it.
   - The list is a PAGE. If next_offset is greater than 0, call list_sources again with that
     offset and the same since, until next_offset is 0. Cover the whole window before you
     answer; do not stop at the first page.
   - If the window is so large that the tool budget cannot cover it, group the sources you read by
     theme, name the total, and say that the digest covers the newest ones. Never imply the list is
     complete.
   - For a digest, give each source one or two sentences: what it is about and what it adds. Cite
     each source as [[source:<hash>|<source title>]] — the hash comes from list_sources. Never
     write a bare [[<source title>]]; the UI turns that into a claim finder query, not a citation.
     Group sources that share a theme instead of listing them mechanically.

Then answer. Do not keep looking once what you have can answer the question.

The claim set is curated: it does not need to be surveyed. list_topics and list_atoms are for
orientation when a search comes back empty, not for a tour. Every extra round costs time.

Rules:
- Never invent facts. Use only what you actually read.
- Write complete, grammatical sentences. A citation is a REFERENCE after the statement it supports;
  it is never a word or phrase of that sentence, and never a substitute for a missing object.
  Bad: "Rust is a language for [[claim:<id>|<short claim text>]]."
  Good: "Rust prevents data races in safe code ([[claim:<id>|<short claim text>]])."
- The short claim text inside a citation is a label, not prose. Do not rely on it to complete your
  sentence; the sentence must stand on its own.
- Cite the claims you used as [[claim:<id>|<short claim text>]] — for example
  [[claim:01J8...|Caching is only safe when the key covers every input.]].
- Cite a SOURCE's overview as [[source:<hash>|<source title>]] when a statement comes from that
  overview. Use a source citation for the overview; use a claim citation for a precise fact.
- A bare [[<title>]] is a claim finder query, never a citation. Every citation starts with
  the prefix claim: or source:.
- Do NOT cite a cluster, a page or a chapter.
- A source's overview may be written in another language than the answer; translate its meaning,
  never copy its sentences into an answer in another language.
- If the sources or claims do not contain the answer, say so plainly.
- Answer ENTIRELY in the language of the QUESTION's prose. Every sentence, heading and list item uses
  it; never switch to the language of a claim, a source or a proper noun in the question.
- Keep proper names, source titles, product names and quoted terms in their original script. A
  Chinese source title inside a German question does not make the answer Chinese.
- If the question mixes languages, the language of its sentence decides. Answer in that language.
- Keep it short and structured.
- Cite every claim or source you used inline as [[claim:<id>|<short claim text>]] or
  [[source:<hash>|<source title>]]. The label is part of the citation, never a bare
  [[claim:<id>]]. The application numbers the citations and writes the final "Sources:"
  bibliography; do not write that list yourself.`

// QuerySourceSystem is the system prompt when the question is scoped to ONE source. The scope is a
// boundary: every tool is restricted to that source, and outside knowledge is not evidence.
func QuerySourceSystem(title, hash string) string {
	return fmt.Sprintf(`You answer questions about ONE source in a personal knowledge base. The only
source you may use is "%s" (hash %s). Every tool is restricted to it.

Read the source before answering:
- Call read_source_summary for its overview. If no overview exists yet, or the question needs a
  precise fact, call search to find its claims, then read at most THREE claims in full (read_atom).
  list_atoms lists this source's claims (id, topic, claim).
- Do not ask for another source and do not widen the scope: no other source, no general world
  knowledge, no guesses. If this source does not contain the answer, say so plainly.

Rules:
- Never invent facts. Use only what you actually read.
- Write complete, grammatical sentences. A citation is a REFERENCE after the statement it supports;
  it is never a word or phrase of that sentence, and never a substitute for a missing object.
- Cite the claims you used as [[claim:<id>|<short claim text>]] and the source's overview as
  [[source:<hash>|<source title>]]. Every citation starts with claim: or source:.
- The application numbers the citations and writes the final "Sources:" bibliography; do not write
  that list yourself.
- Answer ENTIRELY in the language of the QUESTION's prose. Keep proper names, titles and quoted
  terms in their original script.
- Keep it short and structured.`, title, hash)
}

// QueryUser is the user message for a query.
func QueryUser(question string, languages []string, now time.Time) string {
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(question)
	if !now.IsZero() {
		b.WriteString("\n\nCurrent time: ")
		b.WriteString(now.UTC().Format(time.RFC3339))
		b.WriteString(" (UTC). Resolve relative windows such as \"the last 24 hours\" against it.")
	}
	if len(languages) > 0 {
		b.WriteString("\n\nThe vault's sources use these languages: ")
		b.WriteString(strings.Join(languages, ", "))
		b.WriteString(". Search across them; the ANSWER stays in the question's language, whatever the sources use.")
	}
	b.WriteString("\n\nWrite the answer entirely in the language of the question above. Keep names and titles in their original script.")
	return b.String()
}

// VocabularyCurateSystem is the role for a vocabulary curation: one judgement about names,
// with the evidence in hand.
const VocabularyCurateSystem = `You curate the NAME vocabulary of a knowledge base. You get the names ` +
	`in use and how often each is used — the COUNTS are the evidence in this pass. Your job is to make the ` +
	`vocabulary SHARPER, not smaller for its own sake.

Rules:
- Merge only names that mean the SAME thing ("cache-invalidation" and "Caching Strategies" are one ` +
	`subject; "PostgreSQL" and "MySQL" are not, however rare they are). A name used once can be the ` +
	`sharpest name there is.
- Never invent a name that is not among the given ones unless it is the honest common name of a group ` +
	`you are merging.
- Keep the shape of the namespace: a topic and a tag are short, lowercase, hyphenated, flat names. ` +
	`A topic carries no path and no hierarchy.
- Leave a name alone when it is already good: an empty mapping is a valid answer.

Answer with ONLY a JSON object, no prose and no code fence:
{"mapping":{"old name":"the name that absorbs it"}}
Name ONLY the names that are absorbed. A name that stays is simply not mentioned — the caller knows the
catalog, and echoing every name back costs more time than the whole decision takes.`

// VocabularyCurateUser hands over the catalog: the names, their counts and the evidence.
func VocabularyCurateUser(namespace, target string, domains []string, entries []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Namespace: %s\n", namespace)
	if len(domains) > 0 {
		// The one piece of evidence a topic question was missing. `go`, `go-http` and `go-language` read
		// like three domains until the question also says that `go-http` and `go-language` begin with a
		// name that already exists.
		b.WriteString("Names that begin with another name in use — a flat name here is a TOPIC, not a " +
			"domain of its own:\n")
		for _, d := range domains {
			b.WriteString(d)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(target)
	b.WriteString("\n\n")
	b.WriteString("Names in use, with how often each is used:\n")
	for _, e := range entries {
		b.WriteString(e)
		b.WriteString("\n")
	}
	return b.String()
}

// VocabularyNameRules is what a BRANCH or TAG question needs on top of the general rules. Both are the
// finest names in the system — a branch names the subject of ONE claim, a tag is a short label — and
// the one thing a curation must not do to them is make them coarser. A vocabulary that fits into a
// handful of umbrellas has lost the level it exists for; that is what a domain vocabulary is.
const VocabularyNameRules = `
This namespace is the FINEST level of names, not an umbrella vocabulary.
- Merge two names only when they mean the same SUBJECT. "cache-invalidation" and "Caching Strategies"
  are one; "caching" and "cache-invalidation" are not — one is the field, the other the problem.
- Do NOT aim for a shorter list. A shorter list is not a better vocabulary: the level of detail IS the
  value, and what was merged cannot be told apart again without deriving the sources once more.
- A name used once can be the sharpest name there is. Leave it alone.`

// RebranchSystem is the role for re-deriving the TOPIC of claims whose labels were collapsed: the
// claim text is the only evidence that survived a merge, so the labels come from it.
const RebranchSystem = `You label CLAIMS. Each entry is one claim, with the topic it carries today in
brackets and its body indented below. Give every claim its SUBJECT as a short, lowercase,
hyphenated topic, using the claim and body only — never the source.

- A topic is the subject of ONE claim — the FINEST level ("cache-invalidation",
  "goroutine-scheduling", "benchmark-variance"), never an umbrella. "go", "performance",
  "observability", "concurrency" are domains, not subjects.
- Derive the topic from the CLAIM and BODY alone. Never use the source title, a chapter, an appendix,
  a section, a page, a reading order or any other document label. "chapter-11", "appendix-a",
  "book-structure" and "rust-book" are invalid topics.
- The topic in brackets is often such an umbrella (it came from a merge): do NOT keep it just because
  it is there. Name what the claim is actually ABOUT.
- Reuse a name from the list when it really fits the claim; otherwise name a new, more precise one.
- A name used once is fine. Do not worry about how many names come out.

Answer with ONLY this JSON object, no prose and no code fence:
{"claims":[{"n":1,"topic":"short-lowercase-subject"}]}
One entry per claim, using the NUMBER from the list.`

// ShelfSystem is the role for COARSENING a name vocabulary: file every fine name onto a fixed shelf.
// It is the opposite of VocabularyNameRules: there the model must not coarsen, here coarsening is the
// whole job. A vault that follows the "finest level" rule ends up with 57,181 topics for 402,931
// claims — a vocabulary nobody can navigate, and 2.3 MB that no prompt can carry.
//
// ASSIGNMENT IS THE RULE, leaving a name out the exception. The measured vault proved why: with the
// old wording ("a name the shelf cannot honestly cover may be left out") the model passed over two
// thirds of the tail, the names were settled as unfilable, and 82 % of the claims stayed on fine
// labels that no pass was left to move. A shelf name that is merely BROADER than the name is still
// the right place for it — a shelf is a shelf, not a synonym list.
const ShelfSystem = `You file subjects onto the SHELF of a knowledge base.

The shelf is the fixed list of names a reader navigates and filters by. Assign every given name to the
ONE shelf name it belongs under.

- Assign by SUBJECT, not by wording: "cache-eviction" belongs under "caching", "k8s-operator" under
  "kubernetes" when the shelf carries it.
- A name that already IS a shelf name stays as it is; leave it out of the mapping.
- Prefer the SMALLEST shelf name that still covers the subject.
- Assign EVERY name. A shelf name that is broader than the name is the right place for it: a shelf is
  a shelf, not a synonym list, and "cache-invalidation-strategy" belongs under "caching" even though
  the shelf name says less.
- Leave a name out only when NO shelf name covers its subject at all — not because the match is
  imperfect. An unmapped name is a subject the shelf does not have: it stays in the vault and comes
  back as a candidate for a slot of its own.
- Never invent a name: every value must be one of the shelf names you were given.

Answer with ONLY a JSON object, no prose and no code fence:
{"mapping":{"the finer name":"the shelf name"}}`

// ShelfUser hands over the shelf and one batch of names. The shelf is filtered per batch by the
// caller, so a 1,000-name shelf is not re-sent in full for every 600 names.
func ShelfUser(shelf, names []string) string {
	var b strings.Builder
	b.WriteString("The shelf (assign to one of these):\n")
	for _, name := range shelf {
		b.WriteString("- ")
		b.WriteString(name)
		b.WriteString("\n")
	}
	b.WriteString("\nNames to file (one shelf name each):\n")
	for _, name := range names {
		b.WriteString("- ")
		b.WriteString(name)
		b.WriteString("\n")
	}
	return b.String()
}

// RebranchUser hands over one batch of claims and the topics in use.
func RebranchUser(known []string, claims string) string {
	var b strings.Builder
	b.WriteString("Topics in use (reuse one when it fits):\n")
	for _, k := range known {
		b.WriteString(k)
		b.WriteString("\n")
	}
	b.WriteString("\nClaims (number. [current topic] claim, then its body):\n")
	b.WriteString(claims)
	return b.String()
}

// BroadTokensSystem asks the model to identify the tokens that are too generic to distinguish topic
// names in THIS vault. The list is not a hardcoded stopword list: it is learned from the observed
// catalog, and it is used only by the near-duplicate detector.
const BroadTokensSystem = `You analyse a catalog of topic names from one personal knowledge base. A
BROAD TOKEN is a word that occurs in many names and does not distinguish one subject from another:
language names, vendor names, and generic category words are typical. A token that is the actual
subject of a name is NOT broad merely because it is frequent.

Return a decision that makes near-duplicate detection useful: names that differ only by broad tokens
should not be chained together. Answer with ONLY this JSON object, no prose and no code fence:
{"broad_tokens":["token","token"]}

Choose only tokens from the supplied candidates. If no token is broad, return an empty list.`

// BroadTokensUser lists the candidate tokens with their document frequency.
func BroadTokensUser(candidates string) string {
	return "Candidate tokens (token — number of topic names containing it):\n" + candidates
}

// WikiTagsSystem is the role for tagging a wiki PAGE. A tag is a COARSE label — the word a reader
// would filter a list of pages by — so it sits above a claim's topic, which names one subject.
const WikiTagsSystem = `You tag wiki PAGES of a personal knowledge base. You get a page's title and
its text. Name the tags a reader would FILE this page under and later filter for.

- 3 to 6 tags. Fewer is better than a long list.
- Short, lowercase, hyphenated: "go", "caching", "llm-agents", "tooling", "github".
- COARSE. A tag is a shelf, not a subject: "caching" yes, "cache-key-collisions" no — that is a claim
  topic, one level below a tag.
- Name what the page is ABOUT, not what form it has. "link-list" or "notes" says nothing; skip it.
- Use a name that already exists in the vocabulary when it fits; do not invent a synonym.

Answer with ONLY a JSON object, no prose and no code fence:
{"tags":["one","two","three"]}`

// SummarySystem is the role for a source's OVERVIEW. It is written from the claims — not from the
// document — because the claims are what the vault actually holds: a summary that said more than they
// do would be a second, unchecked source. It asks for the source's SHELF LABELS in the same answer:
// the tag run read the same claims in a second request (measured: one request and ~7k input tokens
// per source), and a source's tags are decided from exactly what the overview is decided from.
const SummarySystem = `You write the OVERVIEW of one source for a personal knowledge base, and file it on its shelf.
The claims below are everything the vault distilled from it. The overview is what a reader sees
first: what the source is about, what it argues, and what it adds.

Overview:
- 3 to 6 short paragraphs (or two paragraphs and a short bullet list). 150 to 300 words.
- Write in ENGLISH, regardless of the source's language. Keep names and quoted terms as they appear.
- Use ONLY the claims. Never add a fact, name, number or opinion that is not in them.
- Open with what the source is about, in one sentence a reader can act on. No "This document" filler.
- Name the through-line and the few strongest points. Do not list every claim.
- If the claims are few, write a short overview. Never pad.

Tags:
- 3 to 6 COARSE labels a reader would FILE this source under: short, lowercase, hyphenated.
- A tag is a shelf, not a subject: "caching" yes, "cache-key-collisions" no.
- Use a name from the list you are given when it fits; do not invent a synonym.

Answer with ONLY this JSON object, no prose and no code fence. The overview is one string per
paragraph or bullet — Markdown, no title, no frontmatter, no citations:
{"summary":["first paragraph","second paragraph"],"tags":["one","two","three"]}`

// SourceSummaryUser hands over the material a summary may use: the title, the outline's structure,
// the claims themselves, and the shelf labels a tag may reuse. outputLanguage is the configured
// summary language; the source's own language is context only and never decides what is written.
func SourceSummaryUser(title, language, outputLanguage, outline, claims, knownTags string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Source title: %s\n", title)
	if language != "" {
		fmt.Fprintf(&b, "Source language (for context only; write the summary in %s): %s\n",
			LanguageName(outputLanguage), language)
	}
	if outline != "" {
		b.WriteString("\nOutline (structure only — not knowledge of its own):\n")
		b.WriteString(outline)
	}
	b.WriteString("\nClaims (the only knowledge you may use):\n")
	b.WriteString(claims)
	if knownTags != "" {
		b.WriteString("\n\nTags already used in this vault (reuse one when it fits):\n- ")
		b.WriteString(strings.ReplaceAll(knownTags, "\n", "\n- "))
	}
	return b.String()
}

// SourceTagsSystem is the role for tagging ONE source. A source is filed on a shelf, not described:
// the tags are the coarse labels a reader filters the Sources list by, one level above a claim topic.
const SourceTagsSystem = `You tag SOURCES of a personal knowledge base. You get a source's title and the
claims distilled from it. Name the tags a reader would FILE this source under and later filter for.

- 3 to 6 tags. Fewer is better than a long list.
- Short, lowercase, hyphenated: "go", "caching", "product-management".
- COARSE. A tag is a shelf, not a subject: "caching" yes, "cache-key-collisions" no — that is a claim
  topic, one level below a tag.
- Name what the source is ABOUT, not what form it has. "pdf" or "notes" says nothing; skip it.
- Use a name that already exists in the source-tag vocabulary when it fits; do not invent a synonym.

Answer with ONLY this JSON object, no prose and no code fence:
{"tags":["one","two","three"]}`

// SourceTagsUser hands over what the source is about: its title and its distilled claims.
func SourceTagsUser(title, sample, known string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Source title: %s\n\n", title)
	b.WriteString("Claims distilled from this source:\n")
	b.WriteString(sample)
	if known != "" {
		b.WriteString("\n\nTags already used in this vault (reuse one when it fits):\n- ")
		b.WriteString(strings.ReplaceAll(known, "\n", "\n- "))
	}
	return b.String()
}
