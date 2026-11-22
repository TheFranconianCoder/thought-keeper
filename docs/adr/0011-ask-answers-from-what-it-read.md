# 0011 — Ask answers from what it read

Date: 2026-11-22

## Context

Ask ran on the derivation's `openai.max_iterations` (16). One real multi-part question spent all 16
turns (17 with the wrap-up), 151k tokens and 92 seconds, and answered with a note that part of the
research budget was gone: two of its four subjects had no readable evidence. The loop already took a
final turn without tools, but the run still said `succeeded`, the flag existed only in a log line,
and a search's 20 hits with full claim and body were cut at 6 000 bytes with no way to page them.

The two operations bound different work. A derivation part is one small node (a couple of turns); a
question may be several subjects, and each subject needs its own evidence.

## Decision

- **Ask has its own turn budget**, `ask.max_iterations` (default 32). The derivation keeps
  `openai.max_iterations` and its per-part clamp (6–14).
- **A spent budget answers.** Both the iteration ceiling and the run's deadline take one final turn
  without tools. The loop keeps the last 90 seconds of `pipeline.run_timeout` for that turn. The run
  is marked `truncated` (a run column, not an error): the answer is partial, not failed.
- **A search answer is a page.** `search` takes `limit` (default 8, max 20) and `offset`, and returns
  `next_offset`. A page fits one tool answer, so the model reads evidence instead of the start of a
  truncated array. Ask carries up to 8 000 bytes per tool answer and 32 000 characters of tool output.
- **A multi-part question is several sub-questions.** The prompt sends their searches in one turn,
  as several tool calls, instead of spending a turn per part.

## Consequences

- A broad question costs more: 32 turns is roughly 300–400k tokens and 3–4 minutes in the worst case.
  `pipeline.run_timeout` remains the outer wall, and the reserve makes the loop stop before it.
- Distill is unchanged; its budget is still small and clamped per part.
- `GET /runs/{id}` carries `truncated`. The Ask row does not show a badge yet; the model's own note
  still names what stayed open.
- A scoped search's provenance fallback can return less than a full page when the repository lacks
  the scoped method; the offset still means the same thing.
