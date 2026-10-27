# Thought Keeper Architecture

A local knowledge base that follows the **LLM-Wiki pattern**, extended with an **atom layer**:
immutable **raw sources** (Markdown, PDF, TXT, URL) are distilled by an agent into **atoms**
(one claim per note, the Zettelkasten), the human authors **wiki pages**, and the claims are grouped
into **titleless clusters** (the association layer). Retrieval is **vectorless** — hierarchical
trees, the association layer and reasoning, not embeddings.

> Architectural frame. The model is `../scriber` (single Go service, Clean Architecture, sqlc +
> goose + SQLite, one binary) and `../syncraft` (Chi router, layer/naming rules, quality gates).
> The pattern is Karpathy's **LLM Wiki** (raw → wiki; *Capture / Query / Lint*); the atom layer is
> the `llm-atomic-wiki` extension; vectorless retrieval is modeled on **PageIndex** (tree index +
> reasoning). This document governs **structure, dependencies and quality gates**.
>
> The domain lives in `AGENTS.md` and `docs/`: `docs/pipeline.md` (steps, state, scheduling),
> `docs/storage.md` (vault, sidecars, conventions), `docs/data-model.md` (entities, schema),
> `docs/retrieval.md`, `docs/agent.md`, `docs/api.md`, `docs/pwa.md`, `docs/operations.md`.


---

## 1. Core Principles

- **Four directories, two layers plus associations, strictly separated.** `sources/` is immutable
  and content-addressed (except the two MUTABLE representations: a URL snapshot and a wiki page);
  `wiki/` holds user-authored Markdown (a destination); `atoms/` holds one claim per file (the
  Zettelkasten, **source of truth** for knowledge); `clusters/` is the **derived cache** of
  titleless associations over the claims. A cluster can be rebuilt from its claims; a claim cannot
  be rebuilt from a cluster or a page.
- **A wiki page is user-authored.** It is the one prose artifact a human owns: Markdown, a title,
  tags and a stable ULID under `wiki/`. Its source representation is derived and reprocessed from
  capture on every edit. The app may add `auto_tags`, kept apart from the human's.
- **Provenance is a chain: source node → atom.** Every claim carries the source node it came from.
  A wrong fact is corrected at the atom.
- **Distill once; associate incrementally.** Sources are read once; claims already exist and stay
  current. Ask reasons over claims, not over raw chunks re-derived every time.
- **Vectorless by design.** No embeddings, no vector database, no ANN index. Navigation is the
  generated **indexes**, the **per-source trees**, the **association layer** and lexical
  **full-text search** — with traceable citations.
- **Built to scale to thousands of sources.** Sources shard by hash prefix; claims carry a FLAT
  **topic** field; clusters are records (`clusters/<ab>/<cd>/<id>.meta`) with no title and no prose.
  The agent navigates by reasoning over topics, trees, clusters and lexical search, never by scanning
  one flat file.
- **The agent owns atoms; the human owns sources and wiki pages.** Drop a source or write a page;
  the agent distills it and the deterministic cluster step groups the claims. The cluster step has
  **no model**.
- **Sources are immutable and content-addressed.** Named by SHA-256, sharded, deduplicated, never
  overwritten. A correction is a new source. The deliberate exceptions are a URL snapshot and a
  wiki page representation, whose stable address survives a refresh/edit.
- **Agentic where thinking is needed.** A watcher on `inbox/` triggers the pipeline; the first three
  steps, queries, tagging and vocabulary calls run through the bounded tool loop. Step 4 is a graph,
  not a prompt.
- **Everything derived is rebuildable.** Trees, outlines, cluster records and the SQLite index are
  rebuildable from `sources/` + `wiki/` + `atoms/` + `clusters/`. The only exception is the
  append-only audit.
- **Never invent, never discard.** A claim is never invented from a missing part; a failed step is
  recorded; a scanned page with no text layer is flagged (OCR) rather than silently empty.
- **Standard library first.** Core imports the standard library and its own core packages only —
  no YAML, no SQL, no HTTP, no OpenAI, no PDF, no OCR (see § 2.2 rule 5).
- **External tools are ad-hoc, never daemons.** PDF text and OCR are one-shot subprocesses that
  read stdin and write stdout (`pdftotext`, `pdftocairo`, `tesseract`), invoked per call and gone
  when done. No sidecar, no long-running helper, no CGO.
- **CGO-free end to end, one static binary.** SQLite (`modernc.org/sqlite`), the PDF/OCR path
  (subprocesses) and the tray (`github.com/gogpu/systray` — pure-Go FFI and D-Bus) all avoid CGO, so
  the whole application cross-compiles statically. External binaries are tools it *calls*, not
  libraries it *links*. **No exception:** a native-window shell that links a system WebView would be
  CGO by nature, which is one reason there is none — the UI is the PWA. The binary stays CGO-free; `CGO_ENABLED=0` is therefore set explicitly in the Taskfile rather
  than inherited (a machine with a C compiler would otherwise silently link libc).
