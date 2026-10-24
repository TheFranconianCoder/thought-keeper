# Thought Keeper — the next PWA

This is the **next-generation PWA of Thought Keeper**, built beside the previous source in `../web/`.
The old source is deliberately untouched; the build pipeline now embeds this UI instead.

The goal was not a reskin. The old UI had accumulated several visual languages, a very large central
store, and a reactive/SSE path that was hard to reason about. This version starts from one design system
and one event model, while keeping every product rule of the app: the lenses, the pipeline lights, the
source page, wiki authoring, the claim detail with provenance and peers, Ask, Attention, Bookmarks and
Status.

## Run it

```bash
cd web-next
npm ci
npm run dev        # http://localhost:5174, proxies /api to http://127.0.0.1:9210
```

The app itself is unchanged; start it as usual (or run it headless). The Vite dev server proxies the API
to the default loopback port. To use another port, change `apiTarget` in `vite.config.ts`.

```bash
npm run check      # svelte-check + TypeScript
npm test           # parser and source-state unit tests
npm run build      # build into ../internal/web/dist (embedded by go:embed)
```

`npm run build` writes directly to `../internal/web/dist`, which is the directory `internal/web`
embeds. `task web:build` does the same and is a dependency of `task build`, `task run` and
`task run:open`.

## The design

One palette, one type scale, one spacing scale, one set of components.

- **Surfaces:** warm paper in light (`#f7f5f1`), deep graphite in dark (`#0c0f13`); the sidebar is one
  step darker than the canvas, panels one step lighter.
- **Ink:** near-black on paper, near-white on graphite, with two quiet secondary levels.
- **Accent:** the studio signal orange, used for the active lens, primary actions and citations.
- **Semantics:** green/amber/red/blue appear only where a state is being reported (step lights, findings,
  connection, progress), never as decoration.
- **Typography:** one sans family for chrome and long-form text (Inter or the platform equivalent), and
  a mono face for ids, counts, file names and logs. Long-form text is made readable with size and
  line-height, not with a second typeface.
- **Rhythm:** 4/8/12/16/20/24/32/40 spacing; 6/9/13 px radii; hairline borders; one soft shadow.
- **Density:** calm list rows, clear section labels, no boxes inside boxes. Long pages are split into
  a fixed list pane and an independently scrolling reading pane on desktop, and become a single column
  on narrow screens. Fades mark the edges of every scrollable cloud or list.

The whole visual language is in `src/styles/app.css`. Component styles only describe layout; colour,
type and controls come from the tokens and shared classes.

## The structure

```
src/
  App.svelte                 shell, route switch, global effects
  main.ts                    mount, theme, window controls, service worker
  styles/app.css             design tokens, primitives, markdown, responsive shell
  lib/
    api/                     typed wire shapes, request client, one endpoint object
    events/                  SSE parser, resilient stream, event-to-store sync
    stores/                  one store per domain (claims, sources, wiki, asks, system, search)
    actions/                 user commands, grouped by domain
    router.svelte.ts         hash routes, deep links, query helpers
    source-state.ts          pure derivation-state logic (lights, progress, labels)
    markdown.ts              wikilinks + sanitized Markdown
    theme.svelte.ts          auto/light/dark, persisted before first paint
    overlays.ts              search/add as URL parameters
    confirm.svelte.ts        in-app questions, never native dialogs
    toast.svelte.ts          bounded transient messages
    scroll-memory.ts         scroll position per history entry
    single-window.ts         BroadcastChannel election
  components/
    layout/                  Sidebar, Topbar
    views/                   Claims, Wiki, Sources, Source, Ask, Attention, Bookmarks, Status
    sources/                 step lights, source icon, source progress
    overlays/                command palette, add source, duplicate window
    ui/                      Icon, Markdown, Modal, Confirm, Toasts, EmptyState, CopyId
```

### Stores, not one god object

Each domain owns its data, loading state, errors and generation counters:

