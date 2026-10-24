# Thought Keeper — operations

Running the app, configuration, the CLI and the operating rules that keep the vault and the pipeline
consistent. The domain model is in `AGENTS.md`, the steps in `pipeline.md`, the UI in `pwa.md`.

## 1. Running the app

- **One binary, launched by the user.** `thought-keeper` (or `app`, or `serve`) brings up the
  in-process services (HTTP on loopback, the inbox watcher, the agent workers) and a **tray icon**.
- **The UI is the PWA.** On start the app hands its address to the operating system, preferring the
  installed PWA's own launcher (browser-written) over the plain URL handler (`app.smart_open`).
  `--no-open` only logs the address; `--headless` runs without a tray and without opening anything.
- **Single instance.** A second launch cannot take the vault flock; it reads the runtime endpoint,
  pings the running instance (`GET /api/v1/status`) and opens that instance's UI (exit `0`). The UI
  itself owns the one-window rule (a BroadcastChannel election).
- **Port discovery.** `server.addr` is fixed (default `127.0.0.1:9210`) or ephemeral
  (`127.0.0.1:0`). The running instance writes its address and a lock to a runtime file so the CLI
  finds it.
- **Loopback only, no token, no CORS.** Secrets come from the environment.
- **Graceful shutdown.** Quit finishes or aborts running runs, flushes the index and releases the
  vault lock. The tray offers *Open* / *Pause watcher* / *Quit*; autostart is opt-in.

## 2. Configuration

One YAML file, `--config PATH` (default `$XDG_CONFIG_HOME/thought-keeper/config.yaml`), with
`${ENV}` expansion. In development a `.env` is loaded automatically (cwd, next to `--config`,
`$XDG_CONFIG_HOME/thought-keeper/.env`) and **never overrides real environment variables**.

**`config.example.yaml` is the reference.** Every section carries comments; keep it and this doc in
sync when a knob changes. Sections:

| Section | Holds |
| --- | --- |
| `app` | `tray`, `autostart`, `smart_open`, `memory_limit_mb` (soft heap ceiling, 0 = none) |
| `server` | `addr` (loopback, fixed or `:0`) |
| `vault` | root, sources, inbox, atoms, wiki, clusters, asks, vocabulary, trash, `git.ignore_derived` |
| `index` | `path` (vault-root-relative), `favicons` |
| `openai` | base URL, key, `model_compile`, `model_reason`, reasoning efforts, retries, timeout, headers |
| `pipeline` | auto, auto_distill, debounce, run/cluster timeouts, parts per run, parallel parts/runs, auto_continue, reconcile interval/batch |
| `cluster` | the quality gates of the claim graph (no model) |
| `capture` | URL dedupe, refresh interval (default `168h` — weekly), `min_text_chars` |
| `fetch` | timeout, max bytes, user agent, `allow_private` (SSRF guard) |
| `pdf` | text tool, max bytes, max pages |
| `ocr` | enabled, rasterizer, tesseract, languages, dpi, per-page threshold, max pages, timeout |
| `lint` | `programmatic_on_compile` |

Models resolve **env → yaml → default**: `OPENAI_MODEL` sets both, `OPENAI_MODEL_COMPILE` /
`OPENAI_MODEL_REASON` override one; the same holds for the patience knobs
(`OPENAI_REASONING_EFFORT_DISTILL`, `OPENAI_TIMEOUT`, `OPENAI_RETRIES`, `OPENAI_RETRY_BACKOFF`).
`vault.git.enabled` and `commit_on_write` are **not implemented** — the app never runs git; only
`ignore_derived` is real.

## 3. CLI

One binary, two roles: launching it (or `app`) starts the desktop application; every content verb is
a **client of the running instance**. The instance is found through the runtime file or
`THOUGHT_KEEPER_URL`; `status` and `open` also accept `--url`.

| Command | stdout |
| --- | --- |
| `app [--headless] [--no-open] [--config PATH] [--log-level LEVEL]` | log lines (HTTP + watcher; tray unless headless) |
| `open [--url URL]` | opens the running instance's UI |
| `status` (alias `doctor`) `[--url URL] [--json]` | doctor report |
| `new-id` | one ULID |
| `add --path FILE \| --url URL [--bookmark]` | source hash |
| `attach --url URL \| --source HASH --file FILE` | the source's hash, title and size |
| `sources` (alias `list-sources`) `[--kind K] [--limit N]` | one source per line |
| `tree --source HASH` | the tree outline |
| `claim-topics` (alias `branches`) | claim topics with claim counts |
| `atoms [--topic T] [--limit N]` | one claim per line |
| `distill --source HASH [--reset]` | run id |
| `reset --source HASH` | the reset result (tree, claims, parts) |
| `run --id ULID [--json]` | one run (status, tokens, answer) |
| `search --query Q [--layer atoms\|sources] [--limit N]` | matches with snippets |
| `query --question Q [--wait]` | run id, or the cited answer with `--wait` |
| `cluster` | queues the cluster step |
| `lint [--wait] [--kind K]` | run id, or the findings summary with `--wait` |
| `reindex` | counts of indexed/added/removed |
| `rebuild-trees` | count of trees (re)built |
| `models [--config PATH]` | models the endpoint serves |
| `version` | version string |
| `help` | usage |

