# Thought Keeper

A local knowledge base that follows the **LLM-Wiki pattern** with an **atom layer**: immutable raw
sources (Markdown, PDF, TXT, URL) are distilled by an agent into **atoms** (one claim per note — the
Zettelkasten and the source of truth) and grouped into **titleless clusters** (the association
layer). Retrieval is **vectorless** — hierarchical trees, the association layer and lexical search,
no embeddings.

It is a **desktop application**: one Go binary that serves a PWA from an embedded, loopback-only
HTTP service and lives in the system tray. The human authors wiki pages, curates sources and asks
questions; the agent writes and maintains the claims.

## The documentation map

| Document | Owns |
| --- | --- |
| **`AGENTS.md`** (this file) | orientation: positioning, non-goals, ubiquitous language, the pipeline in brief, structure, working agreements |
| **`ARCHITECTURE.md`** | architecture: layers, dependency rule, naming, quality gates, test tiers |
| **`docs/pipeline.md`** | the four steps, step state, status lights, jobs, gate and automation |
| **`docs/storage.md`** | the vault layout, sidecars, source/atom/wiki/cluster conventions, frontmatter |
| **`docs/data-model.md`** | entities, identifiers, the SQLite schema, derived values, reindex |
| **`docs/retrieval.md`** | vectorless retrieval, multilingual search, citations, neighbours |
| **`docs/agent.md`** | the agent: operations, tools, prompts, part outcomes, models, audit |
| **`docs/api.md`** | the HTTP API, async work, events, error format |
| **`docs/pwa.md`** | the UI: lenses, routes, SSE state, empty states, assets |
| **`docs/operations.md`** | running the app, config, CLI, tasks, operating rules, maintenance, backup |
| **`TODO.md`** | open work and open questions — nothing here describes the present |

The rule: **a decision changes the document that owns it, before the code.** If a fact appears in
two places, one of them is wrong.

## Current state

Phases 1–8 are implemented. The binary starts as a desktop app: tray (`github.com/gogpu/systray`,
pure Go), loopback Chi service, `go:embed` PWA, vault flock, SQLite index + goose migrations incl.
FTS5, and a `status` report. The source store is content-addressed (SHA-256, sharded, deduped), the
inbox is watched (with a polling fallback), and Markdown/text/PDF/URL sources run through the
document steps including poppler + tesseract OCR and the PageIndex tree builder. Claims are atom
files with a flat topic and node-level provenance; clusters are deterministic and titleless. The
cluster step walks the claim corpus as a page-by-page stream and holds its term vectors as flat
interned arrays (not a map per claim), so a pass over a 170k-claim vault costs a stream-sized
working set instead of a gigabyte; `status` reports live and peak heap, with the pass that caused
the peak. Query,
lexical search and programmatic lint are in place. A sitemap URL expands into page sources owned by
the sitemap; wiki-page and sitemap children cannot be deleted apart from their owner.

The PWA is **event-driven** (SSE) and **claim-first**: a calm, Notion-flavored reader whose navigation
is a finite set of lenses — **Start, Sources, Wiki, Bookmarks, Claims, Ask, Attention, Status** — in a
sidebar on the wide layout and in a **footer navigation** when the window is narrow (no drawer, no menu
button), with an Attention badge and a connection mark on Status. **Start** is the front door: an Ask
box, Continue (the last opened details, local memory),
Needs you, the newest sources and one vault-pulse line — never the newest-claims wall. A **wiki
page** is user-authored Markdown with tags; saving it writes a source representation and runs the
document pipeline from capture. The app also tags a page and a source
(auto tags, kept apart from the human's tags) and curates the three name vocabularies (`topics`,
`wiki-tags`, `source-tags`). Search is language-aware, and the interface and the content it writes
have their own languages (`language.ui`, `language.claims`, `language.summary`; every one defaults
to English).
**Ask** puts a question in flight directly in the saved-query list, with its status, and runs
questions in parallel, so the reader never waits on a spinner to ask the next one; a saved query's
row carries the same actions as a source row (open, ask again, delete). A source page's **Ask**
action scopes the composer to that one source — the reference sits under the input, and the answer
may use nothing else. The **source lists are
windowed**: filters and paging are server-side, only the visible range (± overscan) is read, and the
filtered count is the scroll height. A cluster is
**not** a lens: it has no title and no page, and it is visible only as the **peers** of a claim.

