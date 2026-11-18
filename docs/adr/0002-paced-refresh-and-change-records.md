# 0002 — The refresh is paced, and a changed page leaves a record

Status: accepted, 2026-11-18.

## Context

The scheduled URL refresh used to walk EVERY due source in one pass, bounded only by a five-minute
context. A vault with a few hundred pages therefore hit the sites in a burst, and the pass held the
gate's shared side for as long as it ran. Worse, a pass that hit the context deadline had no stop
check: every remaining fetch failed at once and was recorded as a failed CHECK, which moved
`url_checked_at` and silenced the source for a whole cadence.

A reader could see THAT a page changed — the Sources list marks the row **updated** and the rail
shows the change time — but not WHAT changed. The refresh overwrote the snapshot in place, so the old
text was gone by the time anyone could look. The alternatives were a vault under git (the app only
maintains `.gitignore`; it never runs git) or keeping every revision (disk, compression, history UI).

## Decision

- **Cadence and throughput are two knobs.** `capture.refresh_interval` / `feed_refresh_interval` stay
  a source's MINIMUM age; `capture.refresh_min_gap` (default `1m`, empty or `0s` = the old burst) is
  the THROUGHPUT. The scheduled worker fetches one due page per gap; a sweep over N sources takes
  N × gap.
- **The due rule lives in ONE place (Go).** `dueOnCadence` decides (attached, per-kind age, never
  checked). The index query `ListRefreshCandidates` only answers "what has waited longest" —
  ordered by `url_checked_at`, attached excluded — and a partial index keeps that cheap. A paced pass
  grows its candidate window when it is full and nothing in it was due, so a due feed behind
  not-yet-due pages cannot starve.
- **Manual passes are not paced.** `POST /refresh-urls` and the per-source refresh act at once; the
  pace is politeness toward sites, not a limit on the human.
- **A cancelled pass stops.** `executeRows` checks `ctx.Err()` per row and returns the work it did;
  no check time or failure is invented for a source it never fetched.
- **Only a MATERIAL change is an event.** The snapshot and the outline always follow the bytes — the
  outline's node references are byte ranges, so skipping the rebuild would corrupt them — but the
  change time, the **updated** mark, the change record and the cluster request are reserved for a
  change a reader would notice. The measurement is a WORD SHIFT, not a byte comparison: the
  difference of the two word bags (lowercase, folded, punctuation split, one-character noise
  dropped). A replaced word counts once; a reflow, a re-ordered paragraph, a punctuation fix or a
  ticked one-digit counter counts zero. A change is material when the title moved, or when at least
  `capture.change_min_words` word positions differ AND those words average more than
  `capture.change_min_word_length` characters (defaults 10 and 5; `0` words turns the rule off).
- **A material change leaves a change record** (`sources/<ab>/<cd>/<hash>.changes.md`), written on the
  in-place rewrites while BOTH bodies are in hand: the time, the title before and after, the word
  measurement, and a line-based unified diff of the readable body (capped at 2000 lines / 256 KiB). It is a **record**,
  not a cache: the old bytes are gone, so it cannot be rebuilt. A first read writes nothing; a
  title-only change replaces an older diff with "body unchanged". The PWA shows it in a **Changes**
  tab and a strip under the source head, on every tab. Git ignores it like the log: the snapshot
  itself is committed, and its own diff is canonical.
- **No history, no compression.** Only the last change is recorded; keeping superseded revisions (or
  compressing them) is a separate decision and stays open.

## Consequences

- A vault with more sources than gap-minutes per cadence sweeps slower than its cadence. That is
  documented and deliberate; the fix is a larger gap budget, not a silent burst.
- The diff deliberately reports a reflow as a change even though the outline rebuild keeps the claims
  of a reflowed part: the record answers "what does the page say now", the tolerance answers "is the
  claim still true".
- The change record and the change time are separate facts. A title-only change writes a record and
  touches no claim; an unchanged check touches neither.
- The index gains a partial index (`kind = 'url'`, `url_checked_at`). No shape change.
- A committed vault keeps the full history of a snapshot in git; the record is the in-app story and
  is not part of that history.
