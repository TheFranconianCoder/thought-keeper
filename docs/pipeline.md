# Thought Keeper — the pipeline

The four steps, their state, how they are scheduled and how a source's progress is shown. The
domain reference is `AGENTS.md`; the files each step writes are described in `storage.md`; the
domain entities and the index schema in `data-model.md`.

## 1. The model

Four directories, three layers and one association:

- `sources/` — immutable, content-addressed inputs. One source is one object plus its sidecars.
- `wiki/` — user-authored Markdown. The one prose artifact a human owns; its source representation
  is derived and reprocessed like any other source.
- `atoms/` — one claim per file. The claims are the truth. A claim carries a flat `topic`, a `type`,
  a `status` and its provenance (source hash + node id).
- `clusters/` — titleless groups of related claims. A cluster is a **cache**: it can be rebuilt from
  the claims, has no prose, no title and no tags, and is never a destination in the UI.

A cluster is visible only as a claim's **peers** (Claims detail view). There is no cluster lens.

## 2. The steps

| # | Step | What it does | Scope |
| - | ---- | ------------ | ----- |
| 1 | **capture** | Take the source in and make it readable locally: Markdown and text are read, a PDF gets its text layer (or OCR), a URL is fetched. The result is a **local, static snapshot**. | per source |
| 2 | **outline** | Build the hierarchical outline of that snapshot: how the document is structured, part by part. | per source |
| 3 | **distill** | Turn the outline into **atoms**: one claim per atom, with its flat topic, type and the source node it came from. | per source |
| 4 | **cluster** | Group the claims into **titleless clusters**. | over the whole vault |

`capture → outline → distill` are the **document steps**: they run for ONE source, one after the
other, and they never touch the association layer. `cluster` is the **vault-level step**: it is the
one place where claims from different sources meet, and it may only run while **no document step
runs** — in both directions. **Ask** (a query) is never gated and runs in parallel: up to
`pipeline.parallel_queries` questions are answered at the same time.

### 2.1 capture (per source)

- Markdown, text and a URL snapshot: the bytes are already readable; intake stores them and capture
  is done. The title is determined here (declared title → first heading → file name → hash).
- A URL whose response is a sitemap (`<urlset>` or `<sitemapindex>`) is stored as a parent source
  whose snapshot is the page list; its URLs are claimed with the sitemap as owner and fetched in the
  background. Outline, distill and the association step are skipped for the sitemap itself — its
  children carry the prose and the claims.
- A URL whose response is an **RSS/Atom feed** (RSS 2.0, RSS 1.0/RDF, Atom) is stored the same way:
  a parent source whose snapshot is the **item list**, and every item's page is a URL source owned by
  the feed (`origin: rss`, `parent_hash`). Feed sources are marked `rss: true`; outline, distill and
  the association step are skipped for them.
- **A feed or a sitemap is a LIST, not a document.** Its capture is the fetch that produced the list;
  outline, distill and the association step do not apply and are recorded (and shown) `skipped` —
  grey lights, never pending. The document pipeline never queues a step for a list: it has no parts
  and no claims of its own. The source page offers **Refresh** in place of Capture/Outline/Distill/
  Reset, and the API refuses a document run with `conflict`. Its children carry the prose.
- **A feed that carries the post solves the JavaScript page.** Many feeds include the article itself
  (`content:encoded`, Atom `content`). The item's copy becomes the page's snapshot — marked
  `from_feed` with the feed it came from — so a page that only resolves in a browser is still
  readable, without a browser. The page is still tried weekly: a fetch that yields a readable page
  replaces the copy and clears the marker; while it yields nothing, the copy stays.
- **A sitemap is complete; a feed is a window.** A refresh is a 1:1 reconciliation for a sitemap and
  for a wiki page's links: a page that disappeared from the list is removed from Sources. A feed only
  ever EXTENDS: a page that disappeared from it stays — and only deleting the feed removes its pages.
  This is why the two are different origins and not one "parent" kind.