## Positioning

- Studio slogan: "Computer Science as Industrial Craftsmanship".
- Public one-liner (studio-page): `An automatic Zettelkasten over the claims of your sources`
- Classification: **a local desktop knowledge base — not a RAG lookup and not a server.** Sources
  are read once and distilled into atomic claims; the claims are associated with each other, and
  the connections are as valuable as the claims (Memex's associative trails).
- Studio state: `available`.
- Studio artifacts:
    - Entry: `../../studio-page/src/data/projects.ts` (`id: 'thought-keeper'`)
    - Pictogram source: `../../studio-page/design/pictograms/thought-keeper.svg`
    - Rendered asset: studio-page `src/assets/zhjaxnt7ndtlpitn.jpeg`
- The studio-page description is updated in a separate, later step.

### Non-goals

- **No embeddings and no vector database.** Retrieval is reasoning over trees, indexes, the
  association layer and lexical search. `similarity ≠ relevance`.
- **Only three source kinds: Markdown, PDF, TXT — plus URLs.** No `.docx`, no images, no archives,
  no audio/video.
- **No credentials and no browser automation.** The app never stores a login, reads your browser's
  cookies or drives a headless browser. A URL behind a login or a paywall is reported as **blocked**,
  and the human may attach the page they can see (HTML or PDF); the URL stays the source's address.
- **No silent OCR.** Local OCR is supported; if it is unavailable and a PDF has no text layer, the
  source is flagged `unsupported_source`, never silently empty.
- **No rendered claim pages.** Clusters are associations, not documents: no cluster page, no cluster
  title, no cluster tag, never a destination. **Wiki pages are the deliberate exception**: they are
  user-authored Markdown with a title and tags.
- **No human authoring of CLAIMS.** The agent writes and maintains claims; the human may author
  wiki pages, curate sources and ask questions. API writing exists for tooling, not as the claim
  workflow.
- **Not a classic server.** Loopback only, no multi-user, no cloud, no sync in v1.
- **No crawling.** The agent fetches the URLs you give it, one at a time — or every page a sitemap
  you explicitly added declares. It never discovers links on its own.
- **No external MCP servers in v1.**
- **No silent writes.** Every agent mutation goes through the same use cases as any other write.

## Ubiquitous language

**One word per thing, everywhere** — in the code, the log, the wire and the UI.

| Term | What it is | Where it lives |
| --- | --- | --- |
| **source** | An immutable input: a file, a PDF, a text file, or a URL snapshot. Addressed by its `hash`. A wiki page's representation is the one in-place exception (stable `WikiHash`). | `sources/<ab>/<cd>/<hash>.<ext>` |
| **hash** | A source's identity: the SHA-256 of its bytes (for a URL: of the URL). | the file name |
| **object** | A source's bytes in the store. | `sources/` |
| **wiki page** | User-authored Markdown with a title, tags and a stable ULID. Saving it writes/updates its source representation and runs the document pipeline from capture. | `wiki/<ab>/<cd>/<id>.md` |
| **snapshot** | A URL source's object: the page as it was fetched — or as the human attached it. | `sources/<hash>.md` |
| **wall** | Why a fetch did not return the page: a login, a paywall, a consent gate (`login_required`, `paywalled`, `consent_required`). Reported with the reason, never read, never distilled. | the capture step record + `.meta` |
| **attached** | A URL snapshot the human supplied from their browser because the page is walled. It is part of the source's identity; the automatic refresh never replaces it, but the human may replace the file or try the URL again explicitly. | the source's record + `.meta` |
| **language** | A source's detected ISO 639-1 hint (ISO 639-3 when no 639-1 exists), stored in `<hash>.meta` and mirrored into the index. It tunes search, never filters a result. | `<hash>.meta` |
| **sitemap** | A URL source whose snapshot is a page list; it owns the page sources it declared (`origin: sitemap`, `parent_hash`). A sitemap is COMPLETE: a page that left it is removed. | `<hash>.meta` |
| **feed** | An RSS/Atom URL source whose snapshot is its item list; it owns the page sources its items point at (`origin: rss`, `parent_hash`). A feed is a WINDOW: a page that left it is kept, and only deleting the feed removes its pages. When the feed carries a post, that copy becomes the page's snapshot (`from_feed`) until a readable page replaces it. | `<hash>.meta` |
| **part** | ONE text-bearing node of a document: the unit the distill step derives. | the outline + `distill_parts` |
| **node** / **node id** | A place in a document's outline; the ADDRESS a claim cites (`0.1`, `2.3`). | `source_nodes` |
| **outline** | A document's structure: titles, summaries and REFERENCES into the readable text. | `sources/<hash>.outline` |
| **atom** | One claim as a FILE. | `atoms/<ab>/<cd>/<id>.md` |
| **claim** | What an atom says: one statement with provenance. "Atom" is the file, "claim" the sentence. | the atom's `claim` |
| **cluster** | A titleless group of related claims: the association the fourth step derives. It is a CACHE. | `clusters/<ab>/<cd>/<id>.meta` |
| **topic** | The FLAT subject of one claim (`caching`, `llm-agents`). A claim has exactly one. | the claim's `topic` |
| **vocabulary** | The canonical names of a namespace, what they absorbed (the alias table), and the **broad tokens** learned from this vault for near-duplicate detection. Three namespaces: `topics`, `wiki-tags` and `source-tags`. | `vocabulary/topics.md`, `vocabulary/wiki-tags.md`, `vocabulary/source-tags.md` |
| **capture / outline / distill / cluster** | The four steps, in order. The first three are DOCUMENT steps (one source); cluster is the VAULT-LEVEL step (the whole vault). | `.meta` records |
| **run** | One execution of the pipeline, a query, a lint, a tag or a vocabulary pass. | `runs` |
| **job** | One piece of work in the queue: what it is, what it is about, whether it runs or waits. | Status → **Job Queue** |
| **gate** | The lock that keeps the steps apart: document steps share it, the cluster step (and a rewrite) takes it alone. | `sync.RWMutex` |
| **owed** | Work that is due but not started (a claim no cluster owns yet). | the Job Queue |
| **finding** | One result of a health check (lint). | `lint_findings` |
| **reconcile / reindex / cleanup** | The repairs: check records against the vault; rebuild the index from the vault; prune dead cluster members. | Status → Vault |

**There is no claim page, no note and no dirty marker.** A cluster has no prose and no name, so
there is nothing to render or mark stale. A claim is read through the Claims lens. A **wiki page**
is a separate, user-authored artifact: it has a title and tags because a human wrote it, and it is
backed by a source representation so its prose follows the same capture → outline → distill path.

## The pipeline

Four steps, strictly separated, in this order: **capture → outline → distill → cluster**. The first
three are document steps (one source, per source) and never touch the association layer; `cluster`
is the vault-level step and may only run while no document step runs — in both directions. **Ask is
never gated**, and queries run **in parallel** (`pipeline.parallel_queries`): a question waiting for
a slow model does not hold the next one. A step may only run when every earlier step is `ok` (or
`skipped`, for a bookmark or a blocked URL), and a failure is a barrier. Details, state and
scheduling: `docs/pipeline.md`.

- **Capture** makes the source readable locally (bytes are already readable for Markdown/text/URL;
  a PDF gets its text layer or OCR) and leaves a static snapshot. A URL that answers with a login,
  a paywall or a consent gate is **blocked**: reported with the reason, never read — and the human
  may attach the page they can see (HTML or PDF), which becomes the snapshot at the same URL address.
- **Outline** reads that snapshot and builds the hierarchical tree of parts; it never extracts.
- **Distill** runs the agent over one part at a time: one self-contained claim per statement, **in
  the configured claim language** (`language.claims`, default English), a flat topic in the same
  language, provenance to a source node, then an audit for statements left uncovered.
- **Cluster** prunes, attaches, graphs, splits, overlaps and writes titleless memberships. No model.

A page or sitemap is refreshed once a week (`capture.refresh_interval: '168h'`), a feed once a day
(`capture.feed_refresh_interval: '24h'`); an attached snapshot is never fetched by the cadence, only
by the explicit refetch of its source. A sitemap refresh reconciles its pages 1:1, a feed refresh only
adds — and a URL the human added by hand is released from its page/sitemap/feed, so it outlives its
former owner.

Derived things are caches (`outline`, `index.db`, `clusters/`); records are the truth (`<hash>.meta`,
`<hash>.log`, `atoms/`, `wiki/`, `vocabulary/`).

## Stack (locked)

| Building block | Choice | Rationale |
| --- | --- | --- |
| Language | Go 1.27 (pinned via `mise.toml`) | House standard |
| Module | `github.com/TheFranconianCoder/thought-keeper` | Repo remote exists |
| License | MPL-2.0 | see `LICENSE` |
| HTTP | `github.com/go-chi/chi/v5` (loopback, embedded) | PWA + JSON API, same origin |
| UI | Svelte 5 + Vite + TypeScript → `internal/web/dist` → `go:embed` | one binary, no CORS |
| Desktop shell | `github.com/gogpu/systray` (pure Go, zero CGO) | tray on Win/macOS/Linux |
| Agent SDK | `github.com/sashabaranov/go-openai` | native OpenAI SDK |
| Knowledge | `sources/` → `wiki/` + `atoms/` + `clusters/` | what was said, what the human wrote, what we believe, what travels together |
| DB | SQLite via `modernc.org/sqlite` | pure Go, no CGO |
| Migrations | `pressly/goose/v3`, `//go:embed migrations/*.sql` | append-only |
| Queries | `sqlc` (`engine: sqlite`) → `.../sqlc/dal` | generated |
| Lexical search | SQLite **FTS5** (`atoms_fts`, `source_nodes_fts`) | the lexical half of retrieval |
| Outline | per-source JSON next to the source (`<hash>.outline`) | derived, rebuildable |
| PDF text | poppler `pdftotext` | no CGO, no daemon |
| PDF OCR | poppler `pdftocairo` + `tesseract` | local |
| HTML → MD | `go-shiori/go-readability` + `html-to-markdown` + `x/net/html` | main-content extraction |
| Config | YAML + `${ENV}` expansion | `config.example.yaml` |
| Frontmatter | YAML decoded only in the file adapters (`markdown`, `wiki`, `asks`) | keeps YAML out of the core |
| IDs | ULID, 16-byte `BLOB` | claims, clusters, wiki pages, runs; sources by SHA-256 |
| File watching | `fsnotify` + polling fallback | automatic derivation on a dropped source |
| Logging | `log/slog` (JSON handler) | stdlib |
| CLI | Subcommands in the same binary | `app` · `open` · `add` · `query` · `lint` · … |
| Task runner | `Taskfile.yml` + `mise.toml` (+ `lefthook.yml`) | House standard |
| Tests | `go test ./...`, DB as `:memory:`, vault as a temp dir | fast |

**Deliberate non-choices:** any CGO dependency, any vector store/embedding, CGO OCR bindings, an
ORM, Postgres, Bleve/Lucene, an external search service, a web framework beyond chi, an MCP client
in v1, a native WebView.

## Architecture frame

Structure, layers, dependency rule and quality gates live in **`ARCHITECTURE.md`**. Short version:

1. Interfaces live next to the use cases, never next to adapters.
2. Use cases receive and return partials/DTOs, never raw entities.
3. Dependencies point **inward only**: `api/`, `infrastructure/` → `core/`. `core/` imports the
   standard library and nothing else.
4. Wiring happens only in `cmd/thought-keeper` and `internal/registry`.
5. **Two layers plus associations:** `sources/` immutable; `wiki/` human-authored; `atoms/` claims
   (source of truth); `clusters/` derived. Indexes, trees and cluster records are rebuildable.
6. Handlers and repositories stay dumb; time only through `Clock`; errors are sentinel types. A
   `SaveAtom` is authoritative for its provenance; a `SaveCluster` replaces its membership.
   Provenance is resolved **before** a write touches anything.
7. `dal/` is generated; migrations are append-only; one repository per entity.
8. **The agent is a use case; the model is an adapter.** The core never imports `go-openai`.

## Structure

```
cmd/thought-keeper        binary: app, CLI verbs
internal/app              lifecycle, pipeline schedulers, gate, tray, single instance, port
internal/registry         the one place that wires concrete adapters
internal/api              router/, handlers/, payloads/
internal/web              embeds the built PWA
web-next/                 Svelte PWA sources (task web:build → internal/web/dist)
web/                      previous PWA sources, kept as a reference
internal/core/entities    domain types
internal/core/markdown    pure: titles, slugs, code quoting, topic/tag normalization
internal/core/tree        pure: tree shape, node addressing, matter filter, rendering
internal/core/usecases    use cases; interfaces/; agents/; partialentities/
internal/infrastructure   config, objects, atoms, wiki, clusters, markdown, extract, tree,
                          persistence (sqlc + migrations), gateways, langdetect, asks,
                          vocabulary, sourcemeta, sourcelog, favicons, vaultgit, idpath, ...
internal/types            sentinel errors
internal/version, internal/logging
```

## Application model

Thought Keeper is a **desktop application**. The PWA and the CLI are entry points into one running
instance, not clients of a server you operate.

- **One binary, launched by the user.** It brings up the in-process services (HTTP on loopback, the
  watcher, the agent) and a **tray icon**. There is **one UI: the PWA**. On start the app hands the
  address to the operating system, preferring the installed PWA's own launcher; `--no-open` only
  logs it, `--headless` skips the tray and the open.
- **Single instance.** A second launch cannot take the vault flock; it reads the runtime endpoint,
  pings the running instance (`GET /api/v1/status`) and opens that instance's UI (exit `0`). The UI
  itself owns the one-window rule (a BroadcastChannel election).
- **Port discovery.** The HTTP port is fixed or ephemeral; the running instance writes its address
  and a lock to a runtime file so the CLI finds it.
- **Loopback only, no token, no CORS.**
- **Graceful shutdown.** Quit finishes or aborts running runs, flushes the index and releases the
  vault lock.
- **The tray is pure Go**, zero CGO. Autostart is opt-in.

## Vault in one look

```
<vault>/
├── index.db          # SQLite: FTS5, derived counts (rebuildable)
├── inbox/            # drop zone; the watcher intakes files into sources/
├── sources/          # immutable, content-addressed (object + sidecars)
├── wiki/             # user-authored Markdown pages (source of truth)
├── atoms/            # one claim per file (source of truth)
├── clusters/         # titleless associations (derived cache)
├── vocabulary/       # canonical names + aliases: topics.md, wiki-tags.md, source-tags.md
├── asks/             # saved Ask conversations (<run-id>.md)
├── .thought-keeper/  # derived, hidden: favicons/
├── .trash/           # deletions (trash, not delete)
└── (.git/ if you version the vault — the app keeps .gitignore current)
```

The layout, the sidecars, the per-source log and the frontmatter of atoms, wiki pages and cluster
records are specified in **`docs/storage.md`**.

## Working agreements

- **The docs are part of the change.** A new step, file, endpoint, config knob or operating rule is
  documented in the document that owns it (see the map above) in the same change. A decision that
  changes a rule changes this document first, then the code.
- **All repository artifacts are written in English** — code comments, docs, commit messages.
- **Do not commit on your own initiative.** Commits are made by the user; a requested commit message
  follows Conventional Commits, present tense, no Markdown.
- **Errors are sentinel types** from `internal/types/errors.go`, mapped to the wire once.
- **No silent writes and no invented records.** A failed step is recorded; a dead link is listed;
  an unavailable OCR path is flagged, never silently empty.
- **The vault is the truth.** The index is a cache; `reindex` and `rebuild-trees` rebuild it from
  the files. An index from another shape is reported, never rebuilt behind the user's back.
- **Run `task check`** (modernize → format → lint-fix → unit + integration tests → sqlc:verify →
  web:check) before calling work done.
- **New open work belongs in `TODO.md`**, not in a comment that nobody reads.

## Devlog / studio coupling

- Devlog: `../../studio-page/src/content/devlogs/thought-keeper/<nn>-<nn>.mdx`, with frontmatter
  `title`, `description`, `refCode` and `pubDate`. The series exists and is written in order — one
  entry per chapter, in the first person and in English; a new chapter is a **new entry**, never a
  rewrite of an earlier one.
- **Never generate the project logo with AI**: `design/pictograms/thought-keeper.svg` is the source
  of truth; re-render with `task pictograms -- --only thought-keeper` in `studio-page`.
- **The app icons are the pictogram reframed as a squircle**; `web/icons/pictogram.svg` is the
  artwork source, `web/scripts/render-icons.mjs` derives everything. `internal/web/dist/*` is a
  build output — never edit it there.
- Bumping a cached asset (an icon) means bumping the service worker's cache name in
  `web-next/public/sw.js`.
- The studio-page description and this document must stay consistent. The studio-page update is the
  separate final step after a code refactor.
