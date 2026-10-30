# Thought Keeper — vault, artifacts and conventions

How the vault is laid out, what a source, an atom, a wiki page and a cluster are as **files**, and
which files are records (the truth) and which are caches (rebuildable). The domain reference is
`AGENTS.md`; the steps and their scheduling live in `pipeline.md`; the index schema lives in
`data-model.md`.

## 1. The vault

One location: `vault.root` (default `~/thought-keeper`). It is *your* knowledge: readable,
greppable, worth versioning and backing up. The app never treats a directory as a source.

```
<vault>/
├── index.db                    # SQLite: FTS5, derived counts (rebuildable)
├── inbox/                      # drop zone; the watcher intakes files into sources/
├── sources/                    # immutable, content-addressed
│   └── 7f/3a/
│       ├── 7f3a…<sha256>.pdf         # the object
│       ├── 7f3a…<sha256>.capture.md  # the readable rendering (step 1, PDFs)
│       ├── 7f3a…<sha256>.meta        # identity + title + tags + auto tags + step record + part records
│       ├── 7f3a…<sha256>.outline     # the node tree (step 2, JSON)
│       ├── 7f3a…<sha256>.summary.md  # the source's overview (derived from its claims)
│       └── 7f3a…<sha256>.log         # everything done to this source
├── wiki/                       # user-authored Markdown pages (source of truth)
│   └── <ab>/<cd>/
│       └── <id>.md             # id, title, tags, auto_tags, urls, source + body
├── atoms/                      # one claim per file (source of truth)
│   └── <ab>/<cd>/
│       └── <id>.md
├── clusters/                   # titleless associations (derived cache)
│   └── <ab>/<cd>/
│       └── <id>.meta           # id, atoms, signature, created, updated
├── vocabulary/                 # canonical names + what they absorbed (records)
│   ├── topics.md
│   ├── wiki-tags.md
│   └── source-tags.md
├── asks/                       # saved Ask conversations: <run-id>.md
├── .thought-keeper/            # derived, hidden: favicons/
├── .trash/                     # deletions (trash, not delete)
└── (.git/ if you version the vault — the app keeps .gitignore current)
```

The four directories answer four questions: *what did the source say* (`sources/`), *what did the
human write* (`wiki/`), *what do we now believe* (`atoms/`), *what travels together* (`clusters/`).
A cluster is never a destination: it exists to answer "which claims belong with this one?". A wiki
page is a destination, because a human authored it.

## 2. Sidecars

| Sidecar | belongs to | holds | kind |
| --- | --- | --- | --- |
| `<hash>.capture.md` | a source whose bytes are not readable (a PDF) or a wiki page representation | the content as readable Markdown | **cache** — re-derivable (`capture`) |
| `<hash>.meta` | a source | what the source **is** (kind, language, URL, identity), its title, **human tags**, **auto tags** and when they were derived, the state of capture/outline/distill and the part records | **record** — the file IS the truth |
| `<hash>.outline` | a source | the node tree with references into the readable text | **cache** — re-derivable (`rebuild-trees`) |
| `<hash>.summary.md` | a source | its **overview**, written from the claims after a complete distill | **cache** — regenerated from the claims (a model writes it, so not byte-identical) |
| `<hash>.log` | a source | everything done to it | **record** — history |
| `clusters/<ab>/<cd>/<id>.meta` | a cluster | its membership and signature | **cache** — re-derivable from the claims |

**The sidecar rule** — five sentences:

1. **Place and name.** `<parent>/<name><suffix>`; for a source the name is its hash, for a cluster
   its ULID.
2. **Exactly one owner.** A sidecar belongs to one source or one cluster; it is deleted with its
   owner.
3. **Never an object, never a document.** The object scan skips them; the atom scan skips `.meta`.
4. **One suffix, one meaning**, as a constant in one place (`entities/sidecar.go`).
5. **The primary artifact stays primary.** A sidecar never carries content that cannot be derived
   elsewhere — except for the *records*, where the file is deliberately the authority.

