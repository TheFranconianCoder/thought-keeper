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

## Addendum (2026-11-20): the shelf is built, not assumed

The decision above assumed the shelf would appear with a coarsening press. A vault that predates it
therefore kept its 57k names, and the derivation kept filing new claims onto them: the catalog came
from the observed claims, not from the record, so the shelf was a file nothing read.

Two amendments:

- The app **writes** the topics shelf itself when a catalog runs past the target: the most used
  names, on the reconcile tick, with no model, no gate and no claim rewritten. A shelf that exists
  and fits the target is a record and is left alone; a target that moved down trims it. A vault
  without an API key gets one too.
- The derivation **reads** it: candidates come from the shelf when one exists, and an off-shelf
  proposal is mapped onto the best fitting shelf name by the budget path. The observed catalog stays
  the fallback for a vault that has none.

The long tail is still the repair: filing 57k names onto 3,000 rewrites claims, holds the exclusive
gate and costs a model pass, so it stays a press (the Coarsen button on Status) — and the tag
namespaces keep their coarsening-only shelf, because their write path reads `Canonical`.

## Addendum (2026-11-21): literal variants and parallel questions

The decision above described two passes and one question at a time. Two amendments:

- A tail name that carries a shelf name as a **contiguous phrase of at least two words** is filed
  with no model call: `sample-size-insensitivity` onto `sample-size`, `go-code-generation` onto
  `code-generation`. The rule is literal on purpose: a shared subject word never qualifies, so
  `moral-psychology` is not `evolutionary-psychology` — a token overlap would have filed it. The
  pass sits between the near-duplicate families and the model, and it chunks its rename (one
  transaction per 2,000 names), because a rename rewrites claim files.
- The model's questions are independent and one answer takes minutes, so **four are in flight at
  once**. Only the **apply** stays in order: it rewrites claims and the remaining count is read from
  it. The gateway keeps the process-wide cap (`openai.max_concurrent`), so this is latency, not an
  unbounded burst. A failed or cancelled pass stops the outstanding questions, and the empty-batch
  counter still ends a shelf that cannot cover its tail.

The deterministic passes (families and variants) need no key: the model guard sits after them, so a
vault without one still files what a rule can decide.

## Addendum (2026-11-21): the coarsening is housekeeping, not a button

A measured vault (48,153 topic names, shelf 3,000, 407k claims) filed 2,397 names in one press and
stopped there: `vocabularyGateWait + vocabularyUnitTimeout` caps a vocabulary run at forty minutes,
and the tail needs hours of model time at ~110 names a minute. Nothing asked for the next pass, so
the only way to finish was to press a button nineteen times. Two things followed from that measurement
and amend the decision above:

- **The pass repeats itself.** The reconcile tick asks for a coarsening while a namespace still has
  names outside the shelf — the smallest tail first — and asks again after the cooldown, until the
  tail is settled. A pass is bounded by `vocabulary.coarsen.budget` (15 min): it stops ASKING when
  the budget is spent, applies the questions already in flight and returns, so the exclusive gate
  goes back to the document steps between passes. The run's own ceiling stays as the outer bound.
- **A declined name is a decision.** The names a batch asked about and the model did not file are
  recorded in `Decided` — the vocabulary's existing memory of judged names — in the same write as
  the mapping. Without it every pass re-asked the same tail forever, and `remaining` described names
  nobody would ever ask about twice. With it the count converges, the statistic means work left, and
  the pass ends by itself. An answer that maps **nothing** is not a decision: it settles nothing and
  counts as a failed question, so one broken completion cannot poison the tail. Three such questions
  in a row stop the pass, replacing the empty-batch counter that assumed a declined name would be
  asked again.
- A single failed question **no longer ends the pass**: its batch stays open and the next question is
  asked. At 300 s of `openai.timeout` against answers that average ~3.5 minutes, one timeout per two
  thousand names must not throw away the other twenty-nine minutes of a pass.

