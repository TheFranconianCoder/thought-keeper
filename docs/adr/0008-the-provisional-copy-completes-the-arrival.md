# 0008 — The provisional feed copy completes the arrival

Status: accepted, 2026-11-20. Amends the "always an event" list of
[ADR 0003](0003-the-snapshot-moves-only-on-an-event.md): a feed copy replaced by its page is an
event only once the copy has been through a check.

## Context

A feed may carry the body of a post. That copy becomes the page source's **provisional** snapshot,
and the page is fetched once right after; a readable page that carries at least as much text
replaces the copy. The replacement is a provenance change and has to happen whatever the difference
is — the record's `from_feed` marker must go and the outline is built from the bytes — so ADR 0003
listed "a feed copy replaced by its page" among the changes that are always an event.

The reader never saw the copy. The source ARRIVED with it, and the page replaced it within the same
update turn (one source per `capture.refresh_min_gap`). The change record that the event wrote
compared the copy with its page: in practice the same article, reflowed by the main-content
converter — exactly the whitespace difference the materiality threshold exists to keep out of the
record. A live vault made it plain: a source `added 38m ago` with `Changed 37m ago`, and a record
whose own measurement read `Words: 0 changed, 0.0 characters on average` under `Body: +9 -0 lines`.
Every feed article whose page was richer than its copy produced one, so the vault accumulated
"changes" to sources nobody had read.

## Decision

- **The provisional window is the source's first check.** While `<hash>.meta` says `from_feed` and
  the source has never been checked (`url_checked_at` unset), the copy is provisional: the fetch
  completes the arrival.
- **The bytes still move.** The page replaces the copy, the markers go, the outline is rebuilt from
  the new bytes: that is a provenance change, not a threshold question, and `RefreshResult.changed`
  counts it.
- **Nothing reader-facing moves.** No `<hash>.changes.md`, no `changed_at`, no **updated** mark, no
  cluster pass: there is no "before" the reader had. The source log keeps the handover.
- **The window closes on a failed check.** A first fetch that fails writes `url_checked_at` and
  keeps the copy, so the reader can have opened it and the derivation may describe it. A later
  replacement is an event again and writes the record.
- **The human's Refresh does not widen it.** `RefreshOne` (the button) still bypasses the threshold,
  but a provisional copy gives the record nothing to show, so the bytes are replaced and the record
  stays unwritten. The app's own feed job calls `RefreshCopy` — the same pass without the button's
  force — because it is not a person asking.

## Consequences

- A feed article's row is **new** with one instant: its arrival. Its rail shows no "last changed",
  and its page shows no change strip and no **Changes** tab for a page the reader has just received.
- A feed page replaced days later (the first fetch failed) keeps its record, which then compares two
  texts the reader could both have read.
- The change record stops being a log of provenance handovers. The source log is that log, and it
  always says `the page replaced the feed copy`.
- Records written before this decision stay: they are records, not caches, and afterwards no field
  tells a handover's record from a real change (its words are the only clue). Removing them is open
  work in `TODO.md`.
- A wall that clears keeps its record: the stored bytes are then a notice the reader could open, not
  the article. That is a related question, deliberately not answered here.
