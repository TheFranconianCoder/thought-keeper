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
| `GET` | `/api/v1/status` | Doctor report: vault, index (`index.outdated`, `index.bookmarks`, `index.failed`, `index.findings`), agent, pdf/ocr, inbox, search, memory (`heap_mb`, `heap_peak_mb`, `sys_mb`, `num_gc`, `peak_work`, `peak_at`), and the summary backfill (`summary.missing`, `summary.queued`, `summary.running`, `summary.at`) |
| `GET` | `/api/v1/pipeline` | Global step status: documents or clusters, queued counts, `jobs` |
| `POST` | `/api/v1/cluster` | Queue the cluster step by hand (waits for the gate) |
| `POST` | `/api/v1/reconcile` | Run the consistency job by hand |
| `POST` | `/api/v1/reindex` | Rebuild index + clusters from the vault |
| `POST` | `/api/v1/rebuild-trees` | Rebuild every outline from captured sources |
| `GET` | `/api/v1/events` | Server-sent events (see below) |
| `GET` | `/api/v1/sources` | List sources: `kind, limit, offset` |
| `GET` | `/api/v1/sources/{hash}` | A source, its step lights, counts, `tags`/`auto_tags` and `sidecars` |
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
| `POST` | `/api/v1/sources/{hash}/refresh` | Refresh ONE URL source now (a feed or a sitemap re-expands its list, a page is re-fetched). The weekly cadence is ignored; attached snapshots are not fetched (use `/refetch` for those). Answers `{checked, changed, failed, blocked, attached, discovered, removed, detail}` |
| `POST` | `/api/v1/sources/{hash}/attach` | Give a blocked URL source or a bookmark a snapshot the human supplied (multipart `file`: `.html`, `.pdf`, `.md`, `.txt`). A sitemap or a feed is refused. Answers 200 with the source. The URL stays the address; the new file replaces an existing snapshot and resets the old derivation |
| `POST` | `/api/v1/sources/{hash}/refetch` | Try the URL again even when the snapshot was attached by hand. A readable page replaces the attachment and clears the marker (a changed text also resets the old derivation); a wall/failure keeps it. Answers `{replaced, blocked, failed, detail}` |
| `POST` | `/api/v1/sources/{hash}/rebuild-tree` | Rebuild this source's outline |
| `POST` | `/api/v1/sources/{hash}/distill` | Document steps for a source (`{reset:true}` starts over) |
| `PUT` | `/api/v1/sources/{hash}/tags` | Replace the source's HUMAN tags (`{tags:[…]}`); normalized and deduplicated. Auto tags are derived, never written here |
| `POST` | `/api/v1/sources/{hash}/reset` | Drop the derivation and rebuild the tree |
| `DELETE` | `/api/v1/sources/{hash}` | Delete a source (object → trash, orphaned claims removed). A dependent wiki/sitemap page is refused with `dependent_source`; deleting its owner removes it. |
| `POST` | `/api/v1/add` | Add a source (`{path}` \| `{url}` \| multipart). A URL that is a sitemap or an RSS/Atom feed expands into a parent source; a manual URL is released from any owner it had |
| `POST` | `/api/v1/add-urls` | Queue URLs for background intake |
| `POST` | `/api/v1/refresh-urls` | Check every known URL NOW (the weekly cadence is ignored; attached snapshots are left alone). Answers `{checked, changed, failed, blocked, attached, discovered, removed}`: `blocked` counts pages that answered with a wall, `attached` counts human-supplied snapshots (never fetched), `discovered` the new pages a sitemap/feed declared, `removed` the pages a sitemap no longer lists |
| `GET` | `/api/v1/urls/addresses` | Preview the URL address migration (old addresses still in the vault) |
| `POST` | `/api/v1/urls/migrate` | Move URL sources onto their URL's address |
| `POST` | `/api/v1/fix-urls` | Repair index rows that lost "this is a URL" (from the snapshot's frontmatter) |
| `GET` | `/api/v1/wiki` | Wiki pages and their tag cloud |
| `GET` | `/api/v1/wiki/{id}` | One wiki page (Markdown body included) |
| `POST` | `/api/v1/wiki` | Create a wiki page (writes its source) |
| `PUT` | `/api/v1/wiki/{id}` | Edit a wiki page (writes it, then reprocesses and prunes removed links in the background) |
| `DELETE` | `/api/v1/wiki/{id}` | Delete a wiki page and its source representation |
| `GET` | `/api/v1/topics/claims` | Claim topics with claim counts |
| `GET` | `/api/v1/atoms` | List claims: `topic, status, limit, offset` |
| `GET` | `/api/v1/atoms/{id}` | One claim (provenance, `clusters`, `cluster_count`) |
| `GET` | `/api/v1/atoms/{id}/related` | Two separate lists: `peers` (same cluster) and `same_topic` |
| `GET` | `/api/v1/atoms/{id}/cluster` | The same peer answer, named for what it is |
| `GET` | `/api/v1/atoms/layout` | Legacy claim folders and orphaned records |
| `GET` | `/api/v1/atoms/duplicates` | Claims that exist more than once |
| `POST` | `/api/v1/atoms/collapse` | Keep one copy of each duplicate |
| `POST` | `/api/v1/atoms/reorganize` | Move claims to `atoms/<ab>/<cd>/<id>.md` |
| `POST` | `/api/v1/atoms/quote-code` | One-time repair: wrap the bracket syntax a claim body quotes from its source in inline code (`{scanned, repaired, fragments, skipped}`) |
| `GET` | `/api/v1/vocabulary` | The health of the name vocabularies (`topics`, `wiki-tags`, `source-tags`) |
| `POST` | `/api/v1/vocabulary/curate` | Curate the vocabularies (observes, asks the model, renames) |
| `POST` | `/api/v1/vocabulary/retopic` | Re-derive claim topics from the claims themselves |
| `POST` | `/api/v1/vocabulary/rebranch` | Alias of `retopic` (legacy name) |
| `GET` | `/api/v1/search` | FTS5: `q, layer=atoms\|sources, limit` |
| `GET` | `/api/v1/asks` | Saved Ask conversations |
| `GET` | `/api/v1/asks/{id}` | One saved question and answer |
| `DELETE` | `/api/v1/asks/{id}` | Move a saved Ask to `.trash/asks/` |
| `POST` | `/api/v1/query` | Ask the claims `{question}` → `202 {run_id}` |
| `POST` | `/api/v1/lint` | Run lint → `202 {run_id}` |
| `GET` | `/api/v1/lint/findings` | Lint findings |
| `POST` | `/api/v1/cleanup` | Prune dead cluster members and refresh the programmatic findings |
| `GET` | `/api/v1/runs/{id}` | Run status, tokens, steps, answer, question |

There is no list-all-runs endpoint; the UI follows the `run` events and the pipeline's `jobs`. A run
is read by id.

## Asynchronous work

- **Long operations are asynchronous.** `POST /sources/{hash}/distill`, `POST /query`, `POST /lint`
  and the vocabulary verbs record a run and return `202 {run_id}`; the client follows
  `GET /runs/{id}` or the event stream.
- **Fast ones are synchronous.** `POST /add`, `POST /reindex`, `POST /rebuild-trees`,
  `POST /reconcile`, `POST /cleanup`, `POST /atoms/quote-code`, `POST /sources/{hash}/refetch` and
  the source verbs do their local work in the request.
  `POST /cluster` is the exception: it is queued and returns `202` (an empty run id means nothing
  was owed). Adding a source ends at the vault: URLs are queued, never fetched on the request path.
  A URL that answers with a sitemap is stored as a parent source; its pages are claimed and fetched
  in the background, and their `DELETE` is refused while the sitemap owns them.
- **The CLI's `--wait`** is the client's poll loop, not a second server route.
- **Gating is server-side.** Document steps take the shared side of the pipeline gate; the cluster
  step and every synchronous vault rewrite take it exclusively (attaching or refetching a snapshot
  may reset claims and tree, so both are exclusive). The router applies the same gate to
  the write endpoints.- **A query answer is saved automatically** to `asks/<run-id>.md`; there is no explicit filing step.
- A saved ask carries automatic `tags`: the topics of the claims its answer cites, or the meaningful
  words of the question when it cites none. Tags label the query list and tag cloud; they never filter
  retrieval.
- **A wiki save returns as soon as the page and its source representation are written.** The old
  derivation is cleared and the source is pumped in the background, and a background `PruneWikiLinks`
  pass reconciles the link sources the page owns with what it declares; the save never waits for the
  vault gate or a delete.

## Events

One SSE stream (`GET /api/v1/events`) pushes events by type:

`run` · `source` · `wiki` · `clusters` · `findings` · `urls` · `pipeline` · `progress` · `log` ·
`vocabulary`

State events coalesce per type and are never dropped; `progress` coalesces per source; `log` is a
bounded live tail. The PWA patches only the containers each event names. See `pwa.md`.

## Error format

```json
{ "error": { "code": "unsupported_source", "message": "only .md, .pdf and .txt sources are supported" } }
```

Map sentinel errors from `internal/types/errors.go` to the wire once (`internal/api/payloads`). No
stack traces. API misses return the same JSON error; non-API paths go to the embedded PWA.

## Not API

- **No `/notes*`, `/tags`, `/compile`.** The page layer is gone; wiki pages are user-authored files.
- **No cluster endpoint besides the peer read.** A cluster is not a destination and has no page.
- **No global search page:** search is an endpoint (FTS5) plus the claim palette in the PWA.