- **A desktop application, not a server.** One user, loopback only, no service manager, no daemon
  semantics. The embedded HTTP service is the PWA's backend (served from the same origin) and the
  CLI's endpoint; it is never exposed to the network. Secrets come from the environment.

---

## 2. Clean Architecture

### 2.1 Layers

```
┌──────────────────────────────────────────────────────────────┐
│  Entities (Core)                                              │
│  - Source, SourceNode, Atom, Cluster, LintFinding, Run         │
│  - Markdown semantics: slugs, tags, code quoting               │
│  - tree shape, claim topics, cluster membership                │
│  - no external dependencies, no SQL/HTTP/YAML/PDF/OCR/OpenAI  │
├──────────────────────────────────────────────────────────────┤
│  Use Cases (Core)                                             │
│  - capture, outline, distill, cluster, query, lint            │
│  - reindex, rebuild-trees                                     │
│  - define interfaces (ports); receive/return partials or DTOs │
├──────────────────────────────────────────────────────────────┤
│  Adapters                                                     │
│  - source store (CAS), atom store, cluster store, index (sqlc),│
│    codec (YAML), extractors (md/txt/pdf/ocr/html), tree store,│
│    OpenAI gateway, fetcher, OCR runner, git snapshot          │
│  - HTTP (chi router + handlers), CLI subcommands              │
└──────────────────────────────────────────────────────────────┘
```

### 2.2 Dependency Rule (binding)

1. **Interfaces live next to the use cases** (`internal/core/usecases/interfaces/`), not next
   to the adapters. *(from Scriber/Syncraft)*
2. **Use cases receive and return partials or DTOs**, never raw entities. *(from Scriber/Syncraft)*
3. **Partials have `toEntity()`** for the adapter layer. *(from Scriber/Syncraft)*
4. **One partial per entity.** *(from Scriber/Syncraft)*
5. **Dependency direction:** `internal/infrastructure/**` → `internal/core/**`, and
   `internal/api/**` → `internal/core/**`. **Never the other way around.** `internal/core/**`
   imports the standard library and its own core packages only — no `database/sql`, no
   `net/http`, no `modernc.org/sqlite`, no `gopkg.in/yaml.v3`, no `go-openai`, no PDF library, no
   `os/exec`. YAML frontmatter, PDF/OCR extraction and the OpenAI/HTTP clients all live in
   adapters. The *pure* Markdown semantics and the tree *shape* live in core.
6. **Wiring happens in exactly two places:** `cmd/thought-keeper` (lifecycle) and
   `internal/registry` (constructing adapters and handing them over as interfaces). Use cases
   never know concrete types.
7. **Handlers and repositories stay dumb:** handlers parse, call one use case, map the result.
   Stores translate hashes/ids ↔ bytes; repositories translate arguments into SQL. No hashing
   policy, no agent policy outside use cases.
8. **Time is a port.** Use cases read the clock through an interface (`Clock`), never `time.Now()`.
9. **Errors are types, not strings.** Sentinel errors in `internal/types/errors.go`, translated
   to HTTP once in `internal/api`.

### 2.3 Naming Conventions

Only the column that applies to this service: **Go (Service)**.

| Layer       | Path                                                               |
| ----------- | ------------------------------------------------------------------ |
| Entities    | `internal/core/entities/atom.go`                                   |
| Partials    | `internal/core/usecases/partialentities/partial_atom.go`           |
| Use Cases   | `internal/core/usecases/distill_source.go`, `query_knowledge.go`, …     |
| Interfaces  | `internal/core/usecases/interfaces/atom_store.go`, …               |
| Persistence | `internal/infrastructure/persistence/sqlc/atom_repository.go`      |
| Gateways    | `internal/infrastructure/gateways/openai_agent.go`                 |
| Types       | `internal/types/errors.go`                                         |

### 2.4 Naming Rules (strict)

| What                  | Correct                                  | Wrong             |
| --------------------- | ---------------------------------------- | ----------------- |
| Persistence folder    | `persistence/`                           | `storage/`        |
| Partials folder       | `partialentities/`                       | `partials/`       |
| Repository file       | `repository.go`                          | `adapter.go`      |
| External adapter      | `openai_agent.go`, `*_gateway.go`        | `client.go`       |
| Use case files        | `distill_source.go`                      | `run.go`, `manager.go` |
| Generated SQL code    | `dal/`                                   | `sqlc_gen/`, `models/` |
| Source store          | `infrastructure/objects/`                | `sources/`, `cas/`  |
| Extractors            | `extract/pdf.go`, `extract/ocr.go`       | `parsers.go`      |
| Subprocess runner     | `infrastructure/gateways/exec_runner.go` | `shell.go`        |

---

## 3. Project Structure

