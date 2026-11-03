# Thought Keeper — HTTP API

Base: `http://127.0.0.1:<port>` on **loopback only** — the PWA (same origin) and the CLI are the
only clients, so there is no token and no CORS. The port comes from `server.addr` (default
`127.0.0.1:9210`, or an ephemeral port) and is published to a runtime file so the CLI finds the
running instance.

**Atoms, clusters, wiki pages and runs by ULID; sources by SHA-256 (a wiki page's source also has
the stable `WikiHash`); topics by name.** Long operations return a run id.

## Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/status` | Doctor report: vault, index (`index.outdated`, `index.bookmarks`, `index.failed`, `index.findings`), agent, tools (`tools.pdf`, `tools.ocr`, `tools.browser` — the local rendering engine; `tools.browser_tool` names the binary), inbox, search, memory (`heap_mb`, `heap_peak_mb`, `sys_mb`, `num_gc`, `peak_work`, `peak_at`), the summary backfill (`summary.missing`, `summary.queued`, `summary.running`, `summary.at`), the index rebuild (`repair.active`, `repair.phase`, `repair.done`, `repair.total`, `repair.since`), and the configured output languages (`language.ui`, `language.claims`, `language.summary`) |
| `GET` | `/api/v1/pipeline` | Global step status: documents or clusters, queued counts, `jobs`, and the index rebuild (`repair`) while one runs |
| `POST` | `/api/v1/cluster` | Queue the cluster step by hand (waits for the gate) |
| `POST` | `/api/v1/reconcile` | Run the consistency job by hand |
| `POST` | `/api/v1/reindex` | Rebuild index + clusters from the vault |
| `POST` | `/api/v1/rebuild-trees` | Rebuild every outline from captured sources |
| `GET` | `/api/v1/events` | Server-sent events (see below) |
| `GET` | `/api/v1/sources` | List sources: `kind, q, tag, attached, bookmarks, failed, stale, favorite, readlater, interest, roots, since, command, limit, offset`. `q` matches the title, the original name, the URL, the tags and the **hash** (a partial paste finds the row); `stale=1` keeps the derivations that stopped with work left (the Attention lens's second source list). The filters are applied in SQL; `count`/`total` is the **filtered** total (the windowed list's scroll height), not the page length. `readlater=1` is the OPEN queue and is ordered by `read_later_at` ASC (oldest entry first); `interest=<slug>` narrows it to one rule's share; `roots=1` keeps only the sources nothing else declared (a sitemap's pages and a feed's or listing's articles are children, not roots); `since` is an absolute instant (RFC3339) and keeps the sources that entered the vault at or after it — the Start card's week, computed by the caller, never a duration the server would reinterpret. `readlater=1` also answers `queue: {open, from_interests}`: the queue's own split, so a banner needs no tag cloud for two numbers |
| `GET` | `/api/v1/sources/facets` | The Sources facets: `{tags:[{tag,count}], attached, favorites, read_later, read_later_interests}`. Vault-wide, not facts about the loaded window |
| `GET` | `/api/v1/sources/resolve` | Resolve the URLs a rendered document links to: `url` repeated once per address (a destination may contain a comma, so no joined list). Answers `{links:[{url,hash}]}` with only the URLs that ARE sources, matched by address key (fragment and trailing slash ignored). A URL the vault does not hold is absent, so its link keeps opening the web |
| `GET` | `/api/v1/sources/{hash}` | A source, its step lights, counts, `tags`/`auto_tags`, the reader's marks (`favorite_at`, `read_later_at`, `read_later_done_at`, `read_later_manual`, `read_later_via`, and the derived open flag `read_later`) and `sidecars` |
| `GET` | `/api/v1/sources/{hash}/tree` | The tree outline (`has_text` marks parts) |
| `GET` | `/api/v1/sources/{hash}/nodes/{node}` | A node's text |
| `GET` | `/api/v1/sources/{hash}/atoms` | The claims extracted from a source |
| `GET` | `/api/v1/sources/{hash}/raw` | Bytes inline (`?which=render`, `.meta`, `.outline`, `.summary.md`, `.log`) |
| `GET` | `/api/v1/sources/{hash}/children` | The sources this one owns: a sitemap's pages, a wiki page's linked URLs |
| `GET` | `/api/v1/sources/{hash}/summary` | The source's overview as Markdown (404 until one exists) |
| `POST` | `/api/v1/sources/{hash}/summary` | Write (or rewrite) the overview; answers 202 with the run id |
| `GET` | `/api/v1/sources/{hash}/log` | The source's own log |
| `GET` | `/api/v1/sources/{hash}/favicon` | The site's stored icon |
| `POST` | `/api/v1/sources/{hash}/capture` | Step 1 again for one source (the outline follows) |
| `POST` | `/api/v1/sources/{hash}/refresh` | Refresh ONE URL source now (a feed or a sitemap re-expands its list, a page is re-fetched). The configured cadence is ignored; attached snapshots are not fetched (use `/refetch` for those). A feed copy is replaced only by a readable page that carries at least as much text; a wall, a failure or a poorer page keeps it. A changed page's derivation is rebuilt. Answers `{checked, changed, failed, blocked, attached, discovered, removed, detail}` |
| `POST` | `/api/v1/sources/{hash}/attach` | Give a blocked URL source or a bookmark a snapshot the human supplied (multipart `file`: `.html`, `.pdf`, `.md`, `.txt`). A sitemap or a feed is refused, and so is an `.epub` (a book is a source of its own). Answers 200 with the source. The URL stays the address; the new file replaces an existing snapshot and resets the old derivation |
| `POST` | `/api/v1/sources/{hash}/refetch` | Try the URL again for a source whose snapshot is not a normal page: attached by hand, or a collected link (a bookmark). A readable page replaces it and clears the marker (a changed text also resets the old derivation) and a collected link becomes an indexed source; a wall, a thin page or a failure keeps what is there. Answers `{replaced, blocked, failed, detail}` |
| `POST` | `/api/v1/sources/{hash}/rebuild-tree` | Rebuild this source's outline |
| `POST` | `/api/v1/sources/{hash}/distill` | Document steps for a source (`{reset:true}` starts over) |
| `PUT` | `/api/v1/sources/{hash}/tags` | Replace the source's HUMAN tags (`{tags:[…]}`); normalized and deduplicated. Auto tags are derived, never written here |
| `PUT` / `DELETE` | `/api/v1/sources/{hash}/favorite` | The reader's star: set / clear. Answers the source |
| `PUT` / `DELETE` | `/api/v1/sources/{hash}/read-later` | Put the source in the open queue (a finished entry comes back at the END) / finish it. Answers the source |
| `GET` | `/api/v1/sources/{hash}/interests` | Which of the reader's rules this source fits, with the evidence (`{interests:[{slug,name,band,terms,topics,queued,rejected,enabled}]}`). Computed on demand, never cached |
| `POST` | `/api/v1/sources/{hash}/reset` | Drop the derivation and rebuild the tree |
| `DELETE` | `/api/v1/sources/{hash}` | Delete a source (object → trash, orphaned claims removed). A dependent wiki/sitemap page is refused with `dependent_source`; deleting its owner removes it. |
| `POST` | `/api/v1/add` | Add a source (`{path}` \| `{url}` \| multipart). A URL that is a sitemap or an RSS/Atom feed expands into a parent source; a manual URL is released from any owner it had. `{url, listing: true}` turns a page that declares articles into a listing (and walks its pagination); `listing: false` never does. Absent, the page is stored as it is and the answer carries `listing_candidates`: the reader is asked once |
| `POST` | `/api/v1/add-urls` | Queue URLs for background intake |
| `POST` | `/api/v1/refresh-urls` | Check every known URL NOW (the configured weekly page/sitemap and daily feed cadences are ignored; attached snapshots are left alone). A changed page's old derivation is rebuilt. Answers `{checked, changed, failed, blocked, attached, discovered, removed}`: `blocked` counts pages that answered with a wall, `attached` counts human-supplied snapshots (never fetched), `discovered` the new pages a sitemap/feed declared, `removed` the pages a sitemap no longer lists |
| `GET` | `/api/v1/wiki` | Wiki pages and their tag cloud |
| `GET` | `/api/v1/wiki/{id}` | One wiki page (Markdown body included) |
| `POST` | `/api/v1/wiki` | Create a wiki page (writes its source) |
| `PUT` | `/api/v1/wiki/{id}` | Edit a wiki page (writes it, then reprocesses and prunes removed links in the background) |
| `PUT` / `DELETE` | `/api/v1/wiki/{id}/favorite` | The star on a page (its frontmatter is the truth) |
| `DELETE` | `/api/v1/wiki/{id}` | Delete a wiki page and its source representation |
| `GET` | `/api/v1/interests` | The reader's standing rules, each with the number of OPEN queue entries it holds |
| `POST` | `/api/v1/interests` | Create a rule (`{name, terms, exclude?, kinds?, enabled?, queue_existing?}`). `queue_existing` is the explicit backfill and is off by default |
| `POST` | `/api/v1/interests/preview` | Evaluate a draft against the vault (`{name, terms, exclude?, kinds?}`). Answers `{total, strong, weak, examples}`; writes nothing |
| `PUT` | `/api/v1/interests/{slug}` | Update a rule (the slug is stable; the name may change) |
| `DELETE` | `/api/v1/interests/{slug}` | Delete a rule. Queue entries it created stay, their `via` names a rule that no longer exists |
| `PUT` | `/api/v1/interests/{slug}/sources/{hash}` | The reader's correction for one pair: `{accepted:true}` clears a rejection and queues the source when it fits; `{accepted:false}` records "not this interest" and removes an entry the rule queued (a manual one stays) |
| `GET` | `/api/v1/topics/claims` | Claim topics with claim counts |
| `GET` | `/api/v1/atoms` | List claims: `topic, status, command, limit, offset` |
| `GET` | `/api/v1/atoms/{id}` | One claim (provenance, `clusters`, `cluster_count`) |
| `GET` | `/api/v1/atoms/{id}/related` | Two separate lists: `peers` (same cluster) and `same_topic` |
| `GET` | `/api/v1/atoms/{id}/cluster` | The same peer answer, named for what it is |
| `GET` | `/api/v1/atoms/duplicates` | Claims that exist more than once |
| `POST` | `/api/v1/atoms/collapse` | Keep one copy of each duplicate |
| `GET` | `/api/v1/vocabulary` | The health of the name vocabularies (`topics`, `wiki-tags`, `source-tags`) |
| `POST` | `/api/v1/vocabulary/curate` | Curate the vocabularies (observes, asks the model, renames) |
| `POST` | `/api/v1/vocabulary/retopic` | Re-derive claim topics from the claims themselves |
| `POST` | `/api/v1/vocabulary/rebranch` | Alias of `retopic` (legacy name) |
| `GET` | `/api/v1/search` | Lexical search: `q, layer=atoms\|sources, limit` (claims use FTS5; sources match the list's title/URL/tag fields) |
| `GET` | `/api/v1/asks` | Saved Ask conversations |
| `GET` | `/api/v1/asks/{id}` | One saved question and answer |
| `DELETE` | `/api/v1/asks/{id}` | Move a saved Ask to `.trash/asks/` |
| `POST` | `/api/v1/query` | Ask the claims `{question, source?}` → `202 {run_id}` |
| `POST` | `/api/v1/commands` | **The assistant.** `{text, context?}` (`context: {view, id, timezone?}` names the surface the request was made from, so "this claim" resolves and "yesterday" uses the reader's calendar) → `201` with the command record (`id, lens, url, criteria` incl. `criteria.window`/`criteria.terms`, `total, vault, scanned, took_ms, items`). Synchronous: the reading plus one bounded completion. An utterance that names no lens and no criterion answers `422 unparsed` — never "the whole vault". Publishes the `command` event (`running` → `done`) so every open window follows it |
| `GET` | `/api/v1/commands` | The command history, newest first (`limit, offset`) |
| `GET` | `/api/v1/commands/{id}` | One command, its criteria and its materialized set |
| `DELETE` | `/api/v1/commands/{id}` | Move one command to `.trash/commands/` |
| `POST` | `/api/v1/lint` | Run lint → `202 {run_id}` |
| `GET` | `/api/v1/lint/findings` | Lint findings |
| `GET` | `/api/v1/runs/{id}` | Run status, tokens, steps, answer, question |

There is no list-all-runs endpoint; the UI follows the `run` events and the pipeline's `jobs`. A run
is read by id.

## Asynchronous work

- **Long operations are asynchronous.** `POST /sources/{hash}/distill`, `POST /query`, `POST /lint`
  and the vocabulary verbs record a run and return `202 {run_id}`; the client follows
  `GET /runs/{id}` or the event stream.
- **Fast ones are synchronous.** `POST /add`, `POST /reindex`, `POST /rebuild-trees`,
  `POST /reconcile`, `POST /sources/{hash}/refetch` and the source verbs do their local work in the
  request.
  `POST /cluster` is the exception: it is queued and returns `202` (an empty run id means nothing
  was owed). Adding a source ends at the vault: URLs are queued, never fetched on the request path.
  A URL that answers with a sitemap is stored as a parent source; its pages are claimed and fetched
  in the background, and their `DELETE` is refused while the sitemap owns them.
- **The CLI's `--wait`** is the client's poll loop, not a second server route.
- **Gating is server-side.** Document steps take the shared side of the pipeline gate; the cluster
  step and every synchronous vault rewrite take it exclusively (attaching or refetching a snapshot
  may reset claims and tree, so both are exclusive). The router applies the same gate to
  the write endpoints.
- **A query answer is saved automatically** to `asks/<run-id>.md`; there is no explicit filing step.
- **A query may be scoped to one source.** `source` is that source's SHA-256 hash; the run records it
  as its target, the agent may only read that source and its claims, and the saved ask keeps the
  hash, so asking the same question again stays scoped. A malformed hash is `invalid_hash`; an
  unknown source is `not_found`.
- A saved ask carries automatic `tags`: the topics of the claims its answer cites, the tags of the
  sources it cites when no claim is cited, or the meaningful words of the question. Tags label the
  query list and tag cloud; they never filter retrieval.
- **A wiki save returns as soon as the page and its source representation are written.** The old
  derivation is cleared and the source is pumped in the background, and a background `PruneWikiLinks`
  pass reconciles the link sources the page owns with what it declares; the save never waits for the
  vault gate or a delete.

## Events

One SSE stream (`GET /api/v1/events`) pushes events by type:

`run` · `source` · `wiki` · `clusters` · `findings` · `urls` · `pipeline` · `progress` · `log` ·
`vocabulary` · `interests` · `command`

`interests` is a state event: a rule was saved or deleted, or a match queued a source. The rule list
and its open counts are re-read; the queue rows are source rows and ride the `source` scope.

State events coalesce per type and are never dropped; `progress` coalesces per source; `log` is a
bounded live tail. The PWA patches only the containers each event names. See `pwa.md`.

`command` is the one event that carries an ACTION rather than a container name:
`{status:"running", text}` when a command starts, `{status:"done", id, url, lens, total, text}`
when it has been resolved, `{status:"failed", error}` when it could not be read. Every open window
follows the address once per command id — that is what makes the assistant a remote control, and
why the event is keyed by the command's id instead of being coalesced by type.

A command's `criteria.window` is `{spec, from, to, timezone}`: `spec` is the reader's phrase family
(`yesterday`, `last-7d`, `2026-10-01..2026-10-31`), `from`/`to` are the instants this run resolved
it to, and `timezone` is the zone that decided the calendar. `criteria.terms` is the vocabulary
bridge, `mapped` is what the names and mentions contributed, and `took_ms` is persisted with the
record. A question that asks which items exist is a list (`resolution: filter` or `mapped`), not
Ask; only a prose question gets `resolution: ask`.

## Error format

```json
{ "error": { "code": "unsupported_source", "message": "only .md, .pdf, .txt and .epub sources are supported" } }
```

Map sentinel errors from `internal/types/errors.go` to the wire once (`internal/api/payloads`). No
stack traces. API misses return the same JSON error; non-API paths go to the embedded PWA.

`422 unparsed` is the assistant's "I heard you, but this is not a command I can run": the request
named no lens and no criterion, so answering with an unfiltered list would be a lie. The UI offers
examples instead.

`503 repairing` is the start's read-only window: the index is being rebuilt, reads answer from the
vault, and every mutation is refused with a `Retry-After`. It is not a client mistake — the same
request works once `repair.active` is false again.

## Not API

- **No `/notes*`, `/tags`, `/compile`.** The page layer is gone; wiki pages are user-authored files.
- **No cluster endpoint besides the peer read.** A cluster is not a destination and has no page.
- **No global search page:** search is an endpoint (FTS5) plus the claim palette in the PWA.
