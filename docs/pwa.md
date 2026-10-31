# Thought Keeper — the PWA

The UI is a Svelte 5 single-page app served by the embedded Go service (same origin, hash routing).
There is one window rule and one UI: the installed PWA. `internal/web/dist` is a **build output** —
never edit it there.

## 1. The lenses

The navigation is a small, finite set of lenses. In the **wide layout** it is the sidebar; in the
**narrow layout** the sidebar gives way to a **footer navigation**: the same lenses, one tap away,
with an Attention badge and a **connection mark on Status** — green online, orange reconnecting,
grey while connecting, red offline; the Status tab's label/tooltip names the state too. There is no
drawer and no menu button — nothing covers the page, and the reader never has to dismiss a panel.
**Every head keeps one title row** at every width: the heading, an optional count and the page's
action, with the action right-aligned and the title truncating rather than the row wrapping — so it
keeps one place in every language, and the narrow layout needs no extra line for it. A subtitle, when
a page has one (Start, Ask, Status), sits below the row and may wrap without moving the heading or
the action. Narrow list rows also drop their action buttons (the detail carries them, **icon-only**),
and the Sources list trades the four step lights and the status line for **one lamp**: its ring is the
slower progress dimension, its colour the aggregate state. The long **feed item** / **feed copy**
labels shorten to **item** / **copy** in a narrow window, with the full label in the tooltip, so a
feed row keeps its title.

| Lens | What it shows |
| --- | --- |
| **Start** | the front door, not a data dump: an **Ask** box with the three latest questions, **Read later** (the three OLDEST open queue entries with the interest that queued each, “done” per row and the way into the interests manager — its empty state is a quiet line, never a guilt box), **Continue** (the last sources, claims, wiki pages and questions opened; local memory, not a vault record), **Needs you** (failed sources, findings, unclustered claims — quiet when there is nothing), the five newest sources with their live step state, and one calm vault-pulse line; a new vault gets “add the first source” here |
| **Sources** | list with a persistent search/type/tag filter — tags appear as chips in every row and as a tag cloud above the list (human and derived, counted once per source) — the reader's two marks as filters: **Read later** (the open queue, oldest entry first, with a banner naming how much came from interests and the per-interest chips) and **★ Favorites**, plus the **Interests** button; a row carries a filled star, a filled clock and — while it is queued — one interest chip with the rule's name. Add (a path or an upload of a Markdown, PDF, TXT or **EPUB** file, a URL, an RSS feed; the overlay names those kinds and its picker offers exactly them), one source page with content/PDF/live/log/files, summary, sub-sources and a **Tags** editor in the rail; the head carries **Favorite** and **Read later** beside **Ask**, and the head's **Ask** action opens Ask with the composer scoped to this one source; the rail has an **Interests** block with the evidence and the two corrections (`✓ passt`, `× nicht dieses Interesse`); a URL whose capture is blocked (a login, a paywall) **or that became a bookmark** (a page that only renders with JavaScript) offers **Attach snapshot…** there — save it in the browser, print it to PDF for a JS page, and attach it; an attached snapshot offers **Replace snapshot…** and **Try the URL again** (a readable page replaces it, a wall keeps it) and carries a paperclip **attached** tag in the list, where the **Attached** chip filters exactly those sources; a page whose snapshot came from a feed's copy shows a **feed copy** tag and the copy's origin in the rail, and — like a source whose last fetch was walled — offers **Refresh** in place of Capture (the page is tried; the richer text wins; a wall, a failure or a poorer page keeps the copy). Every other URL page is refreshed by the background cadence and has no manual Refresh; a sitemap or feed and its pages show their relation — a child links back to its parent in the rail — and an owned page has no Delete action. A **feed or a sitemap is a list**: its page shows capture green and the other three lights grey ("does not apply"), offers **Refresh** instead of Capture/Outline/Distill/Reset, and names itself `feed · the items are sources` / `sitemap · the pages are sources`. **Deleting returns to the list at once**: the row leaves optimistically while the server finishes (a delete waits for running document/cluster work), and a failure reloads the list |
| **Wiki** | user-authored pages, their tag cloud and tag filter; a **★ Favorites** filter and a star in every row and in the page head; create, edit, delete |
| **Bookmarks** | collected links: URL sources that exist as metadata only; newest first, each row names its host (the list is a window, not a host-grouped dump) |
| **Claims** | newest claims and the busiest claim topics; a local FTS5 finder and the search palette; one claim opens its detail with provenance and cluster peers. The topic cloud stays while a topic is selected: the active chip is highlighted, clicking it again (or the “Filtered by” chip) clears the filter. Opening a claim keeps the filter in the URL (`#/claims/<id>?topic=…`), so the list beside the detail stays filtered |
| **Ask** | saved queries with their automatic tags and row actions (open, ask again, delete); a question **appears in the list at once with its status** while it is answered — no separate “thinking” box — and questions run in parallel, so the reader can ask the next one without waiting; an answer appears on its own detail with its **Ask ID** (copy button), and its scroll position survives a visit to a cited claim or source; the answer is written in the question's language and may cite claims and source overviews. A question asked from a source keeps **one source reference under the input inside the composer**; that source is the only evidence the answer may use, and removing the reference widens the next question to the vault. A saved answer remembers its source and “ask again” keeps the same scope |
| **Attention** | failed source steps and non-info lint findings — one search narrows both; a lens over existing state, not a second health model |
| **Status** | counts, job queue, summary backfill (missing/queued/running), maintenance actions, vocabulary health, memory (live heap and peak) |