```
thought-keeper/
├── AGENTS.md                       # entry point: positioning, language, pipeline in brief, map
├── ARCHITECTURE.md                 # this document
├── TODO.md                         # open work and open questions
├── Taskfile.yml · mise.toml · lefthook.yml · .golangci.yml · config.example.yaml · sqlc.yaml
├── cmd/thought-keeper/main.go      # subcommands; app lifecycle wiring (tray, HTTP, watcher)
├── web/                            # PWA sources; built assets embedded via internal/web
├── docs/
│   ├── pipeline.md                 # steps, state, lights, jobs, gate, automation
│   ├── storage.md                  # vault, sidecars, source/atom/wiki/cluster conventions
│   ├── data-model.md               # entities, identifiers, schema, derived values
│   ├── retrieval.md                # vectorless retrieval, search, citations
│   ├── agent.md                    # operations, tools, prompts, part outcomes, audit
│   ├── api.md                      # HTTP API, async work, events, errors
│   ├── pwa.md                      # lenses, routes, SSE state, empty states, assets
│   └── operations.md               # running, config, CLI, tasks, operating rules, backup
└── internal/
    ├── app/                        # desktop lifecycle: tray, single instance, port, open UI,
    │                               #   pipeline schedulers (pump), gate, reconcile, SSE hub
    ├── web/                        # go:embed of the built PWA + static handler
    ├── core/
    │   ├── entities/
    │   │   ├── pipeline.go             # the four steps, their states, StepSet.Ready
    │   │   ├── sidecar.go              # suffix constants, URLHash, WikiHash, record shape
    │   │   ├── source.go · source_node.go · wiki.go
    │   │   ├── atom.go · cluster.go · vocabulary.go
    │   │   └── lint_finding.go · run.go
    │   ├── markdown/               # pure stdlib: slugs, tags, code quoting, URLs, citations
    │   ├── tree/                   # pure: tree shape, node addressing, breadcrumbs,
    │   │                           #        front/back-matter filter, Markdown rendering
    │   └── usecases/
    │       ├── interfaces/              # ports: stores, index, codec, extractor, tool runner,
    │       │                            #   tree builder, agent gateway, fetcher, clock, …
    │       ├── partialentities/         # one partial per entity, with toEntity()
    │       ├── intake_source.go         # hash, dedupe, store raw bytes
    │       ├── capture_source.go        # CAPTURE: extract + write the readable rendering
    │       ├── build_tree.go            # OUTLINE: rendering → PageIndex tree
    │       ├── distill_source.go        # DISTILL: agent over parts → atoms
    │       ├── document_run.go          # a document run: outline → distill (one source)
    │       ├── cluster_pass.go · cluster_graph.go · cluster_terms.go · cluster_plan.go
    │       ├── query_knowledge.go       # agent: reason over claims, cite
    │       ├── lint_knowledge.go · programmatic_lint.go · cleanup.go
    │       ├── wiki_pages.go · tag_wiki_page.go
    │       ├── curate_vocabulary.go · rebranch_claims.go · apply_vocabulary.go
    │       ├── rebuild_trees.go · reindex_vault.go · reconcile_vault.go
    │       ├── list_atoms.go · related_atoms.go · search.go · asks.go
    │       └── agents/
    │           ├── loop.go              # tool-calling loop (max iterations, transcript budget)
    │           └── prompts.go           # distill / query / vocab / tag prompts
    ├── api/
    │   ├── router/router.go             # chi Router, middleware, the gate on write routes
    │   ├── handlers/                    # atoms.go sources.go wiki.go url.go tree.go search.go
    │   │                                #   query.go asks.go pipeline.go vocabulary.go status.go
    │   └── payloads/                    # mapping.go + per-area payloads
    ├── infrastructure/
    │   ├── config/config.go
    │   ├── objects/store.go             # content-addressed source store (sha256 + shards) +
    │   │                                #   the sidecar rule (SidecarPath, RemoveSidecars)
    │   ├── vaultgit/ignore.go           # keep the derived files out of a versioned vault
    │   ├── sourcemeta/store.go          # <hash>.meta: the step record (vault truth) + tracker
    │   ├── sourcelog/store.go           # the per-source log, next to its source (<hash>.log)
    │   ├── tree/store.go                # the outline, next to its source (<hash>.outline)
    │   ├── atoms/store.go               # atom file store (ID paths, atomic writes)
    │   ├── clusters/store.go            # cluster records (ID paths, atomic writes)
    │   ├── wiki/store.go                # page files (frontmatter + body)
    │   ├── asks/store.go                # saved Ask conversations
    │   ├── vocabulary/store.go          # topics.md, wiki-tags.md
    │   ├── favicons/store.go            # content-addressed icons (.thought-keeper/favicons/)
    │   ├── markdown/atomcodec.go · codec.go
    │   ├── extract/
    │   │   ├── markdown.go · text.go
    │   │   ├── pdf.go                   # poppler: pdftotext + pdftocairo/tesseract (OCR)
    │   │   └── html.go                  # readability → markdown (URL bodies)
    │   ├── langdetect/                  # language hints for multilingual search
    │   ├── idpath/                      # ULID → sharded path
    │   ├── persistence/                 # embed.go · migrate.go · schema.go · migrations/ · sqlc/
    │   └── gateways/
    │       ├── openai_agent.go          # go-openai: tool-calling turn
    │       ├── fetcher.go               # net/http fetcher with guards
    │       ├── exec_runner.go           # os/exec: ad-hoc tools, stdin→stdout, no daemon
    │       ├── readability.go · htmlmeta.go
    │       └── clock.go
    ├── registry/registry.go
    ├── types/errors.go
    ├── version/version.go
    └── logging/logging.go
```

