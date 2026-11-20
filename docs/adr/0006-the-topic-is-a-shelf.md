# ADR 0006 — the topic is a shelf

**Status:** accepted, 2026-11-20

## Context

A claim carried exactly one flat topic, and the distill prompt asked for the FINEST subject of that
claim — "a name used once is fine, do not worry about how many names come out". The vocabulary was
curated afterwards, but only for synonyms: the curation rules said in as many words that a shorter
list is not a better vocabulary, and every name the model judged was written to `Decided` and never
asked about again.

The measured result on a real vault (2026-11-20): **57,181 topic names for 402,931 claims**, 34 %
singletons, a 2.3 MB catalog that no prompt can carry. `list_topics` answered 2.3 MB and the agent
loop cut it at 6,000 bytes — mid-JSON. So the mechanism that was supposed to make names reusable was
not merely expensive, it was unusable: the derivation called it 1,983 times in one morning, paid
1,988 extra round trips and ~5.0 Mio. tokens, and reused nothing.

Deterministic consolidation (the near-duplicate detector's Jaccard families) reaches 54,793 names:
**−4 %**. Lexical merging cannot fix a granularity problem.

## Decision

**The topic is a shelf, not the finest subject of one claim.**

- A namespace has a **shelf**: the bounded set of names a claim may be filed under
  (`vocabulary.topics.max_names`, default 3,000; `vocabulary.source_tags.max_names`, default 200).
- The topic is navigation. What the claim SAYS is the claim; the fine subject no longer has a name
  of its own, and losing it costs nothing a reader had.
- The derivation resolves every proposed name against the catalog at WRITE time: alias → canonical,
  a name that contains an existing one → that name, otherwise a new name while the budget has room,
  and afterwards the best fitting shelf name. A mapping is recorded in the source log as
  `topic_mapped`.
- `POST /vocabulary/coarsen` files an existing long tail onto the shelf: deterministic families first
  (no model), then the model in batches, each applied before the next question, with `Absorbed` as the
  watermark.

## Consequences

- The catalog stops growing with the corpus. A new field can still enter while the shelf has room;
  once it is full, the curation (or the coarsen run) must make room by merging — a vocabulary that
  cannot be navigated is not a vocabulary.
- The candidates a part is offered are **selected, not enumerated**: two dozen names chosen by
  IDF-weighted overlap with the part's text, in the same request. That removes a round trip per part
  and gives the model names that actually fit.
- `retopic` must map onto the shelf instead of inventing names; otherwise it reproduces the explosion.
- A coarsening rewrites up to hundreds of thousands of claim files. It runs in the exclusive gate and
  is resumable, because each batch is applied and recorded before the next is asked.
- 3,000 is a TARGET, not a wall: the hard guard at 1.5× the target is what prevents a relapse, and
  the shelf is trimmed to the most used names when the knob moves down.
