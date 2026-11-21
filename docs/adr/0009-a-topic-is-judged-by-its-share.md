# 0009 — A topic is judged by its share of the claims

Status: accepted, 2026-11-21. Amends the refit rule of [ADR 0006](0006-the-topic-is-a-shelf.md)
(addendum "the shelf follows the corpus").

## Context

The shelf is the bounded vocabulary claims are filed under. Its quality is not the number of its names
but how the claims spread over them. Two failure shapes exist:

- **Catch-all**: one topic carries a large share of all claims and distinguishes nothing. The refit
  promoted by mass alone, so the most used generic word was the first candidate for a slot.
- **Thin**: a topic carries one or a few claims. A slot held by it is a slot a real subject does not get.

The refit used absolute counts (a promotion floor of three claims, a margin of two times the coldest
slot). Counts do not scale: three claims are noise in a vault of 100,000 and the floor says nothing
about a vault of 400,000.

## Decision

- **Judged by share.** A topic's share is its claims over ALL claims of the namespace. Two limits, both
  relative:
  - `vocabulary.shelf.max_share` (default `0.02`): above it the topic is a **catch-all**.
  - `vocabulary.shelf.min_share` (default `0.0001`): below it the topic is **thin**.
- **Identified by share alone.** No word lists, no name patterns and no absolute counts decide it.
- **Promotion** needs a share inside the band. A catch-all is never promoted, a thin name never earns a
  slot. The margin rule for a full shelf is unchanged.
- **Eviction** is not limited to full shelves: a shelf name above `max_share` or below `min_share` gives
  its slot up, even with free slots. Its claims go off the shelf, and the retopic pass re-derives them.
  The shelf moves first, the corpus follows — the same order as before.
- **Zero is off.** A zero limit disables that rule. The shipped defaults are always set; the zero case
  exists so an unconfigured use case never judges by accident.

## Consequences

- The limits hold for any corpus size. A vault that grows does not reclassify its topics by accident.
- Small vaults have noisy shares: three claims in a hundred are three percent. The shelf only exists
  past its target (3,000 names), so the rules run where the numbers are large enough.
- The write path is share-aware too (see the write-time addendum below).
- The report counts catch-alls and thin names and their claim mass for the Status lens, so "is the
  housekeeping correct" is a number, not a feeling.

## Addendum: judged at the write and in the housekeeping

The band also governs where a claim is filed, not only which names the refit keeps. A claim ends on an
in-band topic or on an honest remainder; it is never pushed into a catch-all or a thin name to make the
count work.

- **Offered and mapped names are in band.** The derivation's candidates, the near-duplicate mapping and
  the budget mapping skip a catch-all and a thin name. The retopic repair and the coarsening's shelf
  candidates follow the same rule.
- **An existing outlier is kept as proposed.** A proposal that names an existing catch-all or thin name is
  not moved to another name. The refit evicts the name and the retopic pass re-derives its claims.
- **A proposal no in-band name fits is kept.** Over the budget it stays a new name, as before, and is
  judged at the next refit. Its claims are an honest remainder, never a forced move.
- **One bounded fallback is kept on purpose.** Over the budget, with a candidate that is in band, the
  proposal still maps onto that candidate. Without that the outside tail would grow without a bound
  between two refits. This is the only place a claim is moved onto an existing name for the count, and it
  is an in-band one.