Tests are `*_test.go` files next to the code they exercise. Small fixtures (a `.pdf` with a text
layer, a scanned one-page `.pdf`, a `.md`, a `.txt`) are acceptable.

---

## 4. Vault & Persistence Rules

### 4.1 Sources (`sources/`, immutable, content-addressed)

- **Drop zone.** Files land in `inbox/`; the watcher (or `POST /add`) hashes them into the store and
  removes them from the inbox. `sources/` is only ever written by intake.
- **Name = SHA-256 of the content**, sharded `sources/<ab>/<cd>/<sha256>.<ext>`. Sharding keeps
  directories small so the store scales to tens of thousands of objects; identical bytes collapse.
- **Write-once.** The agent and API never write into `sources/`. A correction is a *new* source →
  new hash → re-distill the affected topic. The vault is append-only at the source layer.
- **Kinds:** `.md`, `.pdf`, `.txt`, and URL snapshots stored as `.md` with `url`/`fetched_at`
  frontmatter. Unsupported extensions are rejected (`unsupported_source`). A wiki page is stored
  as `.md` too; its source identity carries the `wiki:` original name and a stable `WikiHash`.
- **Language is a source identity field.** The detected ISO 639-1/639-3 hint lives in `<hash>.meta`
  and in the index `meta` JSON. Intake/capture write it; the consistency pass backfills missing
  values from the readable text. Long documents are sampled in short windows spread over the text,
  so their size cannot defeat classification. Search uses the SET of present languages for
  morphology, never as a hard filter.
- **The two mutable representations** are URL snapshots and wiki pages. Their object is written
  through a fixed address (`PutAt`); everything else is write-once.

### 4.2 Atoms (`atoms/`, one claim per file, source of truth)

- **One atom is one claim.** A short Markdown file whose body states a single fact/definition/
  claim/example/counterpoint, with provenance to a source node. The Zettelkasten.
- **Addressed by ID, organized by a FIELD.** `atoms/<ab>/<cd>/<id>.md`, sharded two levels. The
  claim's `topic` is a FLAT subject field (`caching`), not a folder; the catalog is SQLite
  (`list_topics`, `list_atoms`, search), and there is no generated atom index.
- **Identity is the frontmatter `id` (ULID)**; the path follows from it. A topic rename moves no
  file. A claim has NO tags.
- **Status lives in the atom.** `active` | `superseded` | `disputed` lets lint express "newer
  source superseded this claim" and "two atoms contradict each other" concretely.
- **Atomic writes.** Temp file → `fsync` → `rename`; content-hash idempotence.

### 4.3 Wiki pages (`wiki/`, user-authored, source-backed)

- **The page is the truth.** `wiki/<ab>/<cd>/<id>.md` with `id`, `title`, `tags` (human), optional
  `auto_tags` (derived), `urls`, `source` and timestamps in frontmatter, plus the Markdown body. The
  source representation is derived.
- **Saving reprocesses one source.** `WikiHash(id)` is the source address; an edit writes through
  it, clears the old derivation and re-arms capture. The normal pump runs capture → outline →
  distill.
- **A save is a local write.** The links a page declares are diffed and handed to the URL queue; the
  request ends when the page is on disk. A new link is claimed as a source row before it is fetched
  (pending capture, later green or red with the reason), and the fetch fills that row rather than
  filing a second one.
- **References are ID-based:** `[[wiki:<id>]]` and `[[source:<hash>]]`. Detail views show the full
  ID with a copy button. Tags are a frontmatter field and power the tag cloud.

### 4.4 Clusters (`clusters/`, derived, titleless)

- **A cluster is a group of claims, not a document.** The record is
  `clusters/<ab>/<cd>/<id>.meta` with `id`, `atoms`, `signature`, `created`, `updated`. There is no
  page body, no title, no kind, no tags, no `.dirty` marker and no successor chain. The shard is the
  tail of the ULID, exactly as for atoms.
- **It is a CACHE.** The claims are the truth and the graph over them is deterministic, so a reindex
  or a re-derivation can rebuild the whole directory from the claims. That is why `reindex` reads the
  records (they are the cheap source for membership) and writes them back when a member vanished.
- **Membership is queried, not navigated.** `cluster_atoms` is the child table; `SaveCluster` is
  authoritative for it (replace-shape). The Claims detail view reads a claim's cluster peers; there
  is no cluster list, no cluster page and no sidebar entry.
- **Split and merge are membership changes.** A structural change is a new membership set; a record
  that loses every member is removed. No id is linked to from outside the cluster layer, so nothing
  needs a redirect.
- **No model.** The cluster step is deterministic: prune, attach, graph, split (including a
  topic-span broadness cut), overlap (gated by real edges + centroid), write.

### 4.5 The pipeline: four strictly separated steps

The domain reference (names, rules, status, scheduling) lives in **`docs/pipeline.md`**. The
structural consequences:

