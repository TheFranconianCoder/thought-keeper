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
| Distill (one part) | `read_source_node` |
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
Bookmarks and sitemap/feed/listing containers are counted, never listed — they have no derivation to
summarise. `read_source_summary` opens one overview in full; when none has been written yet it
returns the source's claims (or its derivation state while the source is still being processed).

## 2. The transcript stays bounded

- One tool answer is capped at **6 000 bytes**; a node longer than a page is read with
  `read_source_node {node_id, offset}`, which answers 5 000 bytes at a time and names the next offset.
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
- **A flat topic in the same language**, derived from the claim and body alone (`caching`,
  `llm-agents`), chosen from the names the request offers: two dozen candidates selected from the
  shelf by IDF-weighted overlap with the part's text. A topic is never a chapter, a document or a
  hierarchy, and it is a SHELF — the write resolves the model's name against the catalog, mapping a
  spelling onto its canonical name and, once the shelf is full, onto the best fitting IN-BAND name
  (ADR 0009: never a catch-all or a thin name). A proposal no in-band name fits is kept, not forced.
- **Provenance cites source node ids** from the outline; unknown ids are rejected.
- **No links, and code is code.** A claim body carries no links; syntax quoted from the source is
  inline code (a backtick span), and the claim sentence names it in prose.
- **Page furniture is not a claim**: running heads, page numbers, copyright lines, cross-references.
- More, smaller atoms beat fewer large ones; a compound sentence is several claims.

### The audit is a sample

The completeness check is a second request per part, and on the measured vault it was 32.5 % of every
token spent while the sample said "complete" almost always. `pipeline.audit_every` (default 4) audits
one part in n; a part that produced **no** claim is always audited (that is how a metadata part is
told apart from a failed derivation), and so is a long text that produced fewer than one claim per
1,500 characters. The sample is taken over the part id, so the same part always gets the same answer.

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

### 3.1 Summary: the overview of one source, and its shelf labels

The summary is written **after** a source's distill step is `ok`, from the claims (capped at 250
claims / 36 KiB) and the outline's titles — never from the raw document, so it cannot say more than
the vault holds. One bounded completion, `reasoning_effort=minimal`, no tools. The answer carries
the overview **and the source's auto tags**: both are decided from the same claims, and asking twice
was a request and ~7k input tokens per source for the same evidence. The answer is
`{"summary":[…paragraphs…],"tags":[…]}`: one string per paragraph, because Markdown prose does not
reliably arrive with its newlines escaped, and the file is written from the paragraphs. The
overview is 3–6 short paragraphs or two paragraphs plus bullets, **always in the configured summary
language** (`language.summary`, default English), whatever the source's language, and is written to
`<hash>.summary.md`; the labels go into the source's record as `auto_tags`, exactly as the tag pass
would write them, and only when the source has none yet. An answer cut off by the output limit is a
failure: neither file nor labels are written, and the run retries later. A source with zero claims
gets the honest one-line file without a model call. The consistency job queues summaries that are
missing; it never writes one inside the check itself. The tag pass stays as the backfill for a
source whose summary never ran.

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
  - The window is **vault arrival** (`imported_at`). A source also carries the date it declares for
    itself (`published_at`, with the layer that found it), which the list shows: the answer may name
    that date, but it never reorders or filters the window — "added to the vault" stays what the
    window means.
  - An empty window is stated plainly; it is never widened silently.
  - The list is a **page**: while `next_offset` is greater than zero the model asks for it, with the
    same `since`, until the whole window has been read. Stopping at the first page is not a digest.
  - If the window is too large for the tool budget, the answer groups what it read by theme, names
    the total and says that it covers the newest ones. It never implies completeness it does not
    have.
  - Bookmarks and sitemap/feed/listing containers are counted, not summarised: they have no
    derivation.

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
  `kind` (`md|pdf|txt|epub|url`), `window`, `state` (`open|failed|done`), `order`, `bookmarks`,
  `attached`. Every field is optional; unknown fields are dropped, and an unknown clause is not
  silently accepted — the reading is validated field by field in Go. `window` is one of `today`,
  `yesterday`, `this-week`, `last-week`, `this-month`, `last-month`, `last-<N>h|d|w|m` or
  `YYYY-MM-DD..YYYY-MM-DD`.
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
- **A question is not automatically Ask.** `ask` is for PROSE: an explanation, a synthesis, a
  comparison, an answer. A question that asks WHICH items exist is still a list: "Was haben wir von
  DHH?" is a Sources filter, "Welche Quellen sind fehlgeschlagen?" is the Attention list. A question
  mark alone never routes to Ask.