- `ClaimsStore` — bounded newest-claims window, topic cloud, one claim + peers.
- `SourcesStore` — source list, live progress by hash, the open source's meta/tree/atoms/log/content.
- `WikiStore` — pages, tag cloud, one page.
- `AsksStore` — saved asks, the active run, the answer and its polling safety net.
- `SystemStore` — status, pipeline/jobs, findings, vault report, vocabulary.
- `SearchStore` — debounced, abortable palette search across claims **or** sources.

Every loader carries a generation. A slow response cannot overwrite a newer one — which matters even
more without polling, because a stale write would stay on screen.

### The event pipeline

`lib/events/event-stream.ts` reads `/api/v1/events` with `fetch` instead of `EventSource`. That buys:

- a real SSE parser (`sse-parser.ts`) that reports heartbeat comments as liveness;
- **exponential backoff with jitter**, capped at 20 s;
- a **heartbeat watchdog**: a half-open stream that still looks open is rebuilt after 60 s without bytes;
- immediate reconnection on visibility/focus/online, without waiting for a backoff;
- one catch-up (`resyncAll`) after a reconnect.

`lib/events/sync.ts` is the only consumer. It has two lanes:

1. **Live lane** — `progress` counters merge per source and `log` lines append to the open source's tail.
   Both are buffered and flushed once per ~60 ms, so a run emitting hundreds of lines causes a handful
   of reactive updates.
2. **State lane** — `source`, `wiki`, `clusters`, `findings`, `vocabulary` and `run` become a set of
   scopes. Each scope is re-read at most once per batch; a batch arriving during a load is picked up by
   the next turn. No overlapping requests, no reload of everything on every frame.

The server's full `pipeline` payload is applied directly; a `run` that finishes clears the local banner,
reports a failure once and invalidates only the containers that can have changed. A return to the window
is a catch-up point; a reconnection is a full resync.

### Routing and overlays

The URL remains the truth. `#/claims/<id>`, `#/wiki/<id>`, `#/sources/<hash>?tab=files&file=.log`,
`#/ask/<id>`, plus `search=1` and `add=1` as query parameters. Closing an overlay does not lose the
reader's place, and back/forward walks tabs and open files.

### Honest empty states

“No sources yet” is only said when a read actually succeeded. If the connection is not live, the empty
state says **Nothing loaded** instead. A local action that is still in flight is shown as a banner until
the server's pipeline or run event supersedes it.

## Differences from the current UI

- A single design system and one icon set (20×20 stroke) instead of mixed styles.
- Two-pane list/reading layouts for Claims, Wiki and Ask; responsive drawer navigation.
- Command palette searches both claims and sources, with a layer switch and keyboard navigation.
- `EventSource` replaced by a fetch stream with a parser, heartbeat watchdog, backoff+jitter and
  visibility/online catch-up.
- Event handling split into a coalesced live lane and a coalesced state lane; each store has generation
  guards.
- Source page tabs, files, logs, outline parts, PDF and live views preserved; the open tab/file/part is
  in the URL.
- Theme is applied before first paint; three states (auto/light/dark) persisted under `tk-theme`.
- The add dialog supports bookmarks (the API already did), batches of URLs, drag/drop and file upload.
- Scroll memory per history entry, single-window election and a PWA shell are preserved.

## Embedding into the Go binary

The build is already wired into the Go binary:

- `web-next/vite.config.ts` writes to `../internal/web/dist`.
- `internal/web/embed.go` embeds `dist` with `//go:embed all:dist`.
- `Taskfile.yml` points `web:install`, `web:build`, `web:dev`, `web:check` and `web:test` at
  `web-next/`, and `run`, `run:open` and `run:headless` depend on `web:build`.

The previous UI source remains in `../web/` as a reference and can still be built manually with its own
toolchain. The service worker cache name is distinct (`thought-keeper-next-v1`), so a browser with the
old shell cached installs this one cleanly.