**The five library lists share one skeleton.** A head carries the title, its count and the lens's
single action — Sources: add source, Wiki: new page, Bookmarks: add link, Attention: vault status;
Claims has none because claims are the agent's. Below the head comes a full-width search field; the
lens's extra filters follow (Sources: kind, **Read later**, **★ Favorites**, **Interests** and
attached) and then its chip cloud (Sources tags, Wiki tags, Claims topics). **Every count cloud
folds its long tail**: it shows the 18 most-used names (ties alphabetically) and leaves the rest
behind a dashed `+N more` chip below the scroller; the active filter is always among the visible
chips, a click expands the complete set in place (`Show less` folds it again), and a cloud with 24
entries or fewer is left untouched. The Ask tag cloud follows the same rule. Bookmarks and Attention
have neither filters nor a cloud. The Sources queue, when the address says `?readlater=1`, adds its
**banner** above the search: the title, the open count, “4 from your interests · 3 yourself”, the
per-interest chips and the way into the manager. No list describes
itself in prose; only Claims keeps a caption, its “newest 50 of …”.

**A list that has scrolled past about one screen offers a scroll-to-top button** in the lower right of
the content area. It follows whichever container the reader moved — the page, or the list pane of a
split view — so the wide and narrow layouts need no separate case, and on the narrow layout it sits
above the footer navigation. A short list never shows it.

The reader's own marks are **not lenses**: a favorite is a star and a lens filter, the read-later queue is a filter of the Sources list with a banner and a Start card, and interests are standing rules managed in an overlay (`?interests=1`) — one source's evidence sits in its rail, the queue rows say which rule queued them, and a source can seed a draft from its topics and labels (`&seed=<hash>`; a source address, never free text). There is no “Saved” lens and no “Interests” lens.

A cluster is **never** a lens and never listed. The Claims detail shows the claim's cluster peers and
the claims under the same topic **as two separate sections**;
that is the only place the association becomes visible. The bundled status views are calm and
Notion-flavored: the data is dense, the chrome is quiet.

