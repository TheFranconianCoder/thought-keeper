# 0005 — The derivation keeps model windows

Status: accepted, 2026-11-19.

## Context

The agent's endpoint is billed by usage, and its provider has hours in which calls are expensive
(DeepSeek's `01:00-04:00` and `06:00-10:00` UTC). The reader wants the derivation to stay out of
them, while keeping the app usable: a question and the assistant are interactive reads whose cost
is paid for the answer the reader is waiting for.

The pipeline has two kinds of model use:

- **Derivation** — the outline's naming, distill, the source summary, the source and wiki auto tags,
  the vocabulary curation and the retopic. It is background work whose pace the app already steers.
- **Reading** — a query's answer and the assistant's reading of an utterance. Ask is explicitly
  never gated and runs in parallel.

A window is not a failure and not a pause of the app: capture (fetch, extract, OCR) is local and the
cluster step has no model at all.

## Decision

- **`openai.blocked_windows`** is a list of UTC ranges, `HH:MM-HH:MM`, start inclusive and end
  exclusive; a range whose start is later than its end wraps midnight. The default is DeepSeek's
  pair; an explicit empty list means around the clock. It is a MACHINE knob: the provider's hours are
  not a property of the vault.
- **The scheduler enforces it, twice.** The pump claims no derivation step inside a window (capture
  still runs, the cluster step still runs), the summary and tag backfills and the vocabulary check
  do nothing, and a manual derivation action answers `409` with the reopening instant. The guarded
  gateway (`WindowedAgent`) is the second line: it refuses a call that reaches it anyway and cuts a
  call that is still running when the window starts. Nothing of the derivation spends a token inside
  a window.
- **A cut is a deferral, not a failure.** The run row is `aborted` with `deferred: the model window
  is closed until …`; the error keeps the sentinel so the app knows. Finished parts, written claims
  and applied merges stay — they are the work of the previous windows. The next window's wake
  resumes it.
- **The work is resumed, not polled.** One wake is armed for the next opening. It asks for a
  pipeline pass, tops the summary and tag backfills up, asks the vocabulary for its next check, and
  re-queues the wiki pages whose tagging was held (a wiki page has no backfill to walk).
- **What waits is reported.** `/pipeline` carries `model_window` while one is open; the Status lens
  says the derivation waits and names the opening in UTC.
- **The interactive paths keep the raw gateway.** A query and the assistant are not derivation, and
  the job a reader waits for is not a batch.

## Consequences

- A source captured inside a window is readable but has no outline or claims until the window opens;
  the wait is visible on Status and in the source's lights.
- A call cut at the boundary loses what that call would have answered: the part is derived again
  after the window, and the tokens spent before the cut bought nothing. This is the price of the
  rule being about the clock, not about politeness.
- A tree built through a manual path inside a window (a wiki save, a stale-outline rebuild) still
  builds deterministically; its model-named title is refused and falls back to the deterministic
  title chain. The outline itself is never lost.
- The list is read on every decision, so a change applies to the next claim; the guarded gateway is
  rebuilt with the wiring on a config apply.