- **A person is a subject, never an author.** The vault stores no authors. "Sources from/by X" is
  read as "sources about X": the name goes into `text`, the server searches the claims and titles,
  and may also map the person's core ideas (the vault's own topics/tags) when the name itself is
  sparse. The assistant never claims that a source was written by a person.
- **Two routes, and the model picks one.** `filter` is for a request that is EXACTLY expressible
  with the vault's own fields (tag, kind, arrival window, step state, bookmarks, attached, a literal
  word search): the resolution is deterministic, exact and complete. `research` is for everything
  that needs judgement about what the content says — "sources about 37signals" is not a source field,
  it is a question about the claims; its resolution is `mapped`. `ask` delegates a prose question to
  the query agent.
- **A subject request is mapped onto the vault's OWN semantic layer.** Every source carries tags an
  LLM derived from its claims; every claim carries a flat topic. Those names are the index a subject
  request is answered from: the tag `software-architecture` covers 67 sources without reading one.
  The flow is `words → terms → NAMES + mentions → the set`:
  - the model turns the reader's words into 2–4 search terms (translation, the established domain
    noun, hyphenated forms). It never names a tag or a topic — the vocabulary is the authority on
    names, and a model that invents one produces a filter for nothing. The terms must REPLACE the
    reader's words, not add related concepts: "LLM" must not add "open-source";
  - the server matches those terms (and the request's own words) against the real names, in Go, with
    word-level matching and inverse document frequency: "architecture" identifies a name that
    "software" (in three names) does not. Exact and full-phrase matches beat a partial one; a single
    shared token in two multi-word names is not a name ("learning" is not "machine-learning", and
    "intelligence-community" is not "artificial-intelligence"). A model-only term is downweighted;
  - a BROAD name is refused unless the reader named its words themselves: `agile-development` (417
    sources) is a field, not the answer to "agile"; `open-source` survives "open source" and not an
    "LLM" expansion. The terms are recorded with the command, so a mapping is checkable;
  - the set is a JOIN: tagged sources, the sources whose claims carry the mapped topics, and the
    LEXICAL channel. The lexical channel is a strict AND over `atoms_fts` (the claims that mention
    the words, mapped to their sources) plus the source's own title/URL/tag text. It is what carries
    a person's name when the vault has no author metadata. No source content is read, no candidate
    is judged with an excerpt, nothing is summarized. Reading content was the earlier design: slow,
    unreproducible, and worse than the metadata the vault already had.
- **Without a model the same matcher runs on the request's own words.** When the wording agrees with
  the vault's names it still finds them; when it does not, the record says the match ran without the
  model instead of pretending the vault is empty.
- **The text of a filter is literal, and it has two channels.** A source's words are matched in SQL
  over title, original name, URL and tags; when nothing matches there, the strict claims channel
  finds the claims that mention the words and maps their provenance to sources. A wiki page is
  matched word by word over title/tags/prose. The reading is stored with the set: a curated set is a
  different promise from an exact filter, and the reader must be able to tell them apart.
- **A time window is a SPEC, not a pair of instants.** `yesterday` must mean the reader's yesterday,
  and "run again" must re-anchor it. Calendar terms use the reader's IANA timezone (the PWA sends
  it): `today` is local midnight to now, `yesterday` the previous local day, `this-week` Monday to
  now, `last-week` the previous Monday–Sunday, `this-month`/`last-month` the local month.
  `last-Nh|d|w|m` are rolling windows. The vault knows ARRIVAL (a source), CREATION (a claim) and the
  last EDIT (a wiki page), and a source carries the date it declares for itself — which is shown, not
  filtered. A source FILTER applies its window in SQL
  (`imported_at`), so a window over an old part of the vault is not lost to a scan limit; a mapped
  set applies it to the joined sources.
- **The set is materialized** (`commands/<id>.md`), which is what makes "what was true then" and
  "what is true now" two different, honest things: the record keeps its members, a deleted source
  stops resolving, and "run again" re-resolves the criteria into a new record. The record also keeps
  the resolved window (`from`/`to`/`timezone`), the vocabulary `terms` and the end-to-end `took_ms`,
  so a run can be audited after the fact.
- **No tools and no run row of its own.** It is one bounded completion, like the summary: the
  command record IS the audit (the utterance, the criteria, `generated: llm|rules`). It writes no
  claim, no source and no wiki page.