- **`capture → outline → distill → cluster`.** `capture`, `outline` and `distill` are DOCUMENT steps:
  one source, strictly in that order, and they never touch the association layer. `cluster` is the
  VAULT-LEVEL step: it is considered over the whole vault and may only run while no document step
  runs.
- **Capture writes, outline reads.** Step 1 extracts (poppler/OCR) and writes the readable rendering
  next to the object (`sources/<ab>/<cd>/<hash>.capture.md`, one `<!--part …-->` marker per part — a
  plain Markdown document, and only a kind that has to be extracted gets one); step 2 parses it
  (`tree.ParseRendering`) and extracts nothing. A rebuild therefore reproduces the parts the bytes
  produced.
- **The order is one function** (`entities.StepSet.Ready`: a step needs every earlier step `ok` or
  `skipped`) **and one lock** (the document steps take a shared `RWMutex`, the cluster step takes it
  exclusively — `internal/app/pipeline.go`).
- **The records are checked against the disk.** A consistency job (`usecases.ReconcileVault`, run by
  the app on `pipeline.reconcile_interval`) verifies that every artefact a step claims exists, and
  invalidates the derivation when it does not. The same job keeps step 4 fed (it asks for a pass
  whenever claims are unclustered).
- **The step state is a file first.** `sources/<ab>/<cd>/<hash>.meta` (vault, written by
  `infrastructure/sourcemeta`) is the truth; `sources.steps` is the index mirror, rebuilt by
  `reindex`. `GET /api/v1/pipeline` reports the global state; each run names its step (`runs.step`).
- **The cluster step is deterministic and incremental.** `usecases.ClusterPass`:
  1. **prune** members that vanished, and clusters that lost all of them;
  2. **attach** free claims to an existing cluster by centroid cosine (`cluster.attach_min_score`);
  3. **graph** the remaining free claims — sparse IDF-weighted neighbours (`neighbor_k`,
     `min_claim_score`), merges with quality gates (`merge_min_internal`, `merge_min_gap`,
     `cross_topic_min_edges`) and splits at the weakest bridges (`split_min_core_size`);
  4. **overlap** a claim into several clusters only when it has at least `overlap_min_edges` real
     edges into that cluster, centroid similarity, and the group is still below
     `max_cluster_claims` after the addition (`overlap_max`);
  5. **write** one record per cluster under `clusters/`, then its index row/membership.

  A group above `cluster.max_cluster_claims` is split by similarity. There is no naming, no review
  and no target number of clusters.
- **A document step never touches a cluster.** The cluster step is the only writer of the association
  layer; a document step's change simply leaves claims unclustered until the next pass.
- **Navigation is not content.** A short breadcrumb/menu consisting only of Markdown links and list
  markers is dropped by the outline builder; `reindex` cleans an old outline's navigation text, and
  `unitsOf` ignores such nodes defensively.
- **Use cases:** `usecases.DocumentRun` (outline → distill, one source), `usecases.CaptureSource`,
  `usecases.BuildTree`, `usecases.DistillSource`, `usecases.ClusterPass` (the whole association
  step), `usecases.Cleanup` (prune dead members, refresh the findings).

### 4.6 Asks (`asks/`, saved questions and answers, records)

- **An Ask is a record, not a cluster.** `asks/<run-id>.md` carries the question and the answer
  (Markdown frontmatter + body). Citations are stored as ordinary Markdown links; legacy
  `[[claim:...]]` files are normalised at start-up. It is written automatically when a query succeeds
  and is removed to `.trash/asks/` only by the reader.
- **The ID is the query run's ID**, so a saved answer remains traceable to the run and its tool steps.
- **Never a cluster:** Ask records have no topics, no atoms and no membership.

### 4.7 The index (SQLite, in the vault root, derived)

- **`dal/` is generated.** Do not touch the file header, do not rename, do not reformat. Changes
  belong in `internal/infrastructure/persistence/sqlc/queries/*.sql` + `sqlc.yaml`, followed by
  `task sqlc:generate`.
- **Query files are ASCII-only.** sqlc's sqlite engine mis-splits a query file at non-ASCII bytes:
  a multi-byte character in a comment shifts the tail of a later statement into SQL that still
  compiles and still passes `sqlc vet` — the damage only shows at runtime. Keep `*.sql` free of
  em-dashes and accents (the generated const in `dal/` is the thing to check after `sqlc:generate`).
- **A whole-vault read is a PROJECTION or a STREAM, never the list read.** `ListAtoms` (every
  column, plus hydration) serves a reader that renders claims; a pass that walks every claim reads
  only the columns it uses (`AtomAddresses`, `AtomIDs`, `ListOldestAtoms`, `AtomTextsForIDs`,
  `SourcesForAtoms`) or walks the table as a stream and keeps nothing (`StreamAtomTexts` — see
  `queries/atom_projections.sql`; the association pass and the health checks fold their work from
  that stream). Reading bodies to answer "which claims exist" is how a pass over 135 new claims
  cost the whole vault.