The **one exception** is the favicon store (`.thought-keeper/favicons/`): two sources of one host
share an icon, so it belongs to no single source.

The rendering is named after the step that writes it, and that name is doing real work:
`<hash>.capture.md` can never be mistaken for an object, while a plain `<hash>.md` could be — a
Markdown source, a URL snapshot and a PDF's rendering are all Markdown.

In a git vault the derived sidecars are excluded. `vaultgit` appends exactly these rules when
missing and never touches the user's own lines:

```
.thought-keeper/ · .trash/ · inbox/ · index.db · index.db-wal · index.db-shm
sources/**/*.capture.md · sources/**/*.text.md
sources/**/*.meta · sources/**/*.outline · sources/**/*.summary.md · sources/**/*.log · sources/**/*.log.1
clusters/**/*.meta
```

What stays in the history is the object, the wiki pages, the atoms and the vocabulary — the
knowledge. `.meta` and `.log` are records but are excluded anyway: they change on every run and are
rebuilt from the vault. `vault.git.enabled` and `commit_on_write` are **not implemented**; the app
never runs git.

## 3. Per-source log

`<hash>.log`, one append-only line per event, greppable, capped at 1 MiB (then rotated to `.log.1`).
It answers "what happened to *this* document?" and survives the run that produced it. It goes away
**with** the source.

```
2026-10-17T02:22:31Z run=01M53… level=info  phase=outline msg="extracted and tree built" kind=pdf pages=512 nodes=713 ocr=false
2026-10-17T02:24:00Z run=01M53… level=info  phase=distill msg="part start" part=4 "Chapter 4" nodes=11
2026-10-17T02:30:11Z run=01M53… level=warn  phase=distill msg="tool call failed" part=4 tool=read_source_node err="unknown node_id 6.3"
2026-10-17T02:41:02Z run=01M53… level=info  phase=cluster msg="cluster pass finished" new_clusters=5 attached=3 atoms=17 remaining=164
```

**The agent calls are in there too**: one line per model turn and one line per tool call. Every
OCR'd page gets its own line; dropped page furniture is logged; a deleted source signs off. A line
the cluster pass writes carries `scope=wiki` when it reports a vault-wide number.

`GET /api/v1/sources/{hash}/log` returns it; the PWA shows it in a **Log** tab of the source page.

## 4. Sources

- **A source is exactly one file — never a folder.** Intake takes single `.md`, `.pdf` or `.txt`
  files (or one URL). A directory path is rejected with `unsupported_source`.
- **The title and the original name are two different things.** The title is the document's;
  `original_name` is what the source came in under (a dropped file's name, or the URL). An empty
  `original_name` stays empty.
- **The title belongs to the document — the model names it.** Only if unavailable does it fall back
  to a declared title, the first heading, or the file name. The decision is logged.
- **Three classes of URL — one collection.**
  - A URL whose page yields enough text becomes an indexed **document**.
  - One that does not (a video, a JS app) becomes a **bookmark**: metadata only, never distilled.
    `add --bookmark` forces this class.
  - One that answers with a **wall** — a login (`401`/`403`, a sign-in redirect, a password form), a
    paywall (a teaser), a consent gate — becomes **blocked**: a snapshot that says what happened
    (`wall: login_required|paywalled|consent_required`), a red capture light with the reason, and
    outline/distill recorded `skipped`. A wall is never content. A source that was readable once
    **keeps its snapshot** when a later fetch is walled.
- **A sitemap and a feed are PARENTS, not pages.** Their snapshot is the list they declared (`sitemap:
  true` / `rss: true`); their children are ordinary URL sources with `origin: sitemap|rss` and
  `parent_hash`. A sitemap is a complete list (a refresh reconciles it 1:1 and deletes what left); a
  feed is a window on the newest items (a refresh only adds — a page that left it stays until the feed
  is deleted).
- **A feed may carry the post itself.** When an item has usable content (`content:encoded`, Atom
  `content`, or a description long enough), that copy becomes the page's snapshot: frontmatter
  `from_feed: true`, `feed`, `feed_hash`, `captured_at` (no `fetched_at`/`http_status` — the page was
  not fetched), and the source's record carries `from_feed`/`feed_hash` (identity, so a reindex keeps
  it). This is how a page that only resolves with JavaScript is readable without a browser. The page
  is still tried on the weekly refresh; a readable page replaces the copy and clears the marker, an
  unreadable one leaves it.
