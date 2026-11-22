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

## Addendum (2026-11-22): the build order and the refit's place in the queue

Two defects in the implementation, not in the rule:

- **The shelf was built from an unsorted catalog.** `ListClaimTopics` answers alphabetically, and the
  build sliced the first `max_names` of that order. A measured vault therefore held the alphabetically
  first 3,000 names (`10000-hour-rule … animal-feed-composition`), which covered 38 % of its claims,
  while the most used 3,000 covered 82 %. The catalog is now ordered by uses before any head is taken,
  and the build rule is versioned in the record (`shelf_built:`). A shelf without the current version
  is rebuilt once; `Decided` and `Relabelled` are dropped with it, because their verdicts were made
  against names that were never a target.
- **The refit was gated behind a settled tail.** "The shelf moves first, the corpus follows" was
  implemented as `tail == 0 && retopic == 0`, which is unreachable on the vault the refit exists for:
  a wrong shelf leaves a tail that cannot settle. The refit now runs when its interval is due, before
  the filing passes of the same look. It still returns after a move so the next look reads the report
  of the new shelf; the coarsening does not chase a target that is moving inside one look.

Also recorded: the target is a ceiling, not a quota. With `min_share` at `0.0001` a 419,541-claim
vault has 721 in-band names, so a 3,000-name shelf cannot be populated from in-band names; the refit
keeps the band and the shelf is honestly smaller. The retopic pass measures its progress in claims,
not names, because one large name can take several passes; and an alias is only applied when it lands
on the shelf, so the repair cannot re-file a claim under the fine name it just left.