**Exit codes:** `0` on success, `1` on any error (usage, not found, conflict, rejected, connection,
server, agent). The message on stderr names the cause.

## 4. Task commands

```bash
task build             # CGO-free static binary incl. PWA
task web:install       # install the next PWA toolchain (npm)
task web:build         # build the next Svelte PWA into internal/web/dist
task web:dev           # Vite dev server with HMR; proxies /api
task web:check         # svelte-check + TypeScript
task web:test          # unit tests (SSE parser, source state)
task icons             # render app icons from web/icons/pictogram.svg
task run               # go run ./cmd/thought-keeper app --config config.yaml
task run:open          # run and hand the address to the OS
task run:headless      # no tray (CI, WSL)
task cross             # static binaries (CGO off)
task format:go         # gofumpt -w .
task lint:go           # golangci-lint run
task lint-fix:go       # go mod tidy + go get -u + golangci-lint --fix (upgrades dependencies)
task modernize         # go fix ./...
task unit-tests        # go test -short ./...
task integration-tests # httptest + :memory: SQLite + temp-dir vault
task check             # modernize → format → lint-fix → unit + integration → sqlc:verify → web:check
task sqlc:generate     # sqlc generate
task sqlc:verify       # sqlc vet + sqlc diff
task migrate:up|down|status   # DB=path/to/index.db
task audit             # syft + grype + grant
task hooks             # lefthook install
```

## 5. Debugging

- **Debugging lives in the terminal.** `app --log-level debug` logs every API request, every LLM
  turn, every agent tool call and each run's start/finish. Static requests are logged at debug; the
  client's status/run polling is kept out of the way.
- **Memory is a number, not a feeling.** `GET /api/v1/status` carries `memory` (`heap_mb` live,
  `heap_peak_mb` the high watermark since start sampled every 2 s, named by `peak_work` and
  `peak_at`, `sys_mb`, `num_gc`), the CLI `status` prints the same line, and every cluster pass logs
  its heap at the end. A peak that never rises is idle; a peak that names "the vocabulary curation"
  says which pass to look at — the number without the name starts an argument, the name ends it.
  `app.memory_limit_mb` sets the Go heap ceiling (`GOMEMLIMIT`): the runtime then collects sooner
  and returns memory to the OS on the way down. It is a SOFT target — set below a pass's real
  working set it only buys GC thrashing; 0 (default) leaves the runtime to itself.
- **A document run is bounded** by `pipeline.run_timeout`, the cluster pass by
  `pipeline.cluster_timeout`.
- **Per source**, `<hash>.log` is the story: steps, tool calls, OCR pages, dropped furniture.
- **What the app is doing is listed, not just counted.** `GET /pipeline` carries `jobs`: each source
  a document step holds, the cluster pass, every ask and lint, and what each run reports while it
  works. `running` means EXECUTING; a worker blocked by the gate is `waiting` with the reason — and
  the reason names the REAL holder (the cluster pass, the consistency job, a curation), never a
  guess. A step is marked executing BEFORE the status is published, and running rows come first.
- **The Running list shows EVERY piece of work**, including maintenance passes (reindex, reconcile,
  cleanup, rebuild outlines, URL refresh, curation).

## 6. The gate, scheduling and runs

- **One gate.** Document steps take the SHARED side of one `sync.RWMutex` (including the outline);
  the cluster step, a vocabulary curation, a reset, a delete and every synchronous vault writer take
  it EXCLUSIVELY. A waiting writer blocks new readers. **Ask is never gated.**
- **The barrier is enforced.** `capture` must be complete (or skipped) for every source before any
  outline starts; every outline before any distill; every distill before `cluster`. The rule is one
  function (`entities.StepSet.Ready`) and the scheduler (`App.pumpPipeline`); the workers re-check
  the barrier on the way in. A failure is a barrier, not an invitation to run the next step for the
  other sources.
- **Runs are parallel across operations.** Document runs, queries, lints and tag runs each have
  their own worker; within one operation runs are serialized.
- **A user-initiated run goes first.** Its own queue is looked at before automatic runs; queueing
  never blocks the caller.
- **The automatic re-derivation is bounded.** Progress is a NEW claim or LESS work left; after two
  consecutive runs without progress the app stops and says why.