**The source lists are windowed.** Sources, Bookmarks and Attention read only the rows around the
viewport (± overscan); every filter (`q`, `kind`, `tag`, `attached`, `bookmarks`, `failed`) is part
of the server query, and the filtered **count** comes with the page, so the scroller spans the whole
list without loading the rows in between. Rows that have not arrived render as placeholders at the
right height. The Sources tag cloud is a **facet** over the vault (`/sources/facets`), never a count
over the loaded window. Home reads only the five newest sources.

The top bar is a **tool bar, not a breadcrumb**: pages carry their own title, and the bar holds the
global search over claims and sources (a compact command button in the browser, a full-height
title-bar segment in the installed PWA, and an icon beside the utilities in the narrow layout), the
activity chip, and the **interface language** switch beside the theme switch. In the narrow layout
the bar's left side is only the logo and “Thought Keeper”, because the sidebar is gone. The language
is the reader's choice and is remembered per browser; the server's `language.ui` is only the default
for a reader who has not chosen one (the app ships English and German). The claims and overviews
follow `language.claims` / `language.summary` instead — a vault can be read in one language and
written in another. **Numbers follow the interface language too**: counts are grouped (`1,234` in
English, `1.234` in German), including numbers interpolated into a sentence; an identifier such as
the PID is
passed as a string and stays ungrouped, and a byte size uses the language's decimal separator.

Saved queries carry **automatic tags** — the topics of the claims their answer cites, the tags of
the sources it cites when no claim is cited, or the meaningful words of the question — and the Ask
list shows them as a tag cloud.

The field sends what the reader is looking at with the request, so a claim or a source can be
referenced as "this one" without typing its id; on a claim's page the field offers "the sources of
this claim" as a starting point. The wiki honours an assistant result too (`?command=`) and shows
the record's pages in the record's order, with the same banner; typing in its search field takes the
filter back. **The assistant is a command line, not a chat.** A permanent button in the top bar (`⌘J`) opens one
field: say what to show, and the app resolves it to a lens, a filter and a set. It is not a
conversation — one request, one GUI state — and it is not a second brain: the request is read once
and the result is an ordinary list. While a command is in flight the button pulses, including when
another window or the CLI sent it. A list that is an assistant result carries a **banner** above the
rows: the utterance, the criteria the request was actually read as (`tag cloud · added ≤ 7 days ·
not finished`), the count, "run again", "keep as a filter" (only when the reading has named facets)
and a close button. The same banner appears when an old request is opened again — it is the same
address, so it is the same view. The Start lens lists the last few commands; there is no history
lens, and no command is kept forever.

## 2. Routes and state