- **Ownership can be released.** A URL the human adds by hand wins over the page/sitemap/feed that
  claimed it first: the record's `origin`, `wiki_page` and `parent_hash` are cleared, the source stays
  as it is, and the owner's next reconciliation no longer touches it.
- **The human may attach the page.** `POST /api/v1/sources/{hash}/attach` (or
  `thought-keeper attach --url URL --file FILE`) converts a saved `.html`, `.pdf`, `.md` or `.txt`
  into a URL source's snapshot at the same address (readability, poppler/OCR, or the text as it is)
  and re-arms outline/distill. It works for a **blocked** URL and for a **bookmark** (a page that only
  renders in a browser, a video, an app). A feed or a sitemap is refused: its snapshot is a list, not
  a page. The snapshot's frontmatter carries `attached: true`,
  `attached_from`, `attached_sha256` and `attached_at` instead of `fetched_at`/`http_status`; the
  source's record carries `attached` (identity, so a reindex keeps it), and its metadata carries
  `attached`/`attached_from`/`attached_hash`. **The automatic refresh never fetches an attached
  source** — a login page is not a correction of the article. The original file is not stored: like a
  fetched page, the vault keeps the readable snapshot, and the receipt names the file it came from.
- **An attachment is replaced or retried explicitly.** `POST /sources/{hash}/attach` with a new file
  replaces the snapshot (and resets the old derivation). `POST /sources/{hash}/refetch` fetches the
  URL once: a readable 2xx with text replaces the snapshot and clears the attachment marker in the
  record/index/identity (a changed text also resets the derivation); a wall, a dead host or an empty
  page keeps the attached bytes and marker exactly as they are. `Reset` remains derivation-only and
  never replaces an attachment.
- **Favicons.** A URL source stores the site's icon content-addressed under
  `.thought-keeper/favicons/`, served at `GET /api/v1/sources/{hash}/favicon`.
- **Name = SHA-256 of the bytes**, sharded `sources/<ab>/<cd>/<full-hash>.<ext>`. Identical bytes
  collapse: re-intake is a no-op.
- **A URL's address is `sha256("url:" + url)`**, not the hash of its content: the page changes, the
  address must not. Every fetch writes through it; `POST /api/v1/urls/migrate` moves a vault written
  with the old first-snapshot address.
- **A captured source stays readable next to its object.** `capture` writes `<hash>.capture.md` for
  a kind that has to be extracted. It is what **outline** reads.
- **Kinds:** `.md`, `.pdf`, `.txt`; URL snapshots are `.md` with frontmatter.
- **Sources are never modified.** A correction is a new file → a new hash. The deliberate
  exceptions are a URL snapshot (refreshed in place, or given the page the human attached) and a
  wiki page's representation (stable `WikiHash`, written through on every edit).
- **A source may exist without an object**: a wiki page's link whose fetch has not answered yet.
  The record is then the whole truth about it; `reindex` and `reconcile` treat it as a link, not as
  damage.

## 5. Atoms

An atom is the smallest unit of knowledge: **one claim**. Atoms are the Zettelkasten and the source
of truth.

```markdown
---
id: 01J8ZC5V9P0R7T2QK4W6Y8B3ND
claim: Caching is only safe when the key covers every input.
topic: caching
type: claim
status: active
sources:
  - hash: 7f3a1c…<sha256>
    node: "2.1"
    ocr: false
created: 2026-10-16T09:00:00Z
updated: 2026-10-16T09:12:00Z
---

The body expands the claim in one to three sentences. Syntax quoted from the source stays in inline
code: `cache_key[0]`.
```

