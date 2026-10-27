package agents

import (
	"fmt"
	"strings"
)

// DistillSystem is the system prompt for the distill step.
const DistillSystem = `You convert ONE source document into atomic claims.

Rules:
- One atom = exactly one self-contained claim (a fact, definition, claim, example, counterpoint or quote).
- Write atoms in the source's own language. Keep each claim to a single sentence.
- THE ATOM MUST STAND ALONE. A reader who has never seen the source must understand its topic, claim
  and body. Never write "Chapter 11", "Appendix A", "this section", "the book", "the author",
  "as we saw", "above", "below", "the next chapter", "Page 42", or any other pointer into the
  document. The source is provenance in the sources list, never part of the statement.
- Do NOT extract statements ABOUT the document: what a chapter covers, its reading order, who wrote
  it, its editions, its table of contents, or where an instruction is printed. Those are not
  knowledge about the subject; skip them.
- Restate the context the claim needs inside the claim and body: name the system, version, condition
  or consequence. Do not rely on surrounding sentences.
- Assign each atom a short, flat topic (its subject), e.g. "caching" or "llm-agents". The topic must be
  derivable from the CLAIM and BODY alone — never from the source title, a chapter, an appendix or
  the document structure. A topic like "chapter-11" or "book-structure" is invalid. Reuse an existing
  topic when it fits; call list_topics first if unsure. A topic is flat: no slash, no hierarchy.
- Cite provenance: pass the source node id(s) the claim came from as "nodes". Only cite node ids that
  appear in the outline. Never invent node ids or facts that are not in the source.
- Page furniture is NOT a claim: a running head, a page number, "Page 42", a copyright line, a
  "see the next chapter" pointer or a link back to the table of contents says nothing about the
  subject. Skip it; a part that carries nothing but furniture gets no atom.
- The body may expand the claim in 1-3 sentences. Do not repeat the claim verbatim.
- A claim has NO links. Never write a bracketed pair as a reference: a claim's associations are its
  clusters, not its text.
- Quote code as code. A declaration, expression, literal, identifier or grammar fragment taken from
  the source goes in the body as inline code (a backtick span), never as bare prose. Bracket syntax
  such as an attribute or a grammar production is quoted syntax, not a link. In the claim sentence,
  name what the code is instead of quoting the raw fragment.
- Cover the source's substance: aim for one atom per distinct claim. Prefer more, smaller atoms over
  fewer large ones, but do not split a single claim artificially.
- A sentence that bundles several facts is SEVERAL claims, not one: "X is fast, close to the
  hardware, supports A and B, and is widely used" is four atoms, not one compound atom. Split it.
- A node of a few hundred characters usually carries more than one claim. One atom for a whole
  paragraph is normally under-derived.
- Use read_source_node when the text you were given was cut short and you need the rest of a node.
- You do NOT write anything. There is no write tool: you ANSWER with the claims as JSON, and the
  application records them. ` + DistillAnswerShape

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

// DistillPartUser describes ONE text-bearing node of a source. The work item is exactly that node
// and its complete text — nothing else. Completeness is a property of a single node, so a small
// work item is what makes it checkable: no plan to infer, no percentage to guess, and no answer
// has to swallow a whole document (which is what used to hit the output limit half way).
func DistillPartUser(title, hash, nodeID, nodeTitle, text string) string {
	return fmt.Sprintf(`Source hash (provenance only): %s
Source title (provenance only — never use it in a topic): %s

This is ONE part of that source: node %s "%s".
Its complete text is below. Turn every statement in it into an atom.
The node title is provenance only: do not put it, the chapter number or the document structure into
an atom. Every claim, body and topic must make sense without this source.

Text:
%s

Give one claim per statement, citing node %s. Do not summarise the part into one claim, and do
not leave a statement out.

`+DistillAnswerShape, hash, title, nodeID, nodeTitle, text, nodeID)
}