The manual press stays as "run one now" — the same pass with the run's ceiling instead of the budget,
and it resets the progress bound so a stalled tail can be retried by hand.

## Addendum (2026-11-21): the shelf follows the corpus

The decision above made the shelf a RECORD: built once, trimmed when the knob moves down, filed onto
by the coarsening. A measured vault showed what that means at the end of the migration: 2,397 names
filed, 45,310 settled as "the shelf cannot honestly cover this" — and 45,310 claim labels stayed
fine forever. The vocabulary had not been reduced to the shelf; the tail had only stopped being asked
about. And the shelf itself could not change: the derivation maps every proposal onto it, so a subject
that arrives after the build can never appear.

Three amendments, and they are one mechanism:

- **The reduction has two stages.** The coarsening renames a NAME (one mapping moves all its claims),
  so it can only place names that mean one thing. The claims of a name it declines are re-derived one
  batch at a time by `POST /vocabulary/retopic`, resolving onto the shelf. Its queue is the names
  outside the shelf that no pass has answered for yet, most used first; a name is recorded in
  `Relabelled` once every batch of its claims has been attempted, so a name that IS the subject is a
  remainder the report shows, never a question that comes back. `off_shelf` (the claim mass outside)
  is the honest distance to the invariant; `remaining` (names) is the bulk queue.
- **The shelf is re-synthesized from the corpus.** When a namespace has nothing left to ask, the
  reconcile tick re-fits its shelf at most once per `vocabulary.shelf.refit_interval`: a name outside
  it that carries twice the coldest slot (never fewer than three claims) takes that slot. The refit is
  deterministic and writes a record; the claims on an evicted name go off the shelf and the retopic
  pass repairs them. The shelf therefore moves FIRST and the corpus follows — the other order never
  moves at all.
- **A new subject may name itself.** The derivation keeps a proposal that is not on the shelf while the
  names outside it stay under the shelf's **headroom** (a tenth of it). A settled vault has room for
  new subjects, which is what the refit needs in order to promote them; a vault still filing its tail
  has none, so nothing competes with the reduction it is doing.

A repair pass never rewrites the vocabulary record: the shelf, the alias table (`Absorbed`), the judged
names (`Decided`) and the re-derived ones (`Relabelled`) are decisions, and the old retopic pass threw
all of them away by writing a fresh `Canonical` from what the claims said.

## Addendum (2026-11-21): assignment is the rule, and a batch is not a name

Two numbers from the same running vault changed the last two decisions.

- **Two thirds of the tail was passed over.** With the prompt saying "a name the shelf cannot honestly
  cover may be left out", the model filed ~35 % of every batch. After the tail settled, 82 % of the
  claims (341,194 of 415,000) still carried a fine label, and the passes that were supposed to reduce
  them had nothing left to ask. The permission was the bug: a shelf name that is merely BROADER than a
  name is still the right place for it. The prompt now assigns every name and leaves one out only when
  no shelf name covers its subject at all. That is a policy, so it has a **version**: the vocabulary
  records `settle:`, and a pass that finds an older version drops the verdicts of the names OUTSIDE
  the shelf and asks them again. A verdict about a shelf name is untouched — the rule is about filing,
  not about the shelf.
- **A retopic batch is taken across names.** The question is the claim's own text; the name it was
  filed under only decides when it is settled. Batching per name therefore spent one call per name —
  45,000 calls for 341,000 claims, each carrying ~7.5 claims — while batches of a hundred drawn from
  the whole queue need ~3,400. A name is recorded in `Relabelled` when its last claim has been asked,
  so the convergence property does not change, only the unit of asking.

The write path is deliberately NOT changed: it already resolves a proposal against the whole part
text, and when that text shares no token with any shelf name the name is a subject the shelf does not
have. Forcing it onto an arbitrary shelf name would say something the claim does not, and the refit —
which promotes a name by the mass it carries — is the honest way back in.