| Key | Owner | Meaning |
| --- | --- | --- |
| `id` | service | ULID; the atom's identity |
| `claim` | agent | One sentence: the claim itself |
| `topic` | agent | The FLAT subject of this one claim |
| `type` | agent | `fact` \| `definition` \| `claim` \| `example` \| `counterpoint` \| `quote` |
| `status` | agent | `active` \| `superseded` \| `disputed` |
| `sources` | service | `{hash, node, ocr}` — the source node(s) this claim came from |
| `created` / `updated` | service | Server clock |

### Rules

- **One claim per file.**
- **A claim is written in the configured claim language** (`language.claims`, default English),
  whatever the source's language. Proper names, titles, code and quoted terms keep their original
  script.
- **A claim stands alone.** No "Chapter 11", "this section", "the book", "as we saw". The source
  stays provenance; it is never part of the statement.
- **The topic comes from the claim and body.** It is the flat subject, in the same language,
  derivable without any source. `chapter-11`, `book-structure` and `rust-book` are not topics.
- **A write that cannot be recorded leaves nothing behind.** The FILE is written first and the index
  row second; if the row fails, the file is removed.
- **A copy is not a claim.** Claims with the same topic, statement, provenance, body, type and
  status are indistinguishable; `POST /api/v1/atoms/collapse` keeps one. A copy a cluster owns wins.
  The check reads the **index** (that identity is indexed; `StreamClaimIdentities`); only the hashes
  that actually collided are loaded as files, because a collapse has to trash them. Walking the atom
  files to ask "are there duplicates?" decoded the whole vault for a 200-byte answer.
- **An atom is addressed by its ID, and the topic is a FIELD.** `atoms/<ab>/<cd>/<id>.md`, sharded by
  the tail of the ULID. A topic rename moves no file.
- **Atoms cite source nodes, not whole sources.** A node not in the source's outline rejects the
  write; a claim that names none inherits the part it was distilled from.
- **`ocr: true` when the text came from OCR.**
- **A claim has NO links.** The body is Markdown prose; a `[[…]]` pair in it is syntax quoted from
  the source (a C attribute, a shell test, a grammar fragment), never a reference. A claim's
  associations are its cluster, and nothing parses a body into edges.
- **Code is quoted as code.** A declaration, expression, literal, identifier or grammar fragment from
  the source goes in the body as inline code; `[[unsequenced]]` is one. In the claim sentence the
  code is named in prose, not quoted raw. The save wraps a bracket pair in a code span, and a body
  written before that rule is repaired once with `POST /api/v1/atoms/quote-code`.
- There is no generated atom index: the catalog is SQLite's job (`list_topics`, `list_atoms`,
  `search`).
- **The cluster membership is not stored beside the atom.** It is in `clusters/` and `cluster_atoms`.

## 6. Wiki pages

A wiki page is the one artifact a human authors. It is Markdown, addressed by a **ULID**, and lives
in `wiki/<ab>/<cd>/<id>.md`:

```markdown
---
id: 01J8ZC5V9P0R7T2QK4W6Y8B3ND
title: Go Concurrency
tags:
  - go
  - concurrency
auto_tags:
  - concurrency
urls:
  - https://go.dev/blog/waza-talk
source: <stable WikiHash>
created: 2026-10-22T09:00:00Z
updated: 2026-10-22T09:12:00Z
---

# Channels

Channels connect goroutines.
```

- **The page is the truth.** Its body, title and tags are the human's; the source representation is
  derived from it and may be rewritten in place.
- **The source keeps a stable address.** `entities.WikiHash(id)` is the source hash, the way
  `URLHash(url)` is a URL's. An edit writes the new bytes through that address, clears the old
  derivation, re-arms capture and lets the ordinary pump run capture → outline → distill. A second
  source is never filed beside it.
- **Tags live in frontmatter**, not in prose. They are normalized like topic names; the Wiki lens
  shows them as a tag cloud (tag + page count) and lets a reader filter the page list.
