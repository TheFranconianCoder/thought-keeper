# 0007 — One seen watermark: the front door summarizes the unread batch

Status: accepted, 2026-11-20. Supersedes
[0001](0001-the-two-seen-watermarks-point-one-way.md).

## Context

0001 gave each lens its own watermark so that opening the front door could not clear the list's
marks. The consequence it accepted turned out to be worse than the problem: after a Start visit the
statistic said "nothing since your last visit" while the Sources list still flagged the same sources
as **new** and **updated**, because only a list visit had ever advanced its watermark. A reader who
rarely opens the list saw a growing backlog of marks that the front door kept denying, and the two
views described different windows of the same vault.

## Decision

- The vault keeps **one seen watermark**: the batch of sources — arrivals and real content changes —
  whose list the reader has not opened yet.
- **Only a Sources visit settles it.** It moves forward to the newest activity, so one visit clears
  the **new** and the **updated** mark alike, and a vault's first visit initializes it to "now".
- **A Start visit settles nothing.** The front door answers the same baseline and summarizes the same
  batch, so its statistic and the list's rows can never disagree. `scope=start` is a read: it writes
  no file and publishes no event, which also means an older client cannot clear the marks by
  rendering the front door.
- The record, the wire and the entity carry one instant (`sources_seen_at`). A record from the split
  era answers with `sources_seen_at` — it is the baseline the marks had; the retired
  `start_seen_at` line is ignored and dropped by the next write, as is the older single `seen_at`
  line once it has been replaced.

## Consequences

- Opening the list clears the statistic and the marks together; opening Start clears nothing.
- A reader who never opens the list sees the batch on the front door grow. That is the intended
  reading: the card is the list's own backlog, not a digest of the last front-door visit — which is
  why it is titled "Unseen in Sources" and not "Since your last visit".
- The activating window still keeps the batch it settled for its active phase, so the reader can see
  which rows it came for; every other window drops them at once, and the next phase clears the rest.
- A future lens that shows the batch reads the same watermark. There is no scope left to declare —
  only the rule that a lens either summarizes (settles nothing) or is the list (settles).