- **Hash routing; the URL is the truth about UI state.** `#/home` is the default (and the Start lens),
  followed by `#/claims`, `#/claims/<atomId>`, `#/wiki`, `#/wiki/<id>`, `#/sources`,
  `#/sources/<hash>`, `#/ask`, `#/ask/<id>`, `#/ask?source=<hash>`, `#/status`, plus `#/attention` and `#/bookmarks`.
  The reader's marks are NAMED filters: `#/sources?readlater=1` (the open queue, oldest first),
  `#/sources?readlater=1&interest=<slug>` (one rule's share), `#/sources?favorite=1`,
  `#/wiki?favorite=1`, and the interests manager is an overlay (`?interests=1`, optionally
  `&seed=<source hash>` to prefill a draft from a source) on any route. Free text never enters an
  address, as everywhere else.
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
- **The assistant's field is an overlay too** (`?assistant=1`), so closing it never loses the
  reader's place.
- **An assistant result has its own kind of address.** A command that named facets hands out those
  facets beside its id (`#/sources?tag=cloud&kind=pdf&command=…`); a command that searched by words
  hands out only the id (`#/sources?command=…`) — free text never enters the address, because an
  address that carries a question is a different address on every visit. The id is a real filter
  dimension: the server reads the materialized set from the record, so the windowed list keeps its
  count, its pages and its scroll height exactly as with a tag filter.
- **That address expires.** `assistant.retention` (a week by default) prunes the records on its own
  daily cadence (`assistant.prune_interval`), so a bookmark on an assistant result dies with it —
  and says so. "Keep as a filter" is the escape: it
  drops the id and leaves an address that means the same thing forever.
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
`vocabulary` · `interests`

- **The UI patches only its containers.** State events coalesce over a short window (150 ms) into a
  set of scopes, and each scope is one container read — so the `source`, `clusters` and `run` frames
  of a single run become one batch, and no list is loaded twice in it. `progress` coalesces per
  source; `log` is a bounded live tail.
- **A scope refreshes only what the open view can show.** A run that finishes while the reader is on
  Status does not re-read the claim list or a hidden source list; a cluster pass re-reads the open
  claim's peers and the vault report, not the claim text or the topics. Hidden containers with a
  lazy view entry point (the claim list and the topic cloud) are marked **stale** and reload when
  their view opens; the source list, findings and the vault report load on entry anyway.
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
- **A `command` frame is an action, not a state.** It is keyed by the command's id so two commands
  cannot coalesce into one, and every open window follows the URL it carries — once. Replaying the
  same frame (a reconnect) cannot move the reader twice, because the store remembers which command
  it already followed.

## 4. Empty states and honesty

- **Not being connected is said where the reader is looking.** The empty states answer with
  `Nothing loaded` while the connection is not live — "No sources yet" would be a claim the UI
  cannot make offline.
- **A question is asked in the app's own dialog**, never a native one.
- **A list read carries everything a SAVE replaces**, so an edit form never starts from a partial
  record.
- **The sidebar badges are vault-wide numbers** from `/status` (`sources`, `bookmarks`, `failed`,
  `findings`), never counts of the page of a list that happens to be loaded. The queue and the star
  counts are facets over the vault (`/sources/facets`), not counts of the loaded window either. A badge derived from the
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
- An **EPUB** shows the same rendering (its chapters and their sections) in the **Text** tab, and the
  count in its head and rail is **chapters**, not pages. There is no inline document tab: a browser
  does not render an EPUB, and the book's text is what the pipeline read. The object stays in the
  **Files** tab and is never mistaken for text.
- **A source's rail sits under the content when the page is narrow.** The reading surface then keeps
  the full page width for its text and for wide images; the page's maximum width still caps the
  surface, and an image never grows past it.
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
- `web/public/sw.js` precaches the shell (document, manifest, icons) and is **network-first** for the
  document. The API is deliberately **not cached**: this app *is* a local service, and a cached
  answer about the vault would be a lie exactly when the service is down.
- Bumping a precached asset means bumping the cache name in `web/public/sw.js`; a new build needs no
  bump (hashed assets change names).

## 7. Updates

- **An open window notices a new build.** Every build writes `internal/web/dist/build.json` with the
  entry chunk it produced; a window compares that with the entry chunk its document loaded. The
  check runs at every catch-up point — the event stream opening again (an app restart is exactly a
  binary update), a return to the window, `online`, a page restored from the back/forward cache —
  and once every five minutes while the window is visible.
- **A hidden window reloads, a visible one is told.** Hidden, the window reloads at once: on a phone
  that is the moment the app goes to the background, and the reader returns to the new version.
  Visible, the top bar shows **Update ready** instead, because a reload under the reader's hands
  would throw away an unsaved wiki page or a half-typed question; leaving the window then reloads it.
- **No reload loop.** The entry a window reloaded for is remembered in `sessionStorage`; if the
  build still looks stale after that (a stale cache), the window says so instead of reloading again.
- **The service worker is not the signal.** `sw.js` changes no file between builds on purpose, so
  no worker update would fire; the shell is network-first and the check above is what moves a
  window on. The API is never part of any of this.

## 8. Development

```bash
task web:install   # pnpm install the PWA toolchain
task web:dev       # Vite dev server with HMR, proxies /api to the running app
task web:check     # svelte-check + TypeScript
task web:test      # unit tests
task web:build     # build into internal/web/dist (embedded by go:embed)
```