## 5. Tag: a wiki page's shelf

- A save queues a `tag` run (its own op and worker, **never on the request path**). The model reads
  the page's title and prose and writes 3–6 **coarse** tags into `auto_tags`.
- A **source's** labels come with its summary (§3.1). The source tagger remains the backfill for a
  source whose summary has not run yet; whichever runs first sets `auto_tags_at`.
- `tags` stays the human's: a tagging run never overwrites what the reader wrote. Deleting a derived
  tag sticks (the next run replaces exactly that key), and a save keeps both.
- The page tagger reads the `wiki-tags` alias table, so a name a curation merged away is not
  reinvented. The source tagger does the same with `source-tags`: it reads the source's claims and
  proposes 3–6 coarse labels, kept apart from the human's. A source whose distill is complete is
  tagged after the run; a source that was never tagged is backfilled by the consistency job, a few per
  pass. An empty answer is recorded as "tagged, nothing fit" and is not asked again.
- A tag is a SHELF, above a claim's topic: coarse names, existing ones reused.

## 6. Vocabulary verbs

- **The topic is a SHELF** (`docs/adr/0006-the-topic-is-a-shelf.md`): a namespace carries a bounded
  set of names (`vocabulary.topics.max_names`, default 3,000; `vocabulary.source_tags.max_names`,
  default 200). The write resolves every proposed name onto it, so the catalog cannot grow with the
  corpus; the units below only keep it healthy inside the shelf.
- **`POST /vocabulary/curate`** observes all three namespaces (`topics`, `wiki-tags`, `source-tags`), asks the model
  which names mean the same, and applies the answer as renames. It learns **broad tokens** (language
  names, generic categories) from the observed catalog; they live in `vocabulary/topics.md` and are
  used only by the near-duplicate detector. Near-duplicate families above the group cap are reported
  as **too broad to merge**, never offered as a merge question.
- **A judged name stays judged.** Every name a curation decided about (kept or merged) is written
  to the vocabulary's `Decided` list, and the next pass only asks about names that are new or were
  never reviewed — the catalogue is not re-questioned each cooldown. Losing that list turned every
  automatic pass into a full-catalogue model run over thousands of names.
- **The shelf of `topics` is built on its own.** A vault that predates the shelf has a catalog of tens
  of thousands of names and a target it never reached, and no other unit would build it: the curation
  merges synonyms, it cannot fix granularity (−4 % measured, ADR 0006). The reconcile tick therefore
  writes `vocabulary/topics.md` with the shelf the target asks for — the most used names of the
  catalog as it is NOW, no model, no gate, no claim rewritten — and trims it when
  `vocabulary.topics.max_names` moves down. The catalog is ordered by uses first (the index answers
  alphabetically, and slicing that order is how a measured vault got a shelf of the alphabetically
  first names covering 38 % of its claims instead of the most used names covering 82 %). The build
  rule is VERSIONED (`shelf_built:`): a shelf that does not carry the current version is rebuilt
  once, and the `Decided`/`Relabelled` verdicts made against its names are dropped with it, because
  they were answers about a target that never was. The target is a CEILING: the share band decides
  how many names are really usable, so a shelf may be smaller than `max_names`. That is the
  record the **derivation reads**: candidates come from the in-band shelf (ADR 0009), and a proposed name that is
  not on it is mapped onto the best fitting in-band shelf name at the write. The tag namespaces get no such build
  (their write path reads `Canonical`); their shelf comes with the coarsening.
