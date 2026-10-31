# Thought Keeper — the PWA

The web client of Thought Keeper: Svelte 5 + Vite + TypeScript, served by the Go binary from
`../internal/web/dist`, which is embedded with `//go:embed all:dist`.

## Run it

```bash
cd web
pnpm install
pnpm run dev        # http://localhost:5174, proxies /api to http://127.0.0.1:9210
```

The app itself is unchanged; start it as usual (or run it headless). The Vite dev server proxies the API
to the default loopback port. To use another port, change `apiTarget` in `vite.config.ts`.

```bash
pnpm run check     # svelte-check + TypeScript
pnpm run test      # parser and source-state unit tests
pnpm run build     # build into ../internal/web/dist (embedded by go:embed)
```

`pnpm run build` writes directly to `../internal/web/dist`, which is the directory `internal/web`
embeds. `task web:build` does the same and is a dependency of `task build`, `task run`,
`task run:open` and `task run:headless`.

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
    update.ts                pure build-comparison logic (is this window stale?)
    update.svelte.ts         the update watch: manifest check, reload or notice
    markdown.ts              wikilinks + sanitized Markdown
    theme.svelte.ts          auto/light/dark, persisted before first paint
    overlays.ts              search/add as URL parameters
    confirm.svelte.ts        in-app questions, never native dialogs
    toast.svelte.ts          bounded transient messages
    scroll-memory.ts         scroll position per history entry
    scroll-top.svelte.ts     which list scrolled furthest; the shared scroll-to-top button
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

## Embedding into the Go binary

The build is wired into the Go binary:

- `web/vite.config.ts` writes to `../internal/web/dist`.
- The same build emits `dist/build.json` — the entry chunk it produced. An open window compares that
  with the entry chunk its document loaded and reloads when the two differ; without it an installed
  PWA would keep its bundle until the reader restarted it. `docs/pwa.md` owns the rule.
- `internal/web/embed.go` embeds `dist` with `//go:embed all:dist`.
- `Taskfile.yml` points `web:install`, `web:build`, `web:dev`, `web:check` and `web:test` at
  `web/`, and `run`, `run:open` and `run:headless` depend on `web:build`.

The service worker cache name is `thought-keeper-v2`; bump it when a precached asset changes.