- **The app TAGS a page, the human may too — and the two are kept apart.** A save queues a `tag` run
  (its own op and worker, never on the request path): the model reads the page's title and prose and
  writes 3–6 coarse tags into `auto_tags`. `tags` stays the human's, so a tagging run never
  overwrites what the reader wrote, deleting a derived tag sticks (the next run replaces exactly
  that key), and a save keeps both. Both kinds are shown, counted in the cloud and usable as a
  filter — the derived ones are marked. A tag is a SHELF (above a claim's topic), so the prompt asks
  for coarse names and offers the tags already in use for reuse.
- **References use the ID and a prefix:** `[[wiki:<id>]]` opens a page; `[[source:<hash>]]` opens a
  source. Both accept the ordinary `|alias` form. The detail views show the full ID with a copy
  button.
- **URLs in a page are sources — and a save never fetches or deletes them.** `core/markdown.URLs`
  extracts http(s) links from authored prose (code fences and inline code are ignored). Saving a page
  writes the page and its source representation and **returns**: the new links are handed to the
  app's URL queue, which fetches them in the background, and a background `PruneWikiLinks` pass
  reconciles the link sources the page owns with what it declares. Fetching inside the request made
  one page with 154 links take three and a half minutes, with nothing on disk until the last link
  answered; deleting inside the request made a page that dropped three links take 73 seconds, because
  every delete walked the whole atom vault.
- **A link is a source before it has bytes.** A new link is *claimed* immediately (`QueueURLs`): a
  row and a `<hash>.meta` at the URL's stable address, `origin: wiki`, a pending capture light, and
  outline and distill recorded `skipped` ("not fetched yet") so nothing downstream waits for text
  that is not there. The fetch fills the SAME row (`FillURLSource`) — it never files a second source
  — and re-arms the derivation. A fetch that fails leaves the row in place with a **red capture
  light and the reason** in the step record, the source's `meta` and its log: a dead link is part of
  what the page claims, so it stays listed (and a source that already exists — failed included — is
  only associated, never fetched again; the retry is `POST /api/v1/refresh-urls`).
- **Removal is tracked, not guessed.** After every save a BACKGROUND pass (`PruneWikiLinks`, behind
  the exclusive gate) compares the link sources the page owns with what the page declares, and deletes
  one only when it was created by a wiki page (`origin: wiki` in the source record) and no other page
  still lists it. The pass re-reads the page instead of trusting a diff: a link re-added before it
  runs is declared again and its source stays, and a save whose cleanup was lost is healed by the next
  one. A URL collected independently is left alone. The wiki page source itself is re-derived;
  existing URL sources are not re-interpreted. The snapshot-stable keys (`language`, `origin`,
  `wiki_page`, `parent_hash`, `sitemap`, favicon references, `content_hash`, `attached`) survive
  every meta write — a snapshot rebuild must never un-own a link or turn a human's attached article
  back into a page the next refresh may overwrite. They are carried forward by the writer (the
  snapshot JSON is merged into the row's meta, never rebuilt from scratch), and the consistency job
  restores them from the record when a row written before that rule lost them.
- **A sitemap is a source that owns the pages it declares.** When a fetched URL parses as a
  `<urlset>` (or a `<sitemapindex>`), its snapshot is a readable page list and the source is marked
  `sitemap: true`. New URLs become ordinary source rows claimed with `origin: sitemap` and
  `parent_hash: <sitemap hash>`, then fetch in the background. A child sitemap expands the same way;
  ownership forms a tree.
- **A dependent source cannot be deleted on its own.** A source with an `origin` (wiki or sitemap)
  is refused by `DELETE /api/v1/sources/{hash}` with `dependent_source`; its owner decides. Deleting
  the owner removes the whole subtree: a sitemap cascades through its pages and child sitemaps, and
  a wiki page removes the links it brought in (unless another page still lists them).
- **Wiki pages are a lens, not a cluster.** The sidebar has **Wiki** below **Claims**. Clusters stay
  titleless and are never listed.

## 7. Clusters

The association layer. A cluster is **titleless by design** and is **never a destination**:

```json
{
  "id": "01J8ZC5V9P0R7T2QK4W6Y8B3ND",
  "atoms": ["01J8Z…", "01J8Z…"],
  "signature": "…",
  "created": "2026-10-16T09:00:00Z",
  "updated": "2026-10-16T09:12:00Z"
}
```

| Key | Owner | Meaning |
| --- | --- | --- |
| `id` | service | ULID; the cluster's identity |
| `atoms` | graph | the claims it groups |
| `signature` | service | stable hash of the sorted member ids/claims |
| `created` / `updated` | service | server clock |

- **A cluster is a group of claims, not a document.** No page body, no title, no kind, no tags, no
  `.dirty` marker, no successor chain. The shard is the tail of the ULID, exactly as for atoms.
- **It is a CACHE.** The claims are the truth and the graph over them is deterministic, so a reindex
  or a new pass can rebuild the whole directory from the claims. `reindex` reads the records (the
  cheap source for membership) and rewrites them when a member vanished.
- **Membership is queried, not navigated.** `cluster_atoms` is the child table; `SaveCluster` is
  authoritative for it (replace-shape). The Claims detail view reads a claim's cluster peers; there
  is no cluster list, no cluster page and no sidebar entry.
- **Split and merge are membership changes.** A structural change is a new membership set; a record
  that loses every member is removed. No id is linked to from outside the cluster layer, so nothing
  needs a redirect.

The derivation itself (prune, attach, graph, split, overlap, write) is described in
`pipeline.md → The cluster step`.

## 8. Vocabulary records

`vocabulary/topics.md`, `vocabulary/wiki-tags.md` and `vocabulary/source-tags.md` hold three
namespaces:

- **topics** — the canonical names of claim topics, the aliases they absorbed, and the **broad
  tokens** learned from this vault (used only by the near-duplicate detector).
- **wiki-tags** — the canonical names of page tags, and their aliases.
- **source-tags** — the canonical names of the labels on sources (the human's and the agent's auto
  tags), and their aliases. It is kept apart from `wiki-tags`: a page and a source are filed
  differently, even when the words overlap.

They are records, not caches: a reindex reads them, and they belong in a versioned vault. A source's
human tags live in its `<hash>.meta` record (and are mirrored into the index); the agent's **auto
tags** live there too, with the time they were derived. A curation that merges two labels rewrites
the records, never a claim or a page. Auto tags are derived from the claims: a full reset clears them
(and their timestamp), a complete distill writes new ones, and a reindex restores them from the
record — it never triggers a model call per source.
`POST /api/v1/vocabulary/curate` merges names, `POST /api/v1/vocabulary/retopic` re-derives claim
topics, and both are renames — no claim text is rewritten and no cluster moves.

## 9. Ask records

Every successful query writes `asks/<run-id>.md`: YAML frontmatter plus the answer body. The id is
the query run's id, so an answer stays traceable to its run and tool steps.

```markdown
---
id: 01J8ZC5V9P0R7T2QK4W6Y8B3ND
question: What does this chapter say about caching?
tags:
  - caching
source: 7f3a1c0000000000000000000000000000000000000000000000000000000000
created: 2026-10-22T09:00:00Z
updated: 2026-10-22T09:00:00Z
---

Caching is safe only when the key covers every input.
```

| Key | Owner | Meaning |
| --- | --- | --- |
| `id` | service | ULID; the query run's id |
| `question` | human | the question as asked |
| `tags` | agent | automatic labels derived from the cited topics/sources |
| `source` | service | the hash of the ONE source the question was scoped to; omitted for a vault-wide answer |
| `created` / `updated` | service | server clock |

- **The answer body is ordinary Markdown.** The model's `[[claim:…]]` / `[[source:…]]` form is
  normalised to real links when the record is written (and repaired by `Normalize`).
- **A scoped ask is a record of its boundary.** `source` is written from the run's scope; opening
  the answer shows the reference again, and asking it again uses the same source.