- **A pump pass claims only what the workers can take** (`2 × parallel_runs + 2`). Claiming the
  whole backlog filled the run queue, blocked one goroutine per overflow and let the watchdog expire
  claims that were only waiting. The pump runs after every finished run.
- **A wait is per STEP, not per source.** "Queued for the outline" and "queued for the distill" are
  two different waits; one flag per source let the outline worker erase the distill's, and the
  watchdog read a live claim as stale (observed: 160 claims released at once).
- **The watchdog never releases a run that is executing.** A claim is stale when nothing is queued,
  no step is executing and it is older than a whole run budget.
- **A run row is closed when its claim is released.** The row is created before the queue hand-off;
  `StartRun.AbandonLeftovers` closes rows a previous process left behind.
- **The pump is coalesced.** `pumpPipeline` only *asks* for a pass; one worker runs it after a quiet
  moment (350 ms) and folds a burst into one pass. The pass reads the index for "which hashes exist"
  instead of a full detail read per hash and step — 154 links used to cost 154 passes of ~780 ms.
- **When the document steps run (automatic).** On add and on start. The start-up consistency check
  runs BEFORE the HTTP server starts and always logs its outcome (`vault consistency ok`, or the
  reason it rebuilt): a missing index is rebuilt; a cluster record without its row (or the reverse)
  triggers a reindex; a record naming a claim that is gone, or a membership that contradicts its
  record, does too; a step recorded `running` is cleared; a distill `ok` with no claims is open
  again. It reads ids, rows and memberships — never claim bodies.
- **Missing summaries are filled in the background.** The consistency check reports sources whose
  derivation is complete but whose `<hash>.summary.md` is gone; the app queues two per pass on the
  summary worker (a model call each), so a vault from before the summary existed drains gradually. A
  single source can be written immediately from its Summary tab. The walk measures the backlog; while
  the backfill runs, Status shows **missing / queued / running** (`summary` in `GET /status`), and a
  completed summary leaves the backlog at once.
- **Source auto tags are filled the same way.** A completely distilled source that was never tagged
  (`auto_tags_at` empty) is queued on the tag worker, two per pass; a source tagged with an empty
  answer is not asked again. The human tags are never touched by this pass.
- **A background job keeps the vault honest.** The consistency job walks the vault on
  `pipeline.reconcile_interval` (`pipeline.reconcile_batch` sources per pass) and checks that step
  records match disk; a missing artefact invalidates everything after it. It also asks step 4 for a
  pass when claims are unclustered. The walk runs in TWO phases: the CHECK is read-only and takes no
  gate (a check that queued behind a running document would hold the writer slot and stop every new
  reader); only when it found something does the real pass run, under the exclusive gate.

## 7. Vault writes and consistency

- **Two writers, one record — and never a shared temp file.** A source's `<hash>.meta` is written
  through a per-hash lock (read → change → write) and into a UNIQUE temp file that is then renamed.
  Writing into `<hash>.meta.tmp` made two concurrent writers publish each other's half-written
  bytes: a truncated record reads as "never recorded" and takes the source's identity with it. The
  same rule holds for objects. A record that lost its identity is repaired by the consistency job
  from the index (kind, URL, language, ownership) — never invented. The other direction too: a ROW
  that lost its owner (a snapshot write rebuilt the meta JSON without `origin`/`wiki_page`) is
  restored **from the record** in the same pass, and the source's own log says so. No source is
  derived again to repair it.
- **Content addressing and atomic writes** everywhere: temp file → `fsync` → `rename`.
- **Trash, not delete.** `DELETE /api/v1/sources/{hash}` moves the object to `.trash/`, drops every
  sidecar and removes any claim that cited only this source. The claims are found in the **vault**
  as well as in the index.
- **The index is a cache, and it never decides what is true.** `reindex` prunes orphaned rows and
  rebuilds clusters from their records. A deleted index or revision is rebuilt from the vault.
- **An index from another shape is reported, never rebuilt behind the user's back.**
- **The pipeline is COMPLETE, or nothing at the back hangs on it.** A claim whose every source is
  gone is trashed; a partial provenance is pruned and the file rewritten.
- **A write that cannot be recorded leaves nothing behind.** The file is written first, the index row
  second; if the row fails, the file is removed.
- **The four steps never overlap where they must not**, and **a reset never deletes a cluster**: the
  records are a cache, and the next pass prunes dead members.
- **Counts never lie.** A source's `atom_count` counts resolvable claims. A light that disagrees with
  its own numbers is amber.
- **UTC everywhere.** Stored as Unix milliseconds; frontmatter renders RFC 3339.

## 8. Network and URLs

