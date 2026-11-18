# Paced URL refresh and a visible change record

## Goal

1. **Pace the scheduled URL refresh.** At most one page per `capture.refresh_min_gap`
   (default `1m`, `''`/`0s` = as fast as possible) instead of one batch pass over every due source.
   The pace is polite to the sites and kind to a small machine (a Pi5), and it makes a progress
   message per page possible.
2. **Make a real page change visible.** When a refresh rewrites a snapshot, write what changed into
   the vault (`<hash>.changes.md`) and show it in the source view as a **Changes** tab. No git, no
   extra revision of the bytes, no new endpoint.

## Non-goals (explicitly out)

- **History / restore.** Only the *last* change is recorded. Keeping superseded snapshots,
  compression of old revisions and a "restore this version" action are a separate plan (P3 in the
  discussion). The change record is written from the two bodies that already exist at refresh time.
- **A built-in `git diff`.** `vault.git.*` stays ignore-only; the app still never runs git.
- **Diffs for wiki pages.** A wiki save replaces its representation knowingly; it is not a page that
  changed behind the reader's back. (Follow-up if wanted.)
- **Part-level change lists in the record.** The text diff is the record; the outline rebuild
  already logs which parts kept or lost their claims.
- **A new HTTP endpoint.** The existing raw/sidecar route serves the record.

---

## Part 1 — Paced refresh

### 1.1 Config knob `capture.refresh_min_gap`

- `CaptureConfig` gets `RefreshMinGap string` (yaml `refresh_min_gap`, default `"1m"`).
- It describes the corpus → **vault-owned** (`config/vault.go`), writable in the Config overlay,
  `TierBasic` (it is an operating knob, not an expert ceiling).
- Validation: a duration; `''` or `0s`/`0` means "no pace" (the pre-change behavior). Add to
  `durationKeys` (so the overlay renders it as a duration) and to `durationRules` with
  `allowEmpty: true`.
- `config.example.yaml`: a commented entry under `capture`, plus a line in the section comment that
  explains the sweep-time trade-off.
- Docs: `docs/operations.md` (`capture` row of the section table) and `docs/pipeline.md` (the
  cadence bullet) must say: *the cadence is the minimum age of a source; the gap is the throughput;
  a sweep over N sources takes N × gap.*

Acceptance

- [x] A machine file and a `vault.yaml` that set `capture.refresh_min_gap` both take effect, and
      the vault wins.
- [x] The Config overlay lists it as a duration, `basic`, writable, scope `vault`, and writing it
      applies in the running process (no restart).
- [x] An invalid value (`"soon"`, `-1m`) is refused by the writer and reported by `status`, the
      running value stays as it was.
- [x] `''` and `0s` are accepted and mean "no pace".

### 1.2 Pick one due source instead of a whole pass

- New sqlc query `ListRefreshCandidates` in `queries/sources.sql`:
  all `kind = 'url'` rows that are **not attached** (the established `json_extract(meta,'$.attached')`
  guard, same shape as the `attached` filter of `ListSourcesFiltered`), ordered by
  `url_checked_at ASC` (NULLs first = never checked), then `imported_at ASC`, with a LIMIT.
- Migration `00013_sources_url_checked_index.sql`: a partial index
  `ON sources (url_checked_at) WHERE kind = 'url'`. Index only, no shape bump.
