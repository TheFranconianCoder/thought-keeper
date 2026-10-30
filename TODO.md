# TODO

Open work and open questions, checked against the code (2026-10-22). Nothing here describes the
present: how the system works is in `AGENTS.md` and `docs/`.

## Superseded backlog (kept as history, not as work)

The old list was written against the **page layer** (`branch`, `concept`, tags on claims, generated
wiki pages). That layer is gone, and the items it asked for are largely resolved:

| Old item | Today |
| --- | --- |
| Merge branches / tags / concepts | **Done:** two curated vocabularies (`topics`, `wiki-tags`) with an alias table; `POST /vocabulary/curate` merges names as a rename. |
| Reuse vocabulary in prompts | **Done:** `list_topics` in distill, the tagger reads the tag aliases, curation sees the catalog. |
| Lint finding for near-duplicate names | **Done:** the vocabulary report aggregates collisions and near-duplicate families into findings. |
| Concepts without a page | **Gone with the page layer.** |

The previous file is in git history; the durable answer it proposed — *a curated vocabulary the
model must reuse, plus a merge action for whatever slips through* — is what the vocabulary verbs now
are.

## Verified open work

1. **The topic prompt contradicts the flat-topic rule.**
   `agents.VocabularyCurateSystem` still says "topics are hierarchical paths `<domain>/<topic>`",
   while the user message, `VocabularyNameRules`, the slug normaliser `NormalizeTopic` and every
   other document say a topic is flat. Only the user message is right; the system prompt should not
   offer a hierarchy. Low risk, but it is a contradiction an agent can follow.

2. **`vault.git.enabled` / `commit_on_write` are not implemented.** The app never runs git. Either
   build commit-on-write (the shape is in `config.example.yaml`) or drop the two knobs so no reader
   believes a setting does something.

3. **CLI verbs for the document steps.** `distill`, `reset` and `rebuild-trees` exist;
   `capture` and a single-source `outline` are API/UI-only. A CLI `capture --source HASH` is the
   obvious gap for headless operation.

4. **Trash retention and restore.** Deletions move to `.trash/`, but there is no restore verb and no
   retention policy; the trash grows until a human empties it.

5. **Human edits to atoms.** The app and the agent own atom files; there is no rule for what happens
   when a human edits one by hand and the claim is derived again. Decide: preserved, overwritten, or
   refused (a status/lock in frontmatter).

6. **TypeSpec.** `spec/main.tsp` is deferred until the API settles. The endpoint surface is stable
   enough to start, but `docs/api.md` must remain the reference while there is no generated spec.

7. **Semantic lint pass (agent).** Deferred. It reuses the query tools plus `report_finding`;
   programmatic lint covers provenance, standalone-ness, vocabulary drift, empty bodies and
   duplicate ids.

8. **MCP tools.** Out of scope for v1 (see the non-goals). Revisit only when the claim workflow is
   stable enough that an external tool surface would not become a second write path.

9. **Cluster count in the Job Queue.** Progress reports attach/new/remaining; whether the reader
    needs a per-cluster counter is open — a cluster has no name, so "37 clusters" is already all
    there is to say.
10. **Cluster centroid cache.** `attach` still walks every claim once per pass to learn the
    document frequencies and the centroid sums — the stream made that walk CHEAP (a page at a
    time, nothing retained), but a per-cluster cache of the centroid's raw (term, tf) sums in
    `clusters/<...>.meta`, invalidated by the existing `Signature` and the claim-set fingerprint,
    would make it O(free + touched records) instead of O(all claims). It changes the RECORD
    format, so it belongs in `docs/storage.md` before the code — and it must keep the arithmetic
    of the stream: the cached sums are corpus-independent tf, the IDF weighting stays at pass
    time.
11. **poppler over a path, not stdin.** `capture` reads the object whole and pipes it to
    `pdftotext`/`pdftocairo` on stdin — per OCR'd page the WHOLE file travels again (a 36 MB PDF
    is held for the entire extraction). Handing poppler the object path removes the parent buffer;
    it trades away "the bytes are the object" at the tool boundary and the `pdf.max_bytes` check
    then belongs where the path is opened.
12. **`Atoms.Scan` reads every claim file, serially.** `reindex` walks 172k atom files (13.6 s warm,
    32 s cold on the reference vault) to answer "what does the vault hold". Nothing on a request path
    uses it any more — deletes answer from the index — so it is a repair-time cost, not a save-time
    one. Streaming it through a small worker pool, or reading only each file's frontmatter, would
    turn a reindex from a hang into a repair.
13. **Source search and tag facets are still scans.** The three source lists (Sources, Bookmarks,
    Attention) are windowed and server-filtered, but `q` uses `instr` over the metadata columns
    (ASCII case folding only) and the tag cloud counts tags by reading two JSON columns. An FTS
    index for source metadata and a cached tag-count table would make both independent of vault
    size.

## Open design questions