- **Hydration hydrates the page, not the table.** A list read carries provenance for the rows it
  RETURNS (`AtomSourcesForAtoms`, `AtomSourceCountsForAtoms`) — never
  by loading every row of `atom_sources` to decorate fifty. And a hydration failure is an ERROR, not
  a missing field: a caller that re-saves a silently-under-hydrated atom DELETES its provenance.
  A bounded queue walk (`ListOldestAtoms`) therefore reads text only, and re-reads each atom WHOLE
  before writing it back.
- **Migrations are append-only.** A shipped migration is never edited; a correction is a new
  numbered file. A down section is mandatory.
- **One repository per entity.** `atom_repository.go` implements `interfaces.IndexRepository`.
- **`sqlc vet` + `sqlc diff`** run as a gate (`task sqlc:verify`).
- **Tests use `:memory:`** — migrations run through the same chain as in production.
- **`PRAGMA`s are set centrally on open** (`journal_mode=WAL`, `busy_timeout`, `foreign_keys`,
  `synchronous`) — in the DSN, so every pooled connection gets them.
- **A small pool, not one connection.** WAL lets readers and the one writer work at the same time;
  with a single connection a slow aggregate (the sources list) stalled every write, step records
  included. Writes still serialise on SQLite's lock and `busy_timeout(5000)` absorbs the wait. An
  in-memory index stays on ONE connection: every connection to `:memory:` is a different database.
- **The schema carries its own SHAPE** (`PRAGMA user_version`, written by the initial migration,
  compared by `persistence.Open`). goose cannot notice a database written by an older build — it does
  not run an applied migration again — and the failure names a column instead of the reason. A mismatch
  is REPORTED (start-up log, `GET /api/v1/status`, `thought-keeper status`) and never repaired by the
  app: the index lives in the vault, and deleting it is the user's decision.
- **Everything derived is rebuildable.** `reindex` re-derives atoms, cluster rows, sources and node
  rows (from the outlines) from the vault; `rebuild-trees` re-derives the outlines. The
  **append-only audit tables** (`runs`, `run_steps`, `lint_findings`) are the only rows `reindex`
  leaves alone.
- **The sidecar rule.** Every derived file lies next to what it is about, named after it plus a
  suffix (`objects.SidecarPath`); it has exactly ONE owner, is deleted with it
  (`objects.RemoveSidecars`), is never mistaken for an object, and carries no content that
  cannot be derived elsewhere — except the *records* (`.meta`, `.log`), where the file is the
  authority. The one shared cache is `.thought-keeper/favicons/`. See **`docs/storage.md` →
  Sidecars**.
- **A source is ONE folder entry.** Everything about it lives next to its object in `sources/`:
  `<hash>.capture.md` (the readable rendering, PDFs only), `<hash>.meta` (the step record), `<hash>.outline` (the
  node tree) and `<hash>.log` (its own log). Deleting the source deletes the whole story
  (`objects.RemoveSidecars`), so nothing about a source that no longer exists can linger. The DB keeps
  node rows for querying and a `tree_state` flag; the outline is the JSON sidecar the comparison and
  the distill step read.
- **The index lives in the VAULT ROOT** (`<vault>/index.db`) and is rebuildable: it belongs to exactly
  one vault, so a vault travels (and can be switched) as a whole. A relative `index.path` is resolved
  against the vault root, an absolute one is honoured. The one shared derived cache beside it is
  `.thought-keeper/favicons/` (two sources of one host share an icon, so it is no single source's
  file).
- **IDs are ULIDs stored in a 16-byte `BLOB` column** (`NOT NULL PRIMARY KEY`,
  `CHECK (length(id) = 16)`). Content hashes are hex `TEXT` with a unique index. On the wire, ids
  are the 26-character Crockford Base32 string; conversion happens at the boundary.
- **Search is FTS5.** `atoms_fts` (claims/bodies) and `source_nodes_fts`
  (section text) provide the lexical half of vectorless retrieval. If the bundled SQLite lacks
  FTS5, a `LIKE` fallback is gated by a startup capability check and surfaced in the status report.

---

## 5. API Rules

The endpoint surface is in **`docs/api.md`**.

- **The service is internal to the desktop app.** It binds loopback only and serves the PWA at `/`
  (same origin) and the JSON API at `/api/v1`; there is no token and no CORS. The port is fixed or
  ephemeral and published to a runtime file so the CLI can find the running instance.
- **The router knows only handlers**, handlers know only use cases. No handler imports
  `infrastructure/objects`, `infrastructure/atoms`, `infrastructure/clusters` or `persistence`.
- **One mapping location:** `internal/api/payloads/mapping.go`.
- **Atoms and clusters are addressed by ULID, sources by hash, claim topics by name.** Paths are
  read-only projections. A topic is a flat name; a cluster has no label of its own.
- **Long operations are asynchronous; fast ones are not.** The agent-backed verbs (`POST /sources/{hash}/distill`,
  `POST /query`, `POST /lint`, the vocabulary verbs) record a run and return `202 {run_id}`; the
  client follows `GET /runs/{id}` or the event stream. `POST /cluster` is queued and also answers
  `202`. `POST /add`, `POST /reindex`, `POST /rebuild-trees`, `POST /reconcile` and the source verbs
  are synchronous (a store walk, not a model call); they adopt runs if that ever changes. The CLI's
  `--wait` is the client's poll loop, not a second server route.
