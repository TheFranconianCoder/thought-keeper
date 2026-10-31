# Thought Keeper — retrieval (vectorless)

No embeddings and no vector database. Retrieval is reasoning over **hierarchies, indexes, the
association layer and lexical search**. `similarity ≠ relevance`.

## 1. Substrates

The agent picks the substrate that fits the question:

| Substrate | What it is | Tool / endpoint |
| --- | --- | --- |
| Claim topics | every FLAT claim topic with its claim count | `list_topics` |
| Lexical search | FTS5 over claims; the Sources layer matches the list fields (title, original name, URL, tags), answerable in any vault language | `search`, `GET /search` |
| Claim proximity | the same cluster, and separately the same topic | `GET /atoms/{id}/related` |
| Source recency | the newest sources by vault arrival, paged, with a short overview excerpt | `list_sources`, `read_source_summary` |
| Tree index (PageIndex) | per-source hierarchical tree | `read_source_node` (distill), `GET /sources/{hash}/tree` |
| Interest match | a standing rule's terms read against a source's topics, claim texts, title and labels | the read-later queue (`?readlater=1`) |

**The tree is built once per source.** Retrieval mimics a human using a table of contents: read the
outline, reason about which part is relevant, open the part, cite it. An outline stores structure
and references into the readable text, never a second copy of the text.

**Ask can narrow every substrate to ONE source.** Asking from a source page scopes the run to that
source's hash: the lexical ladder runs with a provenance restriction, the source recency list and
the topic catalogue are not offered, and a claim read from another source is refused. The source is
then the only evidence the answer may use.

## 2. Search is multilingual and morphology-aware

- Every source records a **language hint** in its `<hash>.meta` (ISO 639-1, or ISO 639-3 when no
  639-1 exists) and in the index `meta` JSON. Intake/capture write it; the consistency pass
  backfills a missing hint by reading the source's readable text. A long document is classified
  from up to eight short windows spread over its text, so a big book's vocabulary size cannot
  drown the detector and a code-heavy opening cannot decide the language alone.
- Search uses the **set of languages present in the vault** to choose suffixes and stems, plus a
  language-independent **prefix family**: `chao*` covers `Chaos` and `chaotisch`; `map*` covers
  `map`/`maps`.
- **Exact terms are tried first.** The expansion is a recall fallback, never a fuzzy replacement.
- The language hint **tunes** search; it never filters a result.
- Ask includes the vault's language set in its prompt, so a question can be answered against sources
  in another language.

## 3. Interest matching is a standing lexical read

An **interest** is a named rule that watches NEW sources. Its match is the same lexical substrate as
search, applied to one source at a time and turned into a BAND, never a score the reader has to
trust:

| Signal | Weight |
| --- | --- |
| a claim **topic** names the term (`async` in `async-runtime`) | strong |
| an **auto tag** or a human tag names the term | strong |
| the **title** carries the term | strong |
| the term appears in **claim texts** (two or more claims) | strong (a theme) |
| the term appears in exactly one claim | weak (`maybe`) |
| a term in the interest's **exclude** list appears anywhere | veto |

- **Folding and word families are shared with search** (`core/textmatch`): "caching" and "cache",
  "Chaos" and "chaotisch", "Häuser" and "Haus". The matcher is intentionally STRICTER than the
  search's recall ladder — it does not use the short prefix family, because a three-letter prefix of
  "rust" would also catch "Russia".
- **Only a strong match queues a source** (the read-later queue); a weak one is shown in the source's
  rail as a suggestion. One mention is easy to produce by accident; two are a theme.
- **It looks forward only.** A source that arrived before the rule existed is never queued by it; the
  editor's explicit "queue the existing matches" checkbox is the one backfill.
- **The evidence is the answer.** Every match names the terms and what they hit (a topic, N claims, a
  label, the title), and "not this interest" records a veto in the rule's file. Nothing is written
  into the source's tags or record.
- **No model and no embeddings run in the loop.** The incremental pass is a few string comparisons
  over the claims the source already has; the editor's preview reads the vault through FTS5 once.
- **Why a rule never lives in a cache:** the matches are a function of the current rule and the
  current claims. Computing them on demand means an edit can never leave a stale membership behind —
  and `reindex` has nothing to rebuild.

## 4. What is not a part

A **navigation block** is not a part: a short block of Markdown links and list markers is dropped by
the outline builder and never counts as a part. A page with no heading keeps the page-per-node
shape; the book's own front/back matter is dropped by `core/tree/matter.go` (exact title match, body
never consulted).

## 5. Citations are mandatory and traceable

- A query answer references the **claims** it used as `[[claim:<id>|short claim text]]` links and a
  **source's overview** as `[[source:<hash>|<source title>]]`. A claim citation supports a precise
  statement; a source citation supports a statement taken from that source's overview.
- Every cited claim or source is numbered and repeated in the final `Sources:` bibliography; that
  block is omitted when the answer cites nothing. Prose carries `([1](#cite-1))`, and the `Sources:`
  block at the end is a numbered list (`1. [short claim text](#/claims/<id>)` or
  `1. [source title](#/sources/<hash>)`). The round parentheses are prose punctuation, not part of
  the link; the claim's wording lives in the bibliography, never in the middle of the answer
  sentence.
- A recency question is answered from **vault arrival** (`imported_at`), never from a publication
  date: the vault does not store one, so the answer says "added to the vault".
- The reference behaves like a footnote in the UI: clicking `(1)` scrolls to bibliography entry 1
  and flashes it. Only the claim text or source title in the bibliography opens its page. A small
  `↩` on the entry returns to the reference.
- The answer itself is a complete, grammatical sentence or paragraph, written **entirely in the
  language of the question's prose**. Proper names and source titles keep their original script; a
  title in another language never switches the answer. The citation is appended as a reference after
  the supported statement; it is never used as a word or as the object the sentence is missing.
- Everything the answer cites is repeated in the final `Sources:` bibliography; that heading
  disappears when there is nothing to cite.
- Pages, chapters and clusters are not part of Ask: it answers from claims and source overviews.
- Every answer is saved as an Ask record (`asks/<run-id>.md`), so an answer stays traceable to its
  run, its tool steps and the claims and sources it cited.

## 6. A claim's neighbours

The Claims detail view calls `/atoms/{id}/related`, which returns **two disjoint lists**: the claim's
**cluster peers** and the other claims under the **same topic**. They answer different questions —
"what does this claim travel with?" and "what else is filed here?" — so the view shows them as two
sections instead of one ranking. A cluster is many-to-many: a claim can travel with several groups,
and the peer list is the only place that becomes visible. `read_atom` returns the same peers (bounded to
eight, id + claim + topic) so the model can open one if it needs the body.

## 7. The in-app finders

- The **search palette** (top bar, `⌘K` / `Ctrl K`) searches claims first and can switch to sources.
  The Sources layer uses the **same text fields as the Sources list** — title, original name, URL,
  human tags and auto tags — ordered by vault arrival. It does not scan the outline node table, so a
  search never waits on the whole node index; it matches metadata, not the overview prose.
- **Claims** also has its local FTS5 finder in the list.
- There is no separate search page: the palette is the one global finder, and claims remain
  searchable where they live.

## 8. Scale

FTS5 and hashed sharding handle tens of thousands of objects. When navigation feels lossy, the
vectorless answer is a better hierarchy — never a vector store.
