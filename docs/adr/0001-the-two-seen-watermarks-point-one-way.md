# 0001 — The two seen watermarks point one way

Status: superseded by [0007](0007-one-seen-watermark-the-front-door-summarizes.md), 2026-11-20.

## Context

The reader keeps two shared watermarks in `.thought-keeper/reader/seen.md`: Start's drives the front
door's "since your last visit" statistic, Sources' drives the **new** and **updated** marks of the
list. Both answer the same vault activity — an arrival or a page whose readable content changed.

## Decision

- A **Start** visit settles `start_seen_at` alone. The front door is a summary, so the Sources marks
  survive it.
- A **Sources** visit settles both. The list answers the same batch the statistic summarizes, so a
  reader who has read the list has seen the summary too.
- The record is written scope by scope (`WriteScopes`): a visit never materializes a watermark the
  reader did not visit. A legacy `seen_at` line seeds both scopes on read and survives until both
  have an instant of their own.
- The activation names every watermark it settled and frames each one over SSE, so the statistic of
  another window clears when a Sources visit settles Start.
- The Start statistic counts the **batch** and names its **main sources**: every matching source is
  attributed to its owner (`parent_hash`), so a feed stands for its articles and a sitemap for its
  pages. The number is the count of sources, the chips are the owners.

## Consequences

- Opening Start never clears the list's marks; opening the list clears the statistic.
- The statistic never names an article; the list is where an article's own mark lives.
- A new lens that summarizes activity has to declare which watermark it settles in
  `Reader.Activate` — the asymmetry is a rule, not an accident of one page.