- Use case `URLs.RefreshOneDue(ctx) (RefreshResult, error)`:
  - asks for a candidate window (start 64), runs the **existing** due predicate
    (`executeRows`' rules: attached, per-kind age, bookmark) over it, and returns the first row
    that is due;
  - if the window was full and nothing was due, doubles the window and asks again (bounded by the
    row count) — this keeps the "due" decision in ONE place (Go) and cannot starve a feed behind a
    page that is not due yet;
  - refreshes that one row through `executeRows(ctx, []entities.Source{src}, true, false)`, so
    walls, feed copies, bookmarks, sitemaps, feeds and listings keep their current handling;
  - reports "nothing due" as a distinct answer (`RefreshResult.Tried == false` or a sentinel), not
    as an error.
- The repository interface and the test fakes get the new method.

Acceptance

- [x] With three due sources, three calls refresh three *different* sources, oldest check first.
- [x] An attached snapshot is never chosen; a feed that is due tomorrow is not chosen; a page that
      is due next week is not chosen.
- [x] A due feed behind 100 not-yet-due pages is still found (window growth).
- [x] A vault with nothing due yields the "nothing due" answer and no fetch.
- [x] `Refresh`, `RefreshAll`, `RefreshOne` and `RefreshOneForce` behave exactly as before.

### 1.3 The worker

- `refreshWorker` reads `capture.refresh_min_gap` on every loop (like the cadences), so a change
  applies without a restart:
  - **gap > 0 (new default):** run one paced check immediately, publish its result, then wait `gap`;
    when nothing is due, wait `min(gap, 1m)` instead — an idle query is cheap, and a cadence or gap
    changed in the overlay then takes effect without a restart or an hour of waiting. When both
    cadences are off, the same short wait keeps enabling them responsive.
  - **gap <= 0:** the current batch ticker, unchanged.
- The 5-minute ceiling stays per pass (one page), and a paced check publishes `source` + `urls` for
  that page so the UI updates as it goes.
- The manual passes (`POST /refresh-urls`, per-source refresh/refetch) ignore the gap: a human
  asking now gets a pass now. Document this.

### 1.4 Stop a cancelled pass from poisoning the rest

Today a pass that hits its 5-minute deadline has no `ctx.Err()` check: every remaining fetch fails
immediately and is recorded as a **failed check** with a fresh `url_checked_at`, which silences the
source for a whole cadence.

- Add a `ctx.Err()` guard at the top of the `executeRows` loop: a cancelled pass returns early with
  the context error and the work it did.
- `refreshNow` applies the partial result, then logs the cancellation as a warning.
- Test: a context cancelled after one row leaves the remaining rows untouched (`url_checked_at`
  unchanged, no failure recorded).

Acceptance

- [x] A cancelled pass never writes a check time or a failure for a source it did not fetch.
- [x] The next pass still finds those sources due.

### 1.5 Docs

- [x] `docs/pipeline.md`: the cadence bullet (pace, sweep math, manual passes exempt) and the
      "changed page → rebuild" bullet (now also names the change record).
- [x] `docs/operations.md`: the `capture` row and the `vault.yaml` key list.
- [x] `config.example.yaml`: the new knob with its trade-off comment.

---

## Part 2 — Visible change record

### 2.0 What counts as a change (materiality)

- The snapshot and the outline always follow the bytes (the outline's node references are byte
  ranges). The **event** — change time, updated mark, record, cluster request — needs a *material*
  change.
- Measurement: `textdiff.Words` compares the two word bags (the vault's tokenizer: lowercase,
  folded, punctuation split, one-character noise dropped). A replaced word counts once; a reflow,
  a punctuation fix, a moved paragraph or a ticked one-digit counter counts zero.
- Rule: the title moved, or at least `capture.change_min_words` word positions differ and those
  words average more than `capture.change_min_word_length` characters (defaults 10 / 5;
  `0` words = off, every difference is material).
- An immaterial change updates the snapshot, rebuilds the outline and logs one line saying why it is
  not an event.
- New knobs: `capture.change_min_words`, `capture.change_min_word_length` (vault-scoped, advanced,
  writable); `RefreshResult.Material` counts the events, `changed` still counts differing pages.

### 2.1 The diff engine (pure)

- New package `internal/core/textdiff` (next to `textmatch`, `markdown`, `tree`):
  `Lines(old, new string) Result` with `Text` (a unified diff, empty when identical), `Added`,
  `Removed`, `Truncated`.
- Line-based, 3 lines of context. Trim the common prefix and suffix first (a weekly change of a long
  article is a few lines in the middle); run a Myers-style diff on the middle only; stop at a hard
  work/output ceiling (`maxOutputLines ≈ 2000`, `maxOutputBytes ≈ 256 KiB`) and report `Truncated`
  instead of hanging the Pi on a rewritten 5 MiB page.
- No dependency: pure stdlib, table-driven tests (identical input, one changed word, reflow, pure
  insertion, pure deletion, empty old, empty new, truncation).

Acceptance

- [x] Identical bodies yield an empty text and zero counts.
- [x] A one-word change yields one hunk with one `-` and one `+` line.
- [x] A reflow (same words, different wrapping) yields a line diff — the record shows the reflow
      honestly, even though the claims are kept (that is the rebuild's tolerance, not the record's).
- [x] A 5 MiB rewritten body is bounded (output cap, `Truncated`), with no pathological runtime.

### 2.2 The record on disk

- New sidecar suffix `entities.SidecarChanges = ".changes.md"`, added to `SidecarSuffixes()` — that
  one line gives the Files tab, `RemoveSidecars`, `RenameSidecars` and the raw route for free. Add it
  to the `Raw` handler's `which` whitelist.
- Content: a short Markdown record, human-readable in the vault (no base64, no escaped JSON):

  - a heading with the change time;
  - the title before → after, when it changed (otherwise "title unchanged");
  - `+n −m` line statistics;
  - a fenced `diff` block with the unified diff; when the diff is truncated, one closing line says
    so and that the snapshot holds the full text.
- Writer: a shared helper next to the refresh (e.g. `source_changes.go`), called
  - in `executeRows` right after the old body is read and before `PutAt` (the old bytes are already
    in hand — zero extra I/O), for every path that rewrites a snapshot: a changed page, a feed copy
    replaced by its page, a cleared wall, an explicit refetch;
  - in `attach_url_snapshot.go`, so the human's replacement also records what it replaced.
- Rules:
  - a **title-only** change writes a record with an empty diff ("the body did not change") and
    **replaces** any older body diff — the record is always about the last change;
  - a **first read** writes nothing (an arrival is not a change);
  - an **unchanged check** never touches the file;
  - a failed write is a warning, never a failed refresh (like the source log);
  - the record goes away with the source (`RemoveSidecars`).
- `vaultgit.Patterns()` ignores `sources/**/*.changes.md`, like the log: it is rewritten on every
  change, and a vault under git already gets the canonical diff from the snapshot itself.

Acceptance

- [x] A changed page leaves `sources/<ab>/<cd>/<hash>.changes.md` with the time, the statistics and a
      diff a human can read.
- [x] The Files tab lists it and the raw view opens it.
- [x] An unchanged check does not rewrite it; a first fetch creates none.
- [x] A title-only change replaces a body diff with a "body unchanged" record.
- [x] A page whose only difference is a ticked counter is `changed` but not `material`: the snapshot
      follows, the change time and the record do not, and the source log says so.
- [x] A rewritten paragraph is material; the record names the word measurement.
- [x] Deleting the source removes it (the folder goes to the trash as a whole).
- [x] A write failure only warns; the refresh still reports its result.

### 2.3 The UI

- `web/src/components/views/SourceView.svelte`:
  - new tab `changes`, addressable as `?tab=changes`, present only when the source lists a
    `.changes.md` sidecar **with a size** (`bytes >= 0`) — the sidecar list names every suffix of a
    source even when the file is absent, so the name alone is not the test (a page that never
    changed stays without the tab). It sits in the tab row next to Snapshot, not behind Files.
  - **a change strip directly under the source header, on every tab**, whenever the record exists:
    "Changed 2 days ago — see what changed" as one clickable line that opens the Changes tab. The
    diff must not depend on the reader knowing that a file tab exists.
  - the tab fetches the record with the existing `api.rawText(hash, '.changes.md')` when it opens
    and again when a `source` event bumps the version — the same pattern as the Log and Files tabs;
  - renders the record as preformatted text (a fenced diff is readable as is; no diff-viewer
    library, no new client code for parsing);
  - the rail's existing `Changed <time>` line becomes a second link to the tab when the record
    exists (the strip is the primary one).
- i18n: `source.tabChanges`, the strip's label and the tab's intro line in `messages/source.en.ts`
  and `source.de.ts` ("Changes" / "Änderungen").

Acceptance

- [x] A page that never changed has no Changes tab; a changed page has one.
- [x] The change strip is visible on every tab of a source that has a record, and one click opens
      the diff.
- [x] The tab shows the record after a refresh without a reload (SSE `source` event).
- [x] A deep link `#/sources/<hash>?tab=changes` opens the tab; a source without a record falls back
      to the default tab (the existing `tabExists` rule).
- [x] Both languages carry the keys ("Changes" / "Änderungen"); the catalog falls back to English,
      so a missing German key is silent — check both files.

### 2.4 Docs

- [x] `docs/storage.md`: the sidecar table (`<hash>.changes.md` — a **record**, the last change, not
      rebuildable), the sidecar list, and why git ignores it.
- [x] `docs/api.md`: the source payload's sidecar list already covers it; name `.changes.md` in the
      raw route's allowed `which` values if the doc enumerates them.
- [x] `docs/pwa.md`: the Changes tab in the source view.
- [x] `AGENTS.md`: one line in the ubiquitous language (**change record**) and the Current state
      sentence about a changed page (it already says "a reflow or a single changed word keeps
      them" — extend it with the record).

---

## Part 3 — Verification

- [ ] `task check` — the toolchain is not installed in this environment; ran `go test ./...`, `go vet ./...`, `svelte-check` and `vitest` by hand, and `vite build`. `sqlc:verify` still has to run where sqlc exists (the generated query was written by hand).
- [x] `go test ./internal/core/textdiff ./internal/core/usecases ./internal/infrastructure/config ./internal/api/...`
- [ ] Manual smoke on a vault (not run here):
  1. set `capture.refresh_min_gap: 1m`, watch the log: one page per minute;
  2. `POST /api/v1/refresh-urls` still refreshes everything at once;
  3. change a page (or point a source at a page that changed), refresh it, open **Changes**;
  4. check the record in the vault with a plain editor and `git status` (ignored);
  5. delete the source, confirm the sidecar is gone.
- [x] No change in `index.db`'s shape beyond the new partial index; `reindex` and `rebuild-trees`
      stay green.

## Decisions worth remembering

- **Default `1m`, not off.** The request is a paced refresh; an unpaced default would keep hammering
  a hundred sites in one minute. `''`/`0s` restores the old behavior for a vault that wants it. The
  cadence stays the minimum age; a vault with more pages than minutes-per-week has to accept that a
  sweep takes longer than a week (documented, not hidden).
- **The due decision stays in Go.** SQL only orders candidates by last check; the per-kind age,
  attached and bookmark rules stay where they already are.
- **The record is Markdown, not JSON.** The vault must stay readable (`less <hash>.changes.md`), and
  the UI can render it without parsing. A JSON sidecar would be machine-friendly and human-hostile.
- **The record is ignored by git.** The snapshot is committed; the canonical diff is the snapshot's
  own diff. The record is the local, in-app story, like the log.
- **One sidecar, no history.** The old body is not kept: that is the P3 decision, and it stays open
  (content-addressed revisions or compressed diff archives).