- **Query answers are saved automatically.** Every successful query writes an Ask record to
  `asks/<run-id>.md`; `GET /asks` lists them and `DELETE /asks/{id}` moves one to `.trash/asks/`.
  There is no explicit filing step in the Ask UI.
- **Error translation happens exactly once:** sentinel error → HTTP status + error body, centrally
  in `internal/api`. No stack traces in the body.
- **Idempotency where it matters:** re-intaking identical bytes is a no-op (content addressing);
  re-writing unchanged content is a no-op; deriving an unchanged source writes no claim twice.

---

## 6. Agent Rules

- **The agent is a use case, the model is an adapter.** Prompting, tool schemas and the tool loop
  live in `internal/core/usecases/agents/`; the OpenAI SDK lives only in
  `infrastructure/gateways/openai_agent.go` behind `interfaces.AgentGateway`.
- **Two models, matching PageIndex's split.** `model_compile` (cheap) names trees and extracts
  atoms, tags pages and curates vocabularies; `model_reason` (strong) answers queries and will run
  semantic lint. Step 4 uses no model.
- **Tools are the whole interface to the world.** Each tool maps to one use case or read; no
  filesystem, shell or network access outside a declared tool. The contract lives in
  `docs/agent.md`.
- **Retrieval is a tool, not a prompt trick.** The query agent navigates claims with `list_topics`,
  `search` (claims), `read_atom` and `list_atoms`; the outline agent uses the PageIndex tree via
  `read_source_node`. Reasoning over hierarchy and graph, never over embeddings.
- **Agentic OCR use is explicit.** Atomizing a page whose text came from OCR carries an
  `ocr: true` marker so the provenance is honest about a lossy extraction.
- **Everything is recorded.** Every run writes a `runs` row; every tool call writes a `run_steps`
  row. Audit tables are append-only and are not rebuilt by `reindex`.
- **The loop is bounded.** `max_iterations` and a per-op timeout are config; hitting the bound is
  a recorded failure.
- **No silent writes.** Agent Markdown is rendered through the same codecs as any other write.
  Output that fails to decode is a recorded failure with the raw output retained.
- **Citations are mandatory.** A query answer references the claims it used as
  `[[claim:<id>|short claim text]]`; clusters and source nodes are not cited by Ask.

---

## 7. Quality Gates

### Tools (`mise.toml`)

| Tool           | Purpose                                    |
| -------------- | ------------------------------------------ |
| `go`           | Language                                   |
| `golangci-lint`| Linting (configured by `.golangci.yml`)    |
| `gofumpt`      | Formatting (not `gofmt`)                   |
| `sqlc`         | Generating persistence code                |
| `goose`        | Running migrations manually                |
| `lefthook`     | Git hooks                                  |
| `task`         | Task runner                                |
| `syft`/`grype`/`grant` | Supply-chain audit (`task audit`)  |

External tools (**poppler**: `pdftotext` + `pdftocairo`, and `tesseract`) are invoked **ad-hoc per
call via stdin/stdout** — never a daemon or a long-running process. In development `mise install`
provides them; in production they are system prerequisites. A startup capability check decides what
is actually available; when absent, PDF text/OCR is unavailable and the affected sources are
flagged rather than silently empty.

### Rules

- **`.golangci.yml` is Scriber's in essence** — `default: none` plus an explicit linter list; the
  only deviation is the exclusion path for generated sqlc code.
- **gofumpt instead of gofmt.** Deviations are defects, not taste.
- **Short loop:** `task format:go` → `task lint:go` → `task unit-tests` → `task integration-tests`.
  `task check` is the sum (modernize, format, lint-fix, both test tiers, sqlc:verify, web:check).
- **lefthook** blocks the commit: audit when `go.{mod,sum}` changes, plus format and lint.
- **No `//nolint` without a reason**; exceptions belong in the commit message.
- **The agent, the network and OCR are ports.** Unit tests use a fake `AgentGateway`, a fake
  `Fetcher` and a fake `ToolRunner`, so intake, capture, outline, distill, cluster, query,
  lint and tool dispatch are covered without OpenAI, without the network and without external
  tools. Opt-in integration tests (skipped without `OPENAI_API_KEY`) hit the real API.

### Task Naming

`build`, `run`, `run:open`, `run:headless`, `cross`, `format:go`, `lint:go`, `lint-fix:go`,
`modernize`, `unit-tests`, `integration-tests`, `check`, `sqlc:generate`, `sqlc:verify`,
`migrate:up|down|status`, `audit`, `hooks`, `icons`, `web:install`, `web:build`, `web:dev`,
`web:check`.

### Test Tiers

1. **Unit** (`internal/core/...`): fakes for every port. Covers Markdown extraction, code quoting,
   the tree shaper, hashing/sharding, the atom codec and the agent loops with a scripted gateway.