// DistillPartAudit is the completeness check. The model reads the part's text against the claims
// that now exist for it and writes whatever no claim covers — so "complete" is verified against
// the text instead of being inferred from a count of cited nodes.
func DistillPartAudit(nodeID, nodeTitle, text, claims string) string {
	return fmt.Sprintf(`Check ONE part of a source for completeness: node %s "%s".

Its text:
%s

The claims that exist for this part so far:
%s

Read the text again, statement by statement, and compare it with those claims. ANSWER with the
claims that no existing claim covers yet (one object per statement, citing node %s). Do not
repeat or rewrite a claim that already exists, and do not invent facts.
If every statement of the text is covered, answer exactly: {"complete":true,"claims":[]}

`+DistillAnswerShape, nodeID, nodeTitle, text, claims, nodeID)
}

// DistillPartTruncated picks a cut-off answer up where it stopped. The model did not choose to end
// there — the endpoint's output limit did — so the statements it never reached are simply missing.
func DistillPartTruncated(nodeID, nodeTitle, claims string) string {
	return fmt.Sprintf(`Your previous answer was CUT OFF by the output limit — it did not end because the work was
finished. Continue with node %s "%s" and write the statements that are still missing.

The claims that exist for this part so far:
%s
One claim per statement, citing node %s. Do not repeat what is there.

`+DistillAnswerShape, nodeID, nodeTitle, claims, nodeID)
}

func DistillSourceUser(title, hash, outline string) string {
	return fmt.Sprintf(`Source hash: %s
Source title: %s

Outline (node_id — title — pages):
%s

Extract the source's claims into atoms now.`, hash, title, outline)
}

// QuerySystem is the system prompt for the query operation.
const QuerySystem = `You answer questions from a personal knowledge base of atomic CLAIMS. You are a
careful researcher. Cluster prose does not exist: use only the claims you read through the tools.

How to work — short and deliberate, not exploratory:
1. ONE search with the question's key words. The top hits carry the claim and its body. The search
   already expands inflections and word families; if the vault's sources use another language than
   the question, search the key terms in that language too.
2. If that is not enough, read at most THREE claims (read_atom) — the ones that clearly bear on the
   question. A second search only if the first found nothing.
3. Then answer. Do not keep looking once the claims you have can answer the question.

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
- Do NOT cite documents, chapters or a cluster. Only claims exist for this answer.
- If the claims do not contain the answer, say so plainly.
- Answer in the question's language. Keep it short and structured.
- Cite every claim you used inline as [[claim:<id>|<short claim text>]]. The short claim text is part
  of the citation, never a bare [[claim:<id>]]. The application numbers the citations and writes the
  final "Sources:" bibliography; do not write that list yourself.
  - [[claim:<id>|<short claim text>]]`

// QueryUser is the user message for a query.
func QueryUser(question string, languages []string) string {
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(question)
	if len(languages) > 0 {
		b.WriteString("\n\nThe vault's sources use these languages: ")
		b.WriteString(strings.Join(languages, ", "))
		b.WriteString(". Search and answer across them; do not assume the question's language is the only one in the vault.")
	}
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
- Keep the shape of the namespace: topics are hierarchical paths "<domain>/<topic>" (at most two ` +
	`levels); topics and tags are short lowercase hyphenated names.
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
// do would be a second, unchecked source.
const SummarySystem = `You write the SUMMARY of one source for a personal knowledge base. The claims below
are everything the vault distilled from it. Your summary is the overview a reader sees first: what the
source is about, what it argues, and what it adds.

Rules:
- 3 to 6 short paragraphs (or two paragraphs and a short bullet list). 150 to 300 words.
- Write in the source's language.
- Use ONLY the claims. Never add a fact, name, number or opinion that is not in them.
- Open with what the source is about, in one sentence a reader can act on. No "This document" filler.
- Name the through-line and the few strongest points. Do not list every claim.
- Plain Markdown prose: no title, no frontmatter, no citations, no code fence.
- If the claims are few, write a short summary. Never pad.`

// SourceSummaryUser hands over the material a summary may use: the title, the outline's structure,
// and the claims themselves.
func SourceSummaryUser(title, language, outline, claims string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Source title: %s\n", title)
	if language != "" {
		fmt.Fprintf(&b, "Language of the source: %s\n", language)
	}
	if outline != "" {
		b.WriteString("\nOutline (structure only — not knowledge of its own):\n")
		b.WriteString(outline)
	}
	b.WriteString("\nClaims (the only knowledge you may use):\n")
	b.WriteString(claims)
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
