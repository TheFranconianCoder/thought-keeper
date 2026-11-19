# 0004 — One source per gap, for the whole update path

Status: accepted, 2026-11-19. Extends
[ADR 0002](0002-paced-refresh-and-change-records.md): the gap no longer paces only the scheduled
refresh.

## Context

ADR 0002 introduced `capture.refresh_min_gap` as the refresh's throughput: the scheduled worker
fetched one due page per gap. Everything else still entered in a burst.

- A sitemap, feed or listing handed every page it declared to a queue served by three workers, so a
  feed adding twenty posts produced twenty fetches at once — and then twenty outlines and twenty
  distill runs.
- A feed's own copy is a source before its page is fetched; the materializer wrote one per item and
  the pump derived all of them in one pass.
- An inbox drop started its capture and derivation immediately, and a batch of drops was a batch of
  derivations.
- A source that needed several runs (a long document) must not be slowed down by any of this: the
  pace is about how many SOURCES enter, not how many runs one of them takes.

The reader asked for one rule: updates and new pages alike, at most one source per gap — and it is
about the downstream steps as much as the fetch.

## Decision

- **A source is TAKEN IN.** The turn is taken at the moment a source enters the update path, not at
  each fetch and not at each step. Its fetch and its document steps — capture, outline, distill, and
  the continuation runs of a long document — belong to that one turn. The next source waits for the
  next gap; its processing may overlap the previous source's.
- **One knob, one turn.** `capture.refresh_min_gap` (default `1m`, empty or `0s` = the burst) paces
  the automatic fetch worker and the pump together. A due check, a discovered page, a feed's copy and
  an inbox drop all compete for the same turn.
- **The fetch worker owns the automatic fetches.** The pages a sitemap, feed, listing or wiki page
  declared go to their own queue served by one worker, which alternates between that queue and the
  due cadence, so neither a sitemap's backlog nor the weekly sweep starves the other. The human's
  own URLs keep their three workers.
- **A booked source costs nothing.** The source's fetch books it (the fetch already took the turn),
  and a human action books it too, because a manual action is not paced at all. The pump works on
  booked sources without waiting.
- **The pump takes the turn for a source that has no fetch of its own** (an inbox drop, a feed's
  copy, a repair that re-opened a step). It never blocks on the turn — the sources already taken in
  still need their claims — so a spent turn skips that source and asks for another pass when the
  turn is free. An idle fetch look gives its turn back.
- **The ledger is in memory and pruned.** A source leaves it when its document steps are through
  (ok, skipped or failed), so a later event — a refresh, a repair — takes a fresh turn. A restart
  starts with a free turn.
- **Manual actions are not paced and do not spend the turn:** a single add, a pasted batch, the
  Refresh button, `POST /refresh-urls`, a run, a reset, a manual capture.

## Consequences

- A first import is slow in wall-clock time: N pages take at least N × gap before their derivations
  start. That is deliberate; `0s` restores the burst for the reader who does not want it.
- A feed's copies trickle: one per gap, interleaved with the fetches and the due refreshes. A feed
  with twenty new posts therefore takes about twenty gaps before its posts are derived — which is
  the point, and the feed's copy is readable in the meantime.
- The gap bounds sources, not HTTP requests: one turn may still fetch a page's favicon, render the
  page locally, or walk a listing's own pagination (bounded by `capture.listing_max_pages`), because
  those belong to the source that took the turn.
- The turn lives in the process. Two instances cannot run (the vault flock), so there is no second
  pacer; a restart resets the rate but not the sources' own `url_checked_at`.
- The pump's claim window (`2 × parallel_runs + 2`) still bounds the burst and the manual bookings;
  with the pace on, it is rarely reached by automatic admissions.