- **Topic namespace stability.** Topics are free names; two runs can invent `caching` and
  `cache-strategies`. The curation/retopic verbs are the repair; a *naming lock* (ask once per
  document for the topic set, the way tags are curated) is the candidate prevention. Growth makes
  this worse, never better.
- **Recency is arrival, not publication.** `imported_at` is when a source entered the vault; a
  document can be years old. Capturing a publication date (HTML meta/JSON-LD, PDF metadata) would let
  "the newest articles" mean what it says, but it is a new extraction path and a new field.
- **Claim granularity.** "One claim per atom" is a judgement the model makes. Too fine and the
  Zettelkasten drowns; too coarse and provenance blurs. The audit bounds it from below, nothing
  bounds it from above.
- **Tree-builder heuristics for PDF.** The deterministic heuristic ships and is tested. Whether a
  model should correct tree *shape* stays open (it names the document title only).
- **OCR quality.** Languages, DPI, page limits and timeout are configurable; poppler + tesseract stay
  the default. The open part is measuring OCR harm, not adding engines.
- **Reading an authenticated page without the human's help.** Today a blocked URL is reported and the
  human attaches a saved page (`POST /sources/{hash}/attach`); the app never holds a credential. The
  three candidates, in the order they were weighed:
  - **Per-host headers/cookies** (`fetch.auth: [{host, headers}]`, env-expanded, redacted everywhere).
    Cheap, works for API-ish sites and long-lived tokens; cookies expire silently and an article
    behind a CSRF flow still fails. Would be opt-in, never the default path.
  - **A browser extension or bookmarklet** that posts the current page to the app. The app would need
    a token and an origin policy first — as it stands, any website could write into the vault.
  - **An archived copy** (Wayback) as a fallback for public paywalls and dead links. Only honest with
    the provenance recorded (`archived_from`), and useless for what was never public.
  Deliberately **not** candidates: a headless browser (the stack forbids CGO and it still cannot log in
  for you) and reading the browser's cookie database (encrypted per platform, silent credential use).
- **Paywall detection is a phrase list.** `internal/infrastructure/gateways/wall.go` recognizes the
  markers it knows; a site that gates its text without saying so still reaches the converter, and a
  teaser above `capture.min_text_chars` would be distilled. The honest fix is a per-site hint the
  human can set ("treat this host as paywalled"), not a longer list of phrases.
- **Feeds are XML-only.** RSS 2.0, RSS 1.0 (RDF) and Atom are read; **JSON Feed** is not, and a feed
  that paginates (`?page=2`) is read as the page it is. A feed source keeps the pages it once listed
  by design; whether a vault should offer "prune feed items older than N" is open — it would be a
  human action, never an automatic one.
- **The manual-addition precedence is per URL, not per host.** Adding `https://example.com/a` releases
  that page; a later sitemap refresh that no longer lists it will not delete it, but the sitemap's
  other pages stay owned. A "this site is mine, never own its URLs" rule is not implemented.

## Ideas

- Bookmarks / Favorites / Read Later
- Wiki / Notes Overlay / Flyover to scribble with links
- Wiki Pages as basis for LLM suggestions (mixed document)
- Wiki Folders (with Hierarchy); or with labels (original GMail-style)

## The assistant (open work)

1. **A command can be lost between windows.** The `command` event is a live frame: a window whose
   stream is reconnecting when a command is published misses it and does not follow. The record
   still exists and the Start lens lists it, but "the Pi navigated, the laptop did not" is the
   failure mode to close. The shape: a `since` read on reconnect (`GET /commands?limit=1` plus the
   `at` of the last frame this window applied), bounded to a short window so a cold start does not
   jump to an old command.
2. **Two dimensions have no column.** `host` (a URL source's domain) and `tag_scope` (human vs.
   auto) are not queryable: a command reads them as free text and a tag, respectively. A column
   plus a reindex backfill would make "sources from heise.de" and "only my own tags" exact.
3. **Cross-entity commands are one step, not a join.** "Claims from the sources I added last week"
   is not expressible: `claims` commands read claims, `sources` commands read sources. The join
   exists in the data (atoms cite a source hash); the DSL needs a `from_source` clause and a
   two-step resolution.
4. **The criteria are not editable in the UI.** The banner shows the reading; correcting it means
   typing a new command. An editable chip (drop "added ≤ 7 days", keep the rest) would make a bad
   parse a one-click fix and re-resolve the set.
5. **Voice.** The input adapter is still the only missing piece: stage 2 records an utterance in the
   PWA (secure context via the TLS proxy) and posts the transcript to the same `POST /commands`.
   Nothing in the backend changes. The iOS fallback is the keyboard's own dictation
   (`SpeechRecognition` does not exist on iOS); the assistant field must say so instead of failing
   silently.
6. **`assistant.max_items` cuts a set silently for the LIST.** The banner says "showing 100 of 340",
   but the windowed list's count is the set's size — 100, not 340 — so the scrollbar is the size of
   the record, not of the vault. That is honest for a materialized set, but a reader who expected
   "all 340" needs the number in the banner to be the thing they see; consider raising the cap or
   paging the resolution.