2. **Integration** (`httptest` + `:memory:` SQLite + a temp-dir vault): router, handlers, the
   index repositories, migrations, extraction of a small real `.pdf`, `reindex`, `rebuild-trees`.
3. **Acceptance** (`internal/registry`): the full path from an HTTP request or CLI verb to files
   on disk and rows in the index, with fetcher, OCR runner and agent replaced by fakes.

---

## 8. Implementation Phases

**Phases 1–8 are implemented.** The semantic (LLM) lint pass is deferred, and git-backed snapshots
and trash *restore* are not built — both are tracked in `TODO.md`. Beyond the phase list: URL intake
+ scheduled refresh (with derivation correction of a changed source), source deletion (trash) and
inline raw source viewing, drag & drop uploads, wiki pages with derived tags and two curated
vocabularies, language-aware search, and an event-driven (SSE) **claim-first PWA** on the studio
palette — a calm, Notion-flavored reader whose sidebar is a finite set of lenses: **Claims, Wiki,
Sources, Attention, Bookmarks, Ask, Status**. A cluster is visible only as a claim's peers.

1. **Foundation** — module, tooling, config, logging, SQLite incl. `PRAGMA`s, goose migrations,
   `status` report, vault flock. *Done when `task check` is green and an empty app starts.*
2. **Sources & index** — content-addressed store (SHA-256, shard dirs, dedupe), kind detection,
   intake, the schema, FTS5 capability check, `reindex_vault`.
   *Done when dropping `.md`/`.txt`/`.pdf` files yields deduplicated sources and a rebuildable index.*
3. **Extraction & trees** — markdown/text/PDF extractors, the external-tool OCR fallback with
   capability check, the tree builder, `rebuild-trees`.
   *Done when a text-layer PDF becomes a navigable tree and a scanned PDF is OCR'd (or flagged when
   tools are absent).*
4. **Atoms** — atom store + codec, the distill agent (claims, topics, provenance, status), the
   atom graph and claim-topic catalog.
   *Done when a source becomes claims with a flat topic and provenance to their source nodes.*
5. **Clusters** — the titleless cluster store, the deterministic claim graph with attach/split/overlap,
   cluster membership reads, and the claim-peer view.
   *Done when the claims of several sources are grouped without a page or a model.*
6. **Query & lint** — `query_knowledge` with mandatory claim citations, automatically saved Ask
   records, the claim finder, programmatic lint over claims, `lint_findings`.
   *Done when a question returns a cited answer, and lint reports actionable findings.*
7. **Desktop shell** — the `app` lifecycle: single instance, port discovery + runtime file, graceful
   shutdown, and the pure-Go cross-platform tray (`github.com/gogpu/systray`, zero CGO) with *Open* /
   *Pause watcher* / *Quit* (+ OS notifications); `--headless` for environments without a tray host.
   The UI is the **PWA**, served by the embedded web handler; there is no native window and no
   WebView (a native shell would be CGO by nature, see § 1).
   *Done when the app lives in the tray, a second launch focuses the running instance, and quitting
   releases the lock cleanly.*
8. **PWA & operations** — the web UI served by Chi from `go:embed` (claims and their cluster peers,
   wiki pages, sources, source trees, query; same origin), plus source deletion to `.trash/` and the
   `VACUUM INTO` backup recipe.
   *Done when the PWA drives the whole loop in a browser tab.* Trash restore is still open
   (`TODO.md`).

---

## 9. References

- `AGENTS.md` — entry point: positioning, ubiquitous language, the pipeline in brief, structure,
  working agreements, and the documentation map.
- `docs/` — the domain reference: pipeline, storage, data model, retrieval, agent, API, PWA,
  operations.
- **`TODO.md`** — open work and open questions.
- **[Karpathy, *LLM Wiki*](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f)** —
  the pattern: raw → wiki → schema; Capture / Query / Lint; `index.md` + `log.md`.
- **[cablate, *llm-atomic-wiki*](https://github.com/cablate/llm-atomic-wiki)** — the atom layer,
  flat claim topics, two-layer lint, the naming lock; "atoms are the source of truth, wiki is a
  derived cache".
- **[PageIndex](https://github.com/VectifyAI/PageIndex)** — vectorless, reasoning-based RAG:
  hierarchical tree index, LLM reasoning over the tree, traceable retrieval.
- `../scriber/ARCHITECTURE.md` — primary structural model.
- `../syncraft/ARCHITECTURE.md` and `../syncraft/server/` — chi router and DI.
- `../stay-awake` — the house's native tray app (Linux appindicator, Windows `Shell_NotifyIcon`):
  the desktop-shell shape this project mirrors in Go.
- **[gogpu/systray](https://github.com/gogpu/systray)** — pure-Go, zero-CGO cross-platform tray
  (`Shell_NotifyIcon` / `NSStatusBar` via pure-Go FFI / D-Bus StatusNotifierItem): the tray this
  project uses. `getlantern/systray` and `fyne.io/systray` are the CGO alternatives.
- `../openaidemo/` — the native OpenAI tool-calling loop.
- `../gaugemind` — sqlc + goose + SQLite in the same house style.
