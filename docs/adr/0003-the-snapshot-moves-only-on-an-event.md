# 0003 — The snapshot moves only on an event

Status: accepted, 2026-11-19. Supersedes the bullet "the snapshot and the outline always follow the
bytes" of [ADR 0002](0002-paced-refresh-and-change-records.md).

## Context

ADR 0002 introduced a materiality threshold for a page change but used it only for the reader's side
of a refresh: the **updated** mark, the change time, the change record and the cluster request. The
bytes themselves were rewritten on every difference, and the derivation followed them unconditionally
— a body difference put the source into `Resets`, the app re-armed the outline step and pumped the
pipeline. One ticking date, one view counter or one corrected word therefore cost a title model call,
the distill (and audit) of the affected parts, and a fresh source summary. A vault with a few hundred
pages paid that every week, for changes no reader would ever notice.

The reason the outline could not simply be skipped was real: a node's text is a byte RANGE into the
snapshot (`TextStart`/`TextEnd`), so replacing the bytes without rebuilding the outline makes every
node's text resolve to the wrong slice. The two options were a second copy of the "derived" text or
not replacing the bytes at all.

## Decision

- **A change below the threshold does not touch the snapshot.** The stored bytes ARE the baseline of
  the derivation: the outline, the node texts, the claims and the search index all describe them, so
  keeping them keeps the whole derivation coherent with no second copy and no corruption.
- **The drift ACCUMULATES.** The next check compares the fetched page with the stored snapshot, not
  with the previous fetch, so a page that gains a sentence a week is re-derived once it has gained a
  paragraph. A pending drift is not lost, it is measured again in full.
- **A change is an event** when the title moved, or when at least
  `max(capture.change_min_words, ⌈stored_words × capture.change_min_percent / 100⌉)` word positions
  differ AND those words average more than `capture.change_min_word_length` characters. Defaults are
  **40 / 1 / 5** (was 10 / – / 5): the floor governs a short page, the share governs a long one (a
  6 000-word reference page must change 60 words before it is worth a model call). `0` words turns the
  whole rule off; `0` percent turns off only the share.
- **Only an event starts the pipeline.** The snapshot is written, the change record and the change
  time follow, the **updated** mark appears, the outline is rebuilt under the exclusive gate (keeping
  the claims of parts whose text survived), the changed parts are distilled and the cluster pass is
  asked for. Everything else costs a check: `url_checked_at`, one log line naming the measurement.
- **Always an event, no threshold:** a first read (an arrival, not a change), a human attachment, a
  wall that cleared, a feed copy replaced by its page, a collected link that became a page, and a
  sitemap/feed/listing refresh (their snapshot is a list; their children are the value).
- **The per-source Refresh button bypasses the threshold.** The human asked for THIS page; a page that
  quietly refuses to update is not a refresh. The scheduled pass and "Refresh URLs" stay strict.

## Consequences

- A page is "current" at the resolution of the threshold, not of the cadence. `url_checked_at` says
  when the page was last seen, `changed_at` when it last said something new, and the snapshot's own
  `fetched_at` when those bytes were taken. The three are shown beside each other and never conflated.
- The change record is a diff against the version the claims were derived from — which is what a
  reader wants to know — not a diff against yesterday's noise.
- The Sources list stops marking pages that only ticked. A row that is marked **updated** has claims
  that were actually re-derived.
- A genuinely small but meaningful edit (one changed number in a table) waits until the accumulated
  drift is worth an event. The reader can always press Refresh on that source, and the log says why
  nothing happened before that.
- `RefreshResult` grows `deferred` (and the app publishes those sources' log lines). `changed` is
  every page whose text differs; `material` the ones that became an event.
- The threshold knobs are vault-scoped and advanced (`capture.change_min_words`,
  `capture.change_min_percent`, `capture.change_min_word_length`), so a vault that wants a different
  trade-off travels with its own answer.
