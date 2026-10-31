# Thought Keeper — the agent

The agent is a **bounded tool-calling loop** (`internal/core/usecases/agents/`) over the native
OpenAI SDK. Prompting, tool definitions and the loop live in the core; the SDK lives only in
`infrastructure/gateways/openai_agent.go` behind `interfaces.AgentGateway`.

Its operations are **distill**, **query**, **tag** (a wiki page's tags or a source's auto tags),
**summary** (a source's overview), **command** (the assistant: one utterance → one GUI state),
**lint** (programmatic today) and the **vocabulary** verbs
(curate, retopic, learn broad tokens). **The cluster step is deterministic and has no model**: it is a claim graph, not a naming
task.

## 1. Tools

**Tools only READ.** The model reaches the world through these tools, and none writes: a write is an
*answer*, and the app records it.

| Operation | Tools |
| --- | --- |
| Distill (one part) | `list_topics`, `read_source_node` |
| Query | `list_topics`, `search` (claims), `read_atom`, `list_atoms`, `list_sources`, `read_source_summary` |
| Lint (semantic, planned) | the query set plus `report_finding` |
| Tag / vocabulary | none — one bounded completion, answer as JSON |
| Summary (one source) | none — one bounded completion, answer as Markdown prose |

`read_atom` returns the claim together with its **cluster peers** (bounded to eight: id, claim,
topic). The association is context for the answer, and the ids let the model read a peer in full if
it needs the body. A claim body carries no links; the cluster is the association.

`list_sources` reads a **recency window**: the newest content sources by vault arrival
(`imported_at`), with a short excerpt of each overview. It is **paged**: a page carries
`next_offset`, and the model asks for the next page with `offset` until the window is read.
Bookmarks and sitemap/feed containers are counted, never listed — they have no derivation to
summarise. `read_source_summary` opens one overview in full; when none has been written yet it
returns the source's claims (or its derivation state while the source is still being processed).

## 2. The transcript stays bounded

- One tool answer is capped at **6 000 characters**.
- Older answers are replaced by a one-line note once carried output exceeds **24 000 characters**;
  the newest three stay.
- **No message is ever empty** (`(tool call)` / `(no output)`).
- `max_iterations` and a per-operation timeout are config; hitting a bound is a recorded failure,
  never a silent stop.

## 3. Distill: one source, one part at a time

A **part** is ONE text-bearing node. `pipeline.parallel_parts` parts run at once,
`pipeline.parts_per_run` bounds a run, and a big document converges over several runs
(`auto_continue`).

For each part the agent:

1. Answers with the part's claims as JSON (`topic`, `claim`, `body`, `type`, `nodes`), **one claim
   per statement**. The app writes the atoms; a claim that names no node inherits the part.
2. **Audits** the part: it re-reads the text against the claims that now exist and writes whatever
   no claim covers, or answers `{"complete":true,"claims":[]}`.

Rules the prompt enforces (and the wire shape):

- **One claim per atom**, a single self-contained sentence, **always in the configured claim
  language** (`language.claims`, default English); proper names, titles, code and quoted terms keep
  their original script.
- **A claim stands alone.** No "Chapter 11", "this section", "the book", "as we saw". The source is
  provenance, never part of the statement.
- **A flat topic in the same language** derived from the claim and body alone (`caching`,
  `llm-agents`). Existing names are offered by `list_topics`; a topic is never a chapter, a document
  or a hierarchy.
- **Provenance cites source node ids** from the outline; unknown ids are rejected.
- **No links, and code is code.** A claim body carries no links; syntax quoted from the source is
  inline code (a backtick span), and the claim sentence names it in prose.
- **Page furniture is not a claim**: running heads, page numbers, copyright lines, cross-references.
- More, smaller atoms beat fewer large ones; a compound sentence is several claims.

### Part outcomes

- Only `done` parts are skipped on the next run.
- A cut-off answer is continued from where it stopped; if it still cannot finish, the part is
  `incomplete` with `reason=truncated`.
- A part that yields no claim is `incomplete` with `reason=no_claims` — unless the audit confirmed it
  is complete, in which case it is `done` with `reason=source_relative` (document metadata or a link
  list).
- An `incomplete` part is derived again after its own atoms are dropped.

**A reset is the "start over" button**: it starts at **step 2**. Claims, tree, part records, log and
derived title go; the source itself stays. `distill {reset:true}` also runs capture again. A reset
never deletes a cluster record: the next cluster pass prunes dead members.

### 3.1 Summary: the overview of one source

The summary is written **after** a source's distill step is `ok`, from the claims (capped at 250
claims / 36 KiB) and the outline's titles — never from the raw document, so it cannot say more than
the vault holds. One bounded completion, `reasoning_effort=minimal`, no tools. The answer is
Markdown prose (3–6 short paragraphs or two paragraphs plus bullets, **always in the configured
summary language** (`language.summary`, default English), whatever the source's language) and is
written to `<hash>.summary.md`. An answer cut off by the output limit is
a failure: the file is not written, and the run retries later. A source with zero claims gets the
honest one-line file without a model call. The consistency job queues summaries that are missing; it
never writes one inside the check itself.

## 4. Query: question → cited answer

The question is classified first, and the two branches do not mix.

Queries are independent and **never gated**: up to `pipeline.parallel_queries` runs answer questions
at the same time, and the reader may ask again while an answer is still being written. A query reads
the vault as it is; a half-associated vault is still worth asking.

- **A subject question** ("what do the claims say about caching?"): one search with the question's
  key words. The top hits carry the claim and its body. If needed, read at most three claims
  (`read_atom`). A second search only when the first found nothing.
- **A recency or digest question** ("what is new?", "summarise the sources of the last 24 hours"):
  `list_sources` first, with the window the question named (`since` as `24h`, `7d` or an RFC3339
  timestamp). The list carries each source's title, arrival time, tags, claim count, derivation
  state and a short excerpt of its overview. Open at most three full overviews
  (`read_source_summary`) when an excerpt is not enough. A source without an overview is still being
  processed: say so, or read its claims with `list_atoms {source}` — never invent its content.
  - The window is **vault arrival** (`imported_at`), not publication. The answer says "added to the
    vault", never "published".
  - An empty window is stated plainly; it is never widened silently.
  - The list is a **page**: while `next_offset` is greater than zero the model asks for it, with the
    same `since`, until the whole window has been read. Stopping at the first page is not a digest.
  - If the window is too large for the tool budget, the answer groups what it read by theme, names
    the total and says that it covers the newest ones. It never implies completeness it does not
    have.
  - Bookmarks and sitemap/feed containers are counted, not summarised: they have no derivation.

A question can be **scoped to one source** — the Ask action on a source page. The source hash travels
with the run: the system prompt names it and every tool is restricted to that source. `list_topics`
and `list_sources` are not offered; `search` is the same lexical ladder with a provenance filter;
`list_atoms` and `read_source_summary` are pinned to the source; `read_atom` refuses a claim that does
not come from it and omits cluster peers (a peer may belong to another source). If that one source
does not contain the answer, the answer says so; it never widens the scope silently.

The answer cites **claims** as `[[claim:<id>|short claim text]]` and a **source's overview** as
`[[source:<hash>|<source title>]]`. A claim citation supports a precise statement; a source citation
supports a statement taken from that source's overview. Clusters, pages and chapters are never
citations.

- Every cited claim or source is numbered and repeated in the final `Sources:` bibliography; that
  block is omitted when the answer cites nothing. Prose carries numbered references such as
  `([1](#cite-1))`; the bibliography repeats the number beside the claim text or the source title and
  carries the actual link (a claim page or a source page). Clicking a reference scrolls to its
  bibliography entry, like a footnote.
- The answer is written in complete, grammatical sentences. A citation is a trailing reference after
  the statement it supports — never a word, phrase or missing object of the sentence.
- A source's overview may be written in another language; the answer translates its meaning and never
  copies its sentences into another language.
- If the claims and the source overviews do not contain the answer, the answer says so plainly.
- The answer is written **entirely in the language of the question's prose**; the vault's language
  set is only a search hint. A source title or proper noun in another script stays in that script and
  never decides the answer's language. A German question that names a Chinese source is answered in
  German.
- Every successful query writes an Ask record to `asks/<run-id>.md`. There is no filing step.

## 4.1 Command: one utterance → one GUI state

The assistant is not a chat and not a second answer: a person says what to SHOW, and the app shows
it. The model's only job is the READING — which lens, which of the app's own dimensions, which
order. It never writes a URL, never invents a hash and never answers the request.

- **A closed schema.** `lens` (`sources|claims|wiki|status|attention|ask`), `text`, `tag`, `topic`,
  `kind` (`md|pdf|txt|epub|url`), `since`, `state` (`open|failed|done`), `order`, `bookmarks`,
  `attached`. Every field is optional; unknown fields are dropped, and an unknown clause is not
  silently accepted — the reading is validated field by field in Go.
- **The vault's own vocabulary goes in.** The prompt carries the known source tags and claim topics
  (bounded), so a plausible word cannot become a filter that matches nothing. A word that is not in
  them stays free text and goes to the lexical search.
- **A reading that narrows nothing is not a reading.** If the model returns only the default lens,
  the answer is discarded and the deterministic parser decides — and that parser refuses to call an
  unrecognised utterance a command at all (`422 unparsed`). An assistant that answers "I did not
  understand" with the whole vault is worse than one that says it.
- **A SUBJECT is never a filter.** A filter is decidable without reading anything: a tag, a kind, an
  arrival window, a step state, a reference the reader pointed at, or words the user names AS words
  ("sources that mention Kamal"). Everything else — anything with "about"/"über"/"zum Thema", any
  request that would need the model to look at the content — is research, even when the lens is as
  small as the wiki. Word-matching is the no-model fallback, not the answer: a page that says
  "projects" once is not a page ABOUT projects, and only a reading can tell the two apart.
- **Two routes, and the model picks one.** `filter` is for a request that is EXACTLY expressible
  with the vault's own fields (tag, kind, arrival window, step state, bookmarks, attached, a literal
  word search): the resolution is deterministic, exact and complete. `research` is for everything
  that needs judgement about what the content says — "sources about 37signals" is not a source field,
  it is a question about the claims. `ask` delegates a question to the query agent.