- **`POST /vocabulary/coarsen`** files a namespace's long tail onto its shelf. Three passes, cheapest
  first. The deterministic **families** (near-duplicates, no model). The **variants**: a tail name that
  carries a shelf name as a contiguous phrase of at least two words — "sample-size-insensitivity",
  "go-code-generation", "string-to-floating-point-conversion" — is filed with no model call. The rule
  is literal on purpose: a shared subject word never qualifies, so "moral-psychology" is not
  "evolutionary-psychology". Then the model, in batches of **150 names** (a measured vault answered
  six of 600, so the batch is sized for the answer, not for the request) with **four questions in
  flight** at once — the APPLY stays in order, because it rewrites claims and the number left counts
  it; the gateway keeps the process-wide cap (`openai.max_concurrent`). Each answered batch is applied
  through the same rename the curation uses and recorded in `Absorbed` — the watermark, so a stopped
  pass continues where it was. The run reports what it **asked** and what it **filed**, and the number
  left is what the vault still shows outside the shelf (a batch always walks 150 names; it does not
  always file them). Three questions in a row that come back empty or fail stop the pass: the shelf
  cannot be asked any further right now, and the budget is not worth burning. **Assignment is the
  rule**: a shelf name that is merely broader than the name is the right place for it, so the model
  leaves a name out only when no shelf name covers its subject at all. That is a POLICY with a
  version: the vocabulary file records `settle:`, and a pass that finds an older version drops the
  verdicts of the names outside the shelf and asks them again — without it a rule change could never
  reach the tail the previous rule had closed (the measured vault had settled two thirds of its tail
  and left 82 % of its claims on fine labels). A rename writes the
  claim files and records
  the new topics in **one transaction per batch** — the text and the provenance do not move, and a
  transaction per claim is what made a real coarsening take hours. It is the repair for a vocabulary
  that grew before the shelf existed and it holds the exclusive gate for the whole pass. Nothing in
  the PWA starts it: the reconcile tick asks for the pass, and `POST /vocabulary/coarsen` (CLI
  `thought-keeper coarsen`) exists for tooling. A pass takes the one-at-a-time latch: a second one
  while it runs is a conflict, and the automatic shelf look and the curation stay out of its way.
- **The shelf follows the corpus.** The derivation files new claims onto the shelf, so without a
  refit the shelf could only shrink and a new subject could never appear. The reconcile tick
  re-synthesizes a namespace's shelf at most once per `vocabulary.shelf.refit_interval` (24h), and it
  does so BEFORE the filing passes of that look. It is deterministic and rewrites no claim, so it may
  run while a tail exists — waiting for "nothing left to ask" made it unreachable on the vault it
  exists for: a shelf built under the wrong rule leaves a tail that can never settle (ADR 0009: the
  shelf moves first, the corpus follows). It still does not churn with the coarsening inside one look,
  because a refit that moved a shelf returns and the next tick re-reads the report and picks the pass
  the corrected names need. A topic is judged by its SHARE of all
  claims (ADR 0009): a name above `vocabulary.shelf.max_share` (0.02) is a catch-all, one below
  `vocabulary.shelf.min_share` (0.0001) is thin. A catch-all or a thin name gives its slot up, even with
  free slots, and is never promoted; a name inside the band that carries at least `2×` the coldest slot
  takes that slot, with free slots below `max_names` it simply joins. The refit writes a RECORD — no model, no gate, no claim — and the
  corpus follows: the claims on an evicted name are off the shelf again, which is exactly the queue the
  retopic pass works through. And so that a genuinely new subject can appear at all, the derivation
  keeps a proposal that is not on the shelf while the names outside it stay under its **headroom**
  (a tenth of the shelf): a settled vault has room for new subjects, a vault still filing its tail has
  none. The refit promotes the ones that earn the mass; the coarsening files the rest in.
- **The coarsening runs by itself.** It is the one piece of housekeeping a measured vault needed for
  days, and a button that has to be pressed every forty minutes is not housekeeping. The reconcile
  tick asks for a pass while any namespace still has names outside the shelf — the SMALLEST tail
  first, so forty source tags are finished instead of waiting behind forty thousand topics — and asks
  again after the cooldown when the pass ended. A name the model passes over is **judged and kept**
  (`Decided`), so the next pass continues further into the tail instead of re-asking the same names;
  that is what makes the count of what is left converge. An answer that maps nothing is not a
  judgement, so it settles nothing. The automatic pass is bounded by `vocabulary.coarsen.budget`
  (15 min): it stops ASKING when the budget is spent, applies the questions already in flight and
  returns, so the exclusive gate goes back to the document steps between passes. It is bounded by
  progress like every other loop (`two passes without progress stop it and say why`), and
  `vocabulary.coarsen.automatic: false` turns it off. The endpoint and the CLI verb remain as "run one
  pass now" for tooling — the same pass with the run's own ceiling instead of the budget. The PWA has
  no vocabulary button: Status shows the numbers and the running pass, and that is the whole report.