- **A manual addition wins.** A URL the human adds by hand is INDEPENDENT: if a wiki page, a sitemap or
  a feed had claimed it first, the addition releases it (the record's origin is cleared), so deleting
  the owner no longer deletes it. A URL that a page or feed declares later never takes an existing
  source over.
- **Two URLs that differ only by a trailing slash are one source.** Existence checks (wiki save,
  sitemap/feed claim, re-add) compare `entities.URLKey` — path trailing slash and fragment ignored. The
  stored address stays the URL as it was given.
- **A URL that does not answer with itself is `blocked`, not a failure and never content.** A login
  wall (`401`/`403`, a redirect to a sign-in, a password form), a paywall (a teaser instead of the
  article) and a consent gate are named as such (`login_required`, `paywalled`,
  `consent_required`) and recorded where the reader sees them: a red capture light with the reason,
  the `wall` key in the snapshot's frontmatter and in the source's metadata, a line in the source's
  log. Outline and distill are recorded **skipped** — for a URL source the intake IS the capture, so
  without that the outline would build a tree out of a login form and distill would write claims
  about the gate. A page that was readable once **keeps its snapshot** when a later fetch is walled:
  the wall describes the fetch, not the bytes.
- **The human may attach the page.** The app does not authenticate and does not read your cookies;
  the human is already logged in, in their browser. Saving the page (HTML or PDF) and attaching it to
  the URL source (`POST /api/v1/sources/{hash}/attach`, `thought-keeper attach`) stores it as that
  source's snapshot at the same address and re-arms outline and distill. The record marks the source
  `attached` (with the file's name and SHA-256), and the automatic refresh **never overwrites an
  attached snapshot** — a login page is not a correction of the article. The source page can
  **replace** the file (a new attach; the old derivation is reset) or **try the URL again**
  (`POST /sources/{hash}/refetch`): a readable page replaces the snapshot and clears the marker (a
  changed text also resets the old derivation), a wall or a failure keeps the attachment.
- **A page or sitemap is refreshed once a week, a feed once a day** (`capture.refresh_interval:
  '168h'`, `capture.feed_refresh_interval: '24h'`), manually at any time. The cadence belongs to the
  source (`url_checked_at`), not to the process. Attached sources are not fetched by the cadence; an
  explicit refetch may try one, and a walled normal source is attempted again on the same cadence,
  because it may become readable.
- PDF: extract the text layer with poppler; OCR a page that has none (`pdftocairo` + `tesseract`).
  Write the readable rendering to `sources/<ab>/<cd>/<hash>.capture.md`, with one
  `<!--part …-->` marker per part.
- **The extractor takes the object whole** (`[]byte`, not a reader): every format needs all of it
  anyway — a PDF goes to poppler on stdin, text is split into parts — and a reader only ever
  invited a second private buffer. The Markdown parser accumulates part bodies in a builder, not
  by appending per line, so one document costs its text once, not its text times its lines.
- A failed capture blocks the outline; a scanned PDF with no OCR available is `unsupported_source`,
  never silently empty.
- The step is recorded in `sources/<ab>/<cd>/<hash>.meta` and appended to the source's own log.

### 2.2 outline (per source)

- **Reads the rendering; never extracts.**
- A bookmark and a blocked URL have nothing to outline: the step is recorded `skipped` and a rebuild
  is a no-op (a tree built from a login page would look like knowledge about the page).
- `tree.ParseRendering` produces exactly the parts the bytes produced; the tree builder shapes them
  into nodes and writes `sources/<ab>/<cd>/<hash>.outline`.
- The sidecar stores structure and **references** (byte ranges + text hash), never the text.
- The model is asked for the **document title** only; node titles come from headings and node
  summaries are deterministic previews.
- Navigation blocks (a short breadcrumb/menu of links and list markers) are not parts.
- A rebuild is compared with the previous tree: a node that kept its id and text keeps its claims; a
  changed/new/gone node loses them and is re-armed (`incomplete/tree_changed`).

### 2.3 distill (per source, per part)

- One part = one text-bearing node. `pipeline.parallel_parts` parts run at once,
  `pipeline.parts_per_run` bounds one run.
- The agent answers with the claims of that part as JSON (`topic`, `claim`, `body`, `type`,
  `nodes`). The app writes the atoms; a claim that names no node inherits the part.
- The agent then audits the part: every statement not covered by a claim is written, or `COMPLETE`
  is answered.
- Only `done` parts are skipped next time. A cut-off answer is continued; still cut off →
  `incomplete/truncated`. No claim and no audited completion → `incomplete/no_claims`. An audited
  zero-claim part that is document metadata or a link list → `done/source_relative`.
- An `incomplete` part is derived again after its own atoms are dropped.
- A reset starts at step 2: claims, tree, part records and log go; the source stays.
  `distill {reset:true}` also re-runs capture.
- The tool contract and prompt rules are in `agent.md`.

**After a complete distill** the source earns a **summary**: a run of its own (`op=summary`) writes
`<hash>.summary.md` from the claims (and the outline's shape), in the configured summary language
(`language.summary`, default English), and the Summary tab shows it. It is
not a step: it never gates the pipeline, and a slow model only delays the overview. A source whose
distill is not `ok` is refused — a summary of a half-derived source would describe a moving target —
and a RESET is not a completion either: it removes the old summary and schedules no new run. A
source that correctly produced **no claims** (a cover, a link list) gets a one-line honest file
without a model call, so it stops being owed. While the consistency job fills a backlog, Status shows
how many summaries are **missing**, **queued** and **running**; a completed one leaves the backlog
immediately.

**A complete distill also earns a source's AUTO TAGS**: the agent reads the claims and proposes 3–6
coarse labels for the Sources filter, kept apart from the human's. The run lands on the tag worker;
the consistency job backfills sources that were never tagged (`auto_tags_at` in the record/index is
the marker), a few per pass. A reset clears the derived labels; the next complete distill writes new
ones. The human tags are records and survive both.

### 2.4 cluster (over the whole vault)

Deterministic, no model. Inputs: the claims no cluster owns yet and the existing memberships.

1. **Prune** members whose claim vanished; remove a cluster that loses all of them.
2. **Attach** a free claim to an existing cluster when the cosine between its content vector and the
   cluster centroid is at least `cluster.attach_min_score`. This is the incremental step: without
   titles, content similarity is the only evidence that a new claim belongs to a group.
3. **Graph** the remaining free claims: sparse IDF-weighted neighbours (`neighbor_k`,
   `min_claim_score`), merge gates (`merge_min_internal`, `merge_min_gap`,
   `cross_topic_min_edges`), split at the weakest bridges (`split_min_core_size`). A group that
   still spans more than `topic_span_limit` topics is cut by its flat topics; a group above
   `max_cluster_claims` is split by similarity.
4. **Overlap**: a claim with real edges into several cores may belong to several clusters
   (`overlap_max`), but only with at least `overlap_min_edges` edges and centroid similarity
   (`attach_min_score`). The size cap is enforced after overlap.
5. **Write** one `clusters/<ab>/<cd>/<id>.meta` per cluster and its `cluster_atoms` membership.
6. **Settle**: refresh the findings (the cleanup pass), so a finished pass leaves no dead
   membership.

The pass runs only while no document step runs, and it is the only writer of the association layer.
It has no model: a broad group is split deterministically, not reviewed by a prompt.

**What the pass costs, and why it is shaped this way.** Attaching one free claim scores it against
every existing cluster, so the pass walks the whole claim corpus — but as a STREAM: one id-ordered
page walk folds the document frequencies, every cluster's centroid sum and the free set itself out
of the claim texts, keeping only what the pass truly carries (the free claims and compact per-cluster
term entries). A page of claims is tokenized, folded in and dropped; the vault is never materialised
as objects. The term vectors are interned (one string per TERM, not per claim) and held as flat
sorted arrays, not a hash map per claim: the map shape cost ~3.8 kB per claim and, with 170k claims,
~900 MB for a pass that had 135 claims to place. The centroid sums of existing clusters are
construction sites: once the norms and the posted top terms exist, the full maps are gone. The same
claims and settings must always produce the same memberships — the golden fingerprint in
`cluster_golden_internal_test.go` is that promise, and the pass logs its heap peak at the end. The
accumulation order is FIXED: term ids are interned in sorted order and every group sum is a sorted
sparse vector, never a hash map, so a run is bit-reproducible — not merely "the same decisions most
of the time".

**There is no model in this step**: no naming, no review, no title, no tags. A cluster is never
split by size alone except the hard `max_cluster_claims` cap, which is a guard against a failed
merge, not a target. **Identity is the claim, never a title.**

## 3. Where the step state lives

- **`sources/<ab>/<cd>/<hash>.capture.md`** is what step 1 leaves behind for a source whose bytes
  are not readable (a PDF). Only such a source has one.
- **`sources/<ab>/<cd>/<hash>.meta`** (JSON) records what one source IS (`kind`, `url`, `bookmark`,
  `language`, `origin`, `wiki_page`, `original_name`, title, favicon/content references, `attached`
  with the file it was attached from) and what it has been through: capture/outline/distill (state,
  timestamp, detail, error) and the part records. It is the **truth**. The cluster step is not in
  the file: it runs over the whole vault, so per source it is derived from memberships.
  The language hint is detected at intake/capture when the text allows it (long texts are sampled
  in windows across the document); the outline fills a missing hint from the document, and the
  consistency job backfills legacy or code-heavy sources from their outline titles and summaries.
- **`clusters/<ab>/<cd>/<id>.meta`** is a cluster record: `id`, `atoms`, `signature`, `created`,
  `updated`. It is a **cache** (re-derivable from the claims), not a record of history.
- **`distill_parts`** in the index mirrors the part records, so the list view can answer "which
  steps is this document through?" without opening a file per row.
- **`sources.steps`** is the mirror of the file; `reindex` rebuilds it from the files.

**The order is enforced, and a failure blocks what follows.** A step may only run when every step
before it is `ok` (or `skipped`, for a bookmark, a blocked URL or a feed/sitemap). The rule is one
function (`entities.StepSet.Ready`) and one lock (the gate). The scheduler is `App.pumpPipeline`; the
workers re-check the barrier on the way in. A failure is a barrier, not an invitation to run the
next step for the other sources.

**The further forward something changes, the more loses its validity.** A new capture invalidates
the outline, the distill and the cluster memberships that drew on its atoms. Steps 1–3 can run for
different sources at the same time — but only up to the **global step**: `capture` must be complete
(or skipped) for every source before any outline starts; every outline before any distill; every
distill before `cluster`.

## 4. Status: four lights and two bars

Each source carries the state of the four steps as a traffic light:

| Light | Meaning |
| --- | --- |
| **empty** (dashed) | not run yet |
| **blue** | running right now |
| **green** | ok — done, and the result on disk is the one derived |
| **red** | failed; it blocks the next step |
| **grey** | does not apply (a bookmark, a feed/sitemap, or a URL whose capture is blocked by a wall) |
| **amber** | started but not finished: a part is still open, the derivation produced no claim, or something it depends on changed (`stale`) — and, for the derived fourth light, only some of the claims reached the association layer |

A **feed** or a **sitemap** shows capture green and the other three lights grey: it is a list of
sources, not a document, and there is nothing to outline, distil or associate in the list itself.
Its state label names it (`feed · the items are sources`, `sitemap · the pages are sources`), so a
finished list is never mistaken for a document stuck after step 1.

Green means done and nothing else. Two bars sit **left** of the lights, stacked:

- the **parts** bar (`parts_done` of `parts_total`) is how much of the document has been distilled;
- the **claims** bar (claims in a cluster of all claims) is how much of what was distilled has
  reached the association layer.

A source is *finished* exactly when both are at 100%; then it shows no bar. Below them, always, the
totals: `N parts · M claims`.

The fourth light is derived per source: **amber only when some of its claims are in a cluster and
some are not**; empty when none is (the step has not started); green when all are — and **green for
a source that produced no claim at all** once its document steps are through. A link collection or
a document of pure metadata has nothing to associate: the association layer cannot group a claim
that does not exist. A `source_relative` part is metadata (or a link list): its done record counts
for the bar but never starts the distill light.

The **status label** describes the derivation: `complete` only when every part is done and no claim
is unclustered; everything else is `pending distill`, `partial · …`, `complete · no claims`,
`bookmark`, `feed · the items are sources`, `sitemap · the pages are sources`,
`link failed · …`, `blocked · login required` or `queued`. A light that disagrees with its own
numbers is amber.

## 5. Steering

- **Per source:** `POST /api/v1/add`, `POST /api/v1/sources/{hash}/capture`,
  `POST /api/v1/sources/{hash}/rebuild-tree`, `POST /api/v1/sources/{hash}/distill`,
  `POST /api/v1/sources/{hash}/reset`, and `POST /api/v1/sources/{hash}/refresh` for a URL (a feed
  or a sitemap re-expands its list, a page is re-fetched; the cadence is ignored).
- **Globally:** `POST /api/v1/cluster` (queued by hand; it waits for the gate),
  `POST /api/v1/lint` and `POST /api/v1/query` (read-only, never gated, answered by a pool of
  `pipeline.parallel_queries` workers).
- **Vault housekeeping (Status → Vault):** the repairs listed in `operations.md`. Every one is a
  BUTTON that says what it will do before it is pressed.
- Every run names its step (`runs.step`).

## 6. Jobs, gate and automation

- **One gate.** Document steps take the shared side of one `sync.RWMutex`; the cluster step, a
  vocabulary curation, a reset, a delete and every synchronous vault writer take it exclusively. Ask
  is never gated: a question runs on its own worker, beside anything.
- **A wait is per STEP, not per source.** "Queued for the outline" and "queued for the distill" are
  different waits; one flag per source let the outline worker erase the distill's and made the
  watchdog release live claims.
- **A pump pass claims only what the workers can take** (`2 × parallel_runs + 2`). Claiming the
  backlog blocked a goroutine per overflow and let the watchdog expire claims that were only
  waiting. The pump runs after every finished run.
- **A user-initiated run goes first**, on its own queue. Queueing never blocks.
- **Automatic continuation is bounded by progress**: a new claim or less work left. Two runs without
  progress stop it and say why.
- **The watchdog never releases a run that is executing.** A claim is stale only when nothing is
  queued, no step is executing and it is older than a whole run budget.
- **A run row is closed when its claim is released**, so the audit cannot keep a `running` row for
  a claim that died in the queue.
- **The reconcile job** checks records against disk (missing outline/render invalidates the
  derivation) and asks step 4 for a pass when claims are unclustered. Its check is read-only and
  gate-free; only a real repair takes the exclusive gate.
- **A wiki save's leftovers are pruned in the background.** The save writes the page and its source
  representation and returns; `PruneWikiLinks` then re-reads the page under the exclusive gate and
  removes the link sources it no longer declares (plus an older representation from before the stable
  `WikiHash`). The pass runs after EVERY save: it is a no-op when the page and its sources agree, and
  it heals a save whose cleanup was lost. A delete inside the save would make every removed link wait
  for a vault walk.
- **What the app is doing is listed, not just counted.** `GET /pipeline` carries `jobs` with
  `running` (executing) and `waiting` (blocked by the gate, with the real holder named).

## 7. Invariants

1. Sources are immutable; a correction is a new source (URL snapshots and wiki representations are
   the deliberate exceptions, refreshed in place).
2. One claim per atom; the file is written before the row.
3. Provenance is resolved before a write; an unknown source node is an error, never a skipped row.
4. A cluster is a cache: it can always be rebuilt from the claims.
5. The cluster step has no model; it is deterministic and incremental.
6. The four steps never overlap where they must not; Ask is never gated.
7. The index is a cache; a lost or outdated index is reported, and rebuilt from the vault.
8. Trash, not delete.