- **A subject request is mapped onto the vault's OWN semantic layer.** Every source carries tags an
  LLM derived from its claims; every claim carries a flat topic. Those names are the index a subject
  request is answered from: the tag `software-architecture` covers 67 sources without reading one.
  The flow is `words → terms → NAMES → the set`:
  - the model turns the reader's words into 3–6 search terms (translation, the established domain
    noun, hyphenated forms). It never names a tag or a topic — the vocabulary is the authority on
    names, and a model that invents one produces a filter for nothing;
  - the server matches those terms (and the request's own words) against the real names, in Go, with
    word-level matching and inverse document frequency: "architecture" identifies a name that
    "software" (in three names) does not. Four similarity rules, weakest last: exact name, the same
    word after a plural/German suffix, one edit away (two in a long word), and the same Cologne
    phonetic code — which is what carries "Müller/Miller". The curated ALIAS table is consulted
    first, so a name a curation merged away still lands on the one the vault uses now;
  - the set is a JOIN: tagged sources, plus the sources whose claims carry the mapped topics.
  Both are index reads. No content is read, no candidate is judged with an excerpt, nothing is
  summarized. Reading content was the earlier design: slow, unreproducible, and worse than the
  metadata the vault already had.
- **Without a model the same matcher runs on the request's own words.** When the wording agrees with
  the vault's names it still finds them; when it does not, the record says the match ran without the
  model instead of pretending the vault is empty.
- **The text of a filter is matched WORD BY WORD, not as a substring.** "Software Architektur" must
  not depend on that exact phrase appearing in a title: every word must be found (in a source's
  title/name/URL/tags, in a wiki page's title/tags/prose), and only when nothing has all of them is
  the reading relaxed to "any word" — with a note that says so. Its one-sentence rationale is
  stored with the set and shown in the banner: a curated set is a different promise from an exact
  filter, and the reader must be able to tell them apart.
- **The set is materialized** (`commands/<id>.md`), which is what makes "what was true then" and
  "what is true now" two different, honest things: the record keeps its members, a deleted source
  stops resolving, and "run again" re-resolves the criteria into a new record.
- **No tools and no run row of its own.** It is one bounded completion, like the summary: the
  command record IS the audit (the utterance, the criteria, `generated: llm|rules`). It writes no
  claim, no source and no wiki page.

## 5. Tag: a wiki page's shelf

- A save queues a `tag` run (its own op and worker, **never on the request path**). The model reads
  the page's title and prose and writes 3–6 **coarse** tags into `auto_tags`.
- `tags` stays the human's: a tagging run never overwrites what the reader wrote. Deleting a derived
  tag sticks (the next run replaces exactly that key), and a save keeps both.
- The page tagger reads the `wiki-tags` alias table, so a name a curation merged away is not
  reinvented. The source tagger does the same with `source-tags`: it reads the source's claims and
  proposes 3–6 coarse labels, kept apart from the human's. A source whose distill is complete is
  tagged after the run; a source that was never tagged is backfilled by the consistency job, a few per
  pass. An empty answer is recorded as "tagged, nothing fit" and is not asked again.
- A tag is a SHELF, above a claim's topic: coarse names, existing ones reused.

## 6. Vocabulary verbs

- **`POST /vocabulary/curate`** observes all three namespaces (`topics`, `wiki-tags`, `source-tags`), asks the model
  which names mean the same, and applies the answer as renames. It learns **broad tokens** (language
  names, generic categories) from the observed catalog; they live in `vocabulary/topics.md` and are
  used only by the near-duplicate detector. Near-duplicate families above the group cap are reported
  as **too broad to merge**, never offered as a merge question.
- **A judged name stays judged.** Every name a curation decided about (kept or merged) is written
  to the vocabulary's `Decided` list, and the next pass only asks about names that are new or were
  never reviewed — the catalogue is not re-questioned each cooldown. Losing that list turned every
  automatic pass into a full-catalogue model run over thousands of names.
- **`POST /vocabulary/retopic`** re-derives each claim's flat topic from the claim text (bounded per
  run; the next press continues). The queue is the clock: it reads the OLDEST claims through the
  index (`updated_at, id`), so a budget of 12k claims reads 12k rows — not the corpus. Only a claim
  whose label actually moves is re-read whole before the write, because the save is authoritative
  for its provenance.
- Both are **renames**: no claim text is rewritten and no cluster moves.
- A curation holds the **exclusive** side of the gate for the whole run: it rewrites claim names,
  so no document step may be writing claims beside it. While it waits for the drain it is reported as
  `waiting`; the questions then run inside the gate. Re-topic is a vocabulary write too and takes the
  same exclusive side.

## 7. Lint

- **Programmatic (Go, seconds):** orphan atoms, atoms without provenance, atoms
  whose topic/claim/body are not standalone, vocabulary drift, empty bodies, duplicate ids. The
  vocabulary report is **aggregated** into at most three findings (collisions, near-duplicate
  families, long tail); it never emits one row per family. `info` findings are collapsed under
  Status → Knowledge health.
- **Semantic (agent):** deferred. It reuses the query tools plus `report_finding`.
- `POST /api/v1/cleanup` repairs what lint flags without the agent: dead cluster members are pruned
  and the programmatic findings are refreshed.

## 8. Models, patience and audit

- **Two models.** `model_compile` (cheap) runs the document steps, tagging and vocabulary calls;
  `model_reason` (strong) answers queries and will run semantic lint. The cluster step uses none.
- **Think less where thinking buys nothing.** `reasoning_effort_compile` and
  `reasoning_effort_distill` default to `minimal`; `reasoning_effort_reason` too.
- **`reasoning_content` is echoed even when empty.** A thinking endpoint rejects a conversation
  whose assistant turns lost the key; the gateway restores it on every assistant message.
- **A transient model failure is retried** (timeout, 429, 5xx) with a doubling backoff; finished
  parts are kept and the source is requeued. A wrong key or a bad request is not retried.
- **The agent needs a key.** Without it the app runs, distill answers `501` and `/status` reports
  `agent: off`. The cluster step still works: it is deterministic.
- **Everything is recorded.** Every run writes a `runs` row; every tool call writes a `run_steps`
  row. Both append-only and untouched by `reindex`.
- **No silent writes.** Agent Markdown is rendered through the same codecs as any other write.
  Output that fails to decode is a recorded failure with the raw output retained.
