# 0010 — the topic target is 1,000, and Ask reads the shelf

**Status:** accepted, 2026-11-22. Amends the target of [ADR 0006](0006-the-topic-is-a-shelf.md) and the
orientation of the query agent; the share band of [ADR 0009](0009-a-topic-is-judged-by-its-share.md) is
unchanged.

## Context

Two measurements on the same vault (420,184 claims, 2026-11-22) decided this:

- **The ceiling never bound.** The topic shelf held **698 names against a target of 3,000**, because the
  share band is what decides how many names are usable: at `min_share` 0.0001 a name needs 42 claims,
  and only 696 names were in band. Lowering `min_share` to 0.00001 (4 claims) would have yielded 706.
  The distribution is bimodal — 4,315 names carried three claims or fewer, then the next name carried
  43 — so the missing granularity was not a knob the ceiling could give. 3,000 described a target the
  vault could never reach, and a target that is four times the reality is not a bound.
- **Ask read the alphabet.** `list_topics` cut the catalog at 120 entries. The repository answers
  `ORDER BY topic`, and the slice happened before any sort, so the agent received the alphabetically
  first 120 names: **15.2 %** of the claim mass, a median of 199 claims, single-claim names included —
  while the most used 120 carried **60.4 %**. The shelf was never consulted, so a catch-all outlier was
  shown and a shelf name that carried few claims was invisible.

## Decision

- **The topics target is 1,000** (`vocabulary.topics.max_names`). It is a navigable ceiling, not a
  quota: the share band still decides how many names are usable, and a shelf may be smaller. The three
  ceilings (`topics`, `source_tags`, `wiki_tags`) become **writable** in the Config overlay: they are
  vault-scoped, the running process applies them by rebuilding its wiring, and a target that moves down
  trims the shelf on the next look.
- **`list_topics` reads the SHELF, not the catalog.** The digest is the names the claims may be filed
  under, judged by the share band, most used first. The names the **question** resolves onto come first
  and carry `matches_question`; the resolution reuses the derivation's IDF matcher, so a name scores by
  the rare words it shares with the question instead of by a shared "software".
- **The digest is a HEAD.** At most 80 entries (20 of them question matches): the agent loop cuts a
  tool answer at 6,000 characters, and the whole shelf would not fit. A head of the shelf is an
  orientation; the model opens claims with `search`.
- **A vault without a shelf is answered from its catalog**, unfiltered. A catalog below the target IS
  the shelf (ADR 0006: there is no tail to bound), and with no record there is no band to judge against.

## Consequences

- A vault whose shelf was built for 3,000 is trimmed to 1,000 on the next look. The evicted names'
  claims go off the shelf and into the retopic queue — the same path a moved-down knob already had.
- Ask no longer sees the alphabetical head, a catch-all the refit has not evicted yet, or a thin name.
  Recall is untouched: it comes from FTS5 and the clusters, never from the topic list.
- The orientation is bounded by the shelf, so a subject the shelf does not carry cannot be found through
  `list_topics`. That is the shelf's contract, not a limitation of the tool: `search` finds the claims.
- The assistant's own digest (`command_run`) already sorts the catalog by uses; it is not changed here.
