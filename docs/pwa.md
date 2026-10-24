# Thought Keeper — the PWA

The UI is a Svelte 5 single-page app served by the embedded Go service (same origin, hash routing).
There is one window rule and one UI: the installed PWA. `internal/web/dist` is a **build output** —
never edit it there.

## 1. The lenses

The sidebar is a small, finite set of lenses:

| Lens | What it shows |
| --- | --- |
| **Claims** | newest claims and the busiest claim topics; a local FTS5 finder and the search palette; one claim opens its detail with provenance, cluster peers and backlinks. The topic cloud stays while a topic is selected: the active chip is highlighted, clicking it again (or the “Filtered by” chip) clears the filter. Opening a claim keeps the filter in the URL (`#/claims/<id>?topic=…`), so the list beside the detail stays filtered — and a claim link inside the detail does too |
| **Wiki** | user-authored pages, their tag cloud and tag filter; create, edit, delete |
| **Sources** | list with a persistent search/type/tag filter — tags appear as chips in every row and as a tag cloud above the list (human and derived, counted once per source) — add (path/upload/URL/RSS feed), one source page with content/PDF/live/log/files, summary, sub-sources and a **Tags** editor in the rail; a URL whose capture is blocked (a login, a paywall) **or that became a bookmark** (a page that only renders with JavaScript) offers **Attach snapshot…** there — save it in the browser, print it to PDF for a JS page, and attach it; a page whose snapshot came from a feed's copy shows a **feed copy** tag and the copy's origin in the rail; a sitemap or feed and its pages show their relation — a child links back to its parent in the rail — and an owned page has no Delete action. A **feed or a sitemap is a list**: its page shows capture green and the other three lights grey ("does not apply"), offers **Refresh** instead of Capture/Outline/Distill/Reset, and names itself `feed · the items are sources` / `sitemap · the pages are sources`. **Deleting returns to the list at once**: the row leaves optimistically while the server finishes (a delete waits for running document/cluster work), and a failure reloads the list |
| **Attention** | failed source steps and non-info lint findings — a lens over existing state, not a second health model |
| **Bookmarks** | collected links: URL sources that exist as metadata only |
| **Ask** | saved queries with their automatic tags; an answer appears only on its own detail, whose scroll position survives a visit to a cited claim |
| **Status** | counts, job queue, summary backfill (missing/queued/running), maintenance actions, vocabulary health, memory (live heap and peak) |

A cluster is **never** a lens and never listed. The Claims detail shows the claim's cluster peers and
the claims under the same topic **as two separate sections**;
that is the only place the association becomes visible. The bundled status views are calm and
Notion-flavored: the data is dense, the chrome is quiet.

Saved queries carry **automatic tags** — the topics of the claims their answer cites, or the
meaningful words of the question when it cites none — and the Ask list shows them as a tag cloud.

## 2. Routes and state

- **Hash routing; the URL is the truth about UI state.** `#/claims`, `#/claims/<atomId>`, `#/wiki`,
  `#/wiki/<id>`, `#/sources`, `#/sources/<hash>`, `#/ask`, `#/status`, plus `#/attention` and
  `#/bookmarks`.
- **The back button on a source page returns to the Sources list at the offset the reader left it**
  (the list is one page scroller; the new list entry is seeded with that offset).
- **A source page is addressable per tab and part:** `?tab=snapshot|pdf|live|log|files|summary|children`
  and `?part=<nodeId>` / `?file=<name>` are query parameters, so back/forward returns to them and a
  link can be shared.
- **Dialogs and overlays are query parameters too** (`withParam` / `withoutParams`), so closing an
  overlay does not lose the reader's place.
- **The theme toggle has three states**: light / dark / auto (follows the system), persisted in
  `localStorage`.
- **A second window focuses the first** (BroadcastChannel election).
- **Lists are flat indexes.** Claims, Wiki, Sources, Ask and Bookmarks share one row grammar
  (leading icon, title, secondary line, trailing metadata/actions). Grouping — a host in Bookmarks,
  a tag in Ask — is a section header, not a card; framed cards belong to detail pages.
- **Long lists are windowed, not paginated.** The five library lists mount only the rows in view
  (with a small overscan) and keep a spacer for the rest. No extra page is loaded; the DOM stays
  bounded while the scrollbar still represents the whole list.
- **Split views keep the list in place.** Claims, Wiki and Ask show a sticky navigation pane beside
  a framed reading surface. The reading content scrolls with the page, so the scrollbar stays at
  the window edge and the list does not move; scrollable clouds and lists fade at their scroll edge.
- **Scroll follows the history entry, like a normal page.** The shell and reading content, the
  sticky list panes, and the scrollable clouds record their position in the current history entry.
  Back and forward restore it; a direct link or a new page starts at the top. A list that stays on
  screen keeps its place when only the reading pane changes.

## 3. Event-driven updates

One SSE connection (`GET /api/v1/events`) replaces polling. Event types:

`run` · `source` · `wiki` · `clusters` · `findings` · `urls` · `pipeline` · `progress` · `log` ·
`vocabulary`

- **The UI patches only its containers.** State events coalesce per type and are never dropped;
  `progress` coalesces per source; `log` is a bounded live tail.
- **A frame is never handled concurrently with another.** Frames are queued, folded, and each
  affected container is reloaded once; every loader carries a **generation**, so a slow response
  cannot overwrite a newer one.
- **The stream heals itself.** EventSource is recreated when CLOSED; every return to the window
  (visibility/focus) is a catch-up point and a resync.
- **Writes that happen while no frame is received are published anyway**: the server's
  `publishWrites` middleware tells every connected client after a synchronous write, so no window
  keeps a stale picture until reload.
- **A source's live counts win over the last API snapshot** while a run is in flight (claims written,
  parts done, queued-ahead), so a list row and the detail overlay cannot disagree.

## 4. Empty states and honesty

- **Not being connected is said where the reader is looking.** The empty states answer with
  `Nothing loaded` while the connection is not live — "No sources yet" would be a claim the UI
  cannot make offline.
- **A question is asked in the app's own dialog**, never a native one.
- **A list read carries everything a SAVE replaces**, so an edit form never starts from a partial
  record.
- **The sidebar badges are vault-wide numbers** from `/status` (`sources`, `bookmarks`, `failed`,
  `findings`), never counts of the page of a list that happens to be loaded. A badge derived from the
  loaded rows grew with every page and moved when an unrelated lens refreshed — a "1" that became 15.
- **The source list is published once per load.** Every page is read first and the complete list is
  assigned in one step; the previous list stays on screen while a refresh runs. That is what keeps a
  filtered list from briefly matching nothing and a refresh from looking like an empty vault.
- **A read in flight is joined, not restarted.** Later requests (an event burst, a tab switch) wait
  for the running read and ask for one trailing read instead of starting a competing one; for the
  open source the same rule applies to its detail. Only a switch to ANOTHER source supersedes a read.
  Restarting on every event starved the one read that would fill the screen — the lens stayed on its
  skeleton and a source would not open until a full reload.
- **A summary event is a sidecar event.** A completed summary refreshes only the open source's
  overview; it does not reload the source list, the claims or the detail. A backfill of hundreds of
  summaries therefore costs one small read per viewed source, not a list read per summary.

## 5. Content view

- A PDF source shows its **readable rendering** (the text step 1 extracted) in the snapshot tab; the
  PDF itself is one tab away. The rendering is capped (a preview for tens of pages) and the raw file
  is always reachable.
- Every derived file of a source is readable in the **Files** tab: the step record, the outline, the
  summary, the log, the object. The tree is the table of contents; clicking a node opens that part of
  the text.
- A source's log is shown verbatim in the **Log** tab.
- **A URL source's address is clickable**: the frontmatter renders its `url:` as a link, the Details rail
  shows the host as one, and the header carries **Open URL** — all open the page in the browser
  (a new tab), distinct from the **Live** tab, which embeds it.
- The **Summary** tab renders `<hash>.summary.md`: the overview written after a complete distill. A
  missing file is an empty state with **Generate summary** (and the reason it is not available yet
  when the derivation is still open); the consistency job fills missing ones in the background.
- The **Sub-sources** tab lists the sources this one owns — a sitemap's pages, a wiki page's linked
  URLs. It appears only when the source has children (or is a sitemap), and a row opens that source.
- A source carries **human tags**, edited in the rail (comma-separated), and the agent's **auto
  tags** (dashed chips, read-only, derived from the claims after a complete distill). Both are
  observed by the `source-tags` vocabulary alongside `topics` and `wiki-tags`, and the Sources filter
  matches either.
- **Every text field carries a clear (×) button** (`lib/clearable.ts`). It is hidden while the field is
  empty, including after a programmatic clear, and leaves checkboxes, file pickers and other
  non-text inputs alone.

## 6. Assets and icons

- **Never generate the project logo with AI**: `design/pictograms/thought-keeper.svg` (in the
  studio-page repo) is the source of truth.
- The app icons are the pictogram reframed as a squircle: `web/icons/pictogram.svg` is the artwork
  source, `web/scripts/render-icons.mjs` derives the rest (`task icons`).
- `web-next/public/sw.js` precaches the shell (document, manifest, icons) and is **network-first** for the
  document. The API is deliberately **not cached**: this app *is* a local service, and a cached
  answer about the vault would be a lie exactly when the service is down.
- Bumping a precached asset means bumping the cache name in `web-next/public/sw.js`; a new build needs no
  bump (hashed assets change names).

## 7. Development

```bash
task web:install   # npm install the next PWA toolchain
task web:dev       # Vite dev server with HMR, proxies /api to the running app
task web:check     # svelte-check + TypeScript
task web:test      # unit tests
task web:build     # build into internal/web/dist (embedded by go:embed)
```