- **`POST /vocabulary/retopic`** re-derives the topic of the claims the SHELF COULD NOT TAKE, from the
  claims' own text — and every answer resolves onto the shelf. It is the second half of the reduction:
  the coarsening renames a NAME (one answer moves all of its claims), so it can only place names that
  mean one thing; a name whose claims are several subjects, or one subject the shelf spells
  differently, is what this pass is for. The queue is the names outside the shelf that no pass has
  answered for yet, most used first, and a question takes **a hundred claims drawn from ANY of them**:
  the question is the claim's text, not the name it was filed under, and asking one name's seven
  claims at a time spent a call per name (45,000 calls for 341,000 claims on a measured vault, against
  ~3,400 batch-wise). **Four questions are in flight** — the buffer holds one chunk per thread, and the
  answers are applied in order; filling it to a single chunk was the measured bottleneck, because the
  configured parallelism never applied. The pass measures its progress in **claims**, not names: one
  large name can consume several passes without being settled, so a guard that watched the name count
  stopped a queue that was visibly shrinking. An alias is only used when it lands ON the shelf; one
  that points outside is chained to its shelf name or dropped, so the repair cannot re-file a claim
  under the fine name it just left. A name is recorded in `Relabelled` when its LAST claim has been asked, so a name
  that legitimately stays fine is a remainder the report shows, never a question that comes back on
  every cycle. It does NOT rewrite the vocabulary record — the shelf, the alias table and the judged
  names are decisions. A vault without a shelf keeps the oldest-first repair (`updated_at, id`), where
  only a claim whose label moves is re-read whole before the write, because the save is authoritative
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
  families, long tail); it never emits one row per family. `info` findings are collapsed in the
  Attention lens, beside the failed steps.
- **Semantic (agent):** deferred. It reuses the query tools plus `report_finding`.
- **Cleanup runs on its own**: every finished cluster pass prunes the dead members and refreshes the
  programmatic findings, so the report never describes a membership that no longer exists. A write
  that fails fails the pass (it is returned, not swallowed): a half-pruned layer would make the next
  start rebuild the index.

## 8. Models, patience and audit

- **Two phases, two models.** `model_compile` (cheap) runs the document steps, tagging and
  vocabulary calls; `model_reason` (strong) answers queries and will run semantic lint, and the
  assistant reads its command with the compile model. A phase may also have its own `api_key` and
  `base_url` (`api_key_compile`/`base_url_compile`, `api_key_reason`/`base_url_reason`, or the
  matching `OPENAI_*_COMPILE`/`OPENAI_*_REASON` variables): a phase that declares none takes the
  shared pair — and that fallback is applied **after** the environment is read, so a key that lives
  only in `.env` reaches both phases. The cluster step uses no model.
- **The derivation keeps window hours.** `openai.blocked_windows` is a list of UTC ranges
  (`HH:MM-HH:MM`, start inclusive and end exclusive; `23:00-02:00` wraps midnight) in which no
  derivation calls the model — outline naming, distill, summary, source and wiki auto tags,
  vocabulary curation and retopic. Ask and the assistant are interactive reads and stay allowed, as
  do capture (fetch, extract, OCR) and the cluster step, which has no model. The scheduler states the
  rule twice: no such run is claimed or queued inside a window, and the guarded gateway refuses a
  call that reaches it anyway. A call in flight when a window opens is **cut at the boundary**; the
  run row says `aborted` with `deferred: the model window is closed until …` (not `failed`) and
  everything already derived is kept. One wake is armed for the opening and resumes the pump, the
  summary/tag backfills, the vocabulary check and the wiki pages whose tagging waited. The default
  is DeepSeek's `01:00-04:00` and `06:00-10:00`; an explicit empty list means around the clock. The
  Status note shows the opening in the reader's local time (the browser's zone); the ranges
  themselves stay UTC.
- **Think less where thinking buys nothing.** `reasoning_effort_compile` and
  `reasoning_effort_distill` default to `minimal`; `reasoning_effort_reason` too.
- **`reasoning_content` is echoed even when empty.** A thinking endpoint rejects a conversation
  whose assistant turns lost the key; the gateway restores it on every assistant message.
- **A transient model failure is retried** (timeout, 429, 5xx) with a doubling backoff; finished
  parts are kept and the source is requeued. A wrong key or a bad request is not retried.
- **The agent needs a key.** Without one in the compile phase — its own or the shared `api_key` —
  the app runs, distill answers `501` and `/status` reports `agent: off`. Ask is separate: it needs
  the reason phase's key only. The cluster step still works: it is deterministic.
- **Everything is recorded.** Every run writes a `runs` row; every tool call writes a `run_steps`
  row. Both append-only and untouched by `reindex`.
- **No silent writes.** Agent Markdown is rendered through the same codecs as any other write.
  Output that fails to decode is a recorded failure with the raw output retained.