- **URL sources are local snapshots.** A changed page is written **through the source that is
  already there** (one source per URL); its derivation is corrected, not repeated.
- **A save never fetches.** Writing a page (or adding a source) ends at the vault: links go to a
  queue.
- **Fetch guards.** Only http/https, a User-Agent, timeouts, max bytes, an SSRF guard
  (`fetch.allow_private`, off by default).
- **No crawling.** The agent fetches the URLs you give it, one at a time (plus refresh).
- **A URL is refreshed once a week.** `capture.refresh_interval: '168h'` is the cadence one SOURCE
  may be revisited (the worker looks for due sources every hour); empty means manual only. The cadence
  is stored per source (`url_checked_at`), so restarting the app neither postpones nor doubles a
  refresh. A manual pass — `POST /api/v1/refresh-urls` — ignores it and checks everything now; the
  **Refresh** button (or `POST /api/v1/sources/{hash}/refresh`) checks ONE source now. A
  source with a **human-supplied snapshot** is not fetched at all; a **blocked** page (a login, a
  paywall, a consent gate) is attempted again on the same cadence, because it may become readable.
- **A refresh keeps parents honest.** A sitemap is reconciled 1:1 (new pages claimed, pages it no
  longer lists removed with their claims); an RSS/Atom feed only ever extends (new items claimed,
  items that left kept). A page the human added by hand is never removed by either — it was released
  from its owner. A page whose snapshot came from the feed's copy keeps that copy while the page itself
  yields no readable text.
- **A confirmed delete finishes.** The delete detaches from the request context, so a browser that
  navigates away cannot leave a half-deleted feed or sitemap behind, and the atom lookup scans the
  vault once for the whole cascade instead of once per child. The request still WAITS for the gate —
  a delete rewrites sources and claims and must not race a running document step or the cluster pass
  — but the UI returns to the list immediately and drops the row optimistically; a failure reloads
  the list and brings it back.
- **A wall is reported, never read.** A login form or a paywall teaser is not the page: the capture
  light turns red with the reason, and outline/distill are skipped. The human's way in is
  `thought-keeper attach --url URL --file page.html|page.pdf` (or **Attach snapshot…** in the source
  page): the app does not authenticate and never reads your cookies.
- **A refresh never loses a source's icon.** A new snapshot carries the previous icon when the site
  cannot be fetched.

## 9. Index, search and SQLite

- **A small pool (4), not one connection.** WAL lets readers and the writer work at the same time;
  a single connection let one slow aggregate stall every write. An in-memory index keeps ONE
  connection.
- **`PRAGMA` on open**, per connection, in the DSN: `journal_mode=WAL`, `busy_timeout=5000`,
  `foreign_keys=ON`, `synchronous=NORMAL`.
- **FTS5 capability check.** If absent, `status` shows `search: degraded`, and search falls back to
  `LIKE`.
- **`reindex` never scores anything**: it is a file walk, not a model call.

## 10. The drop zone

Files land in `inbox/` and are hashed into the store by the watcher (or `POST /add`), then removed
from the inbox.

- **The drop zone survives an exhausted inotify budget.** `ENOSPC` from `inotify_add_watch` is the
  kernel's per-user WATCH budget (`fs.inotify.max_user_watches`, shared with editors and browsers),
  never the disk. When a watch is refused the inbox is **polled** (5 s, settled files only, one
  intake per file) and the watch is retried every 30 s, so the drop zone keeps working and returns to
  inotify by itself. `status` and the Status page say `inbox: polling — <reason>`.

## 11. Maintenance and recovery

Status → Vault offers every repair as a **button that says what it will do** before it is pressed:

| Action | What it does |
| --- | --- |
| `POST /lint` | programmatic health check → findings |
| `POST /cleanup` | prune dead cluster members, resolve claim links |
| `POST /reconcile` | check step records against disk, invalidate what is missing, restore a row's lost owner from its record |
| `POST /reindex` | rebuild the index from the vault |
| `POST /rebuild-trees` | rebuild every outline from captured sources |
| `POST /atoms/reorganize` | move claims into `atoms/<ab>/<cd>/<id>.md` |
| `POST /atoms/collapse` | keep one copy of each duplicate |
| `POST /fix-urls` | repair URL identity from the snapshot's own frontmatter |
| `POST /urls/migrate` | move old URL sources onto their URL address |
| `POST /refresh-urls` | check every known URL now (attached snapshots are left alone) |
| `POST /vocabulary/curate` · `/retopic` | merge names · re-derive claim topics |

**Backup.** The real backup is the vault: `sources/` + `atoms/` + `wiki/` + `clusters/` (or git
history). The index is rebuildable, so it does not need a backup. For a live copy, use SQLite's
`VACUUM INTO` rather than copying `index.db` while the app runs.
